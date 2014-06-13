// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by a
// license that can be found in the LICENSE file.

// Package serviced - agent implements a service that runs on a serviced node.
// It is responsible for ensuring that a particular node is running the correct
// services and reporting the state and health of those services back to the
// master serviced.
package serviced

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zenoss/glog"
	docker "github.com/zenoss/go-dockerclient"
	"github.com/zenoss/serviced/commons"
	coordclient "github.com/zenoss/serviced/coordinator/client"
	coordZK "github.com/zenoss/serviced/coordinator/client/zookeeper"
	"github.com/zenoss/serviced/domain"
	"github.com/zenoss/serviced/domain/pool"
	"github.com/zenoss/serviced/domain/service"
	"github.com/zenoss/serviced/domain/servicestate"
	"github.com/zenoss/serviced/domain/user"
	"github.com/zenoss/serviced/proxy"
	"github.com/zenoss/serviced/utils"
	"github.com/zenoss/serviced/volume"
	zkDocker "github.com/zenoss/serviced/zzk/docker"
	zkService "github.com/zenoss/serviced/zzk/service"
	zkVirtualIP "github.com/zenoss/serviced/zzk/virtualips"
)

/*
 glog levels:
 0: important info that should always be shown
 1: info that might be important for debugging
 2: very verbose debug info
 3: trace level info
*/

const (
	dockerEndpoint     = "unix:///var/run/docker.sock"
	circularBufferSize = 1000
)

// HostAgent is an instance of the control plane Agent.
type HostAgent struct {
	master         string              // the connection string to the master agent
	uiport         string              // the port to the ui (legacy was port 8787, now default 443)
	dockerRegistry string              // the docker registry to use
	varPath        string              // directory to store serviced data
	vfs            string              // driver for container volumes
	mount          []string            // each element is in the form: dockerImage,hostPath,containerPath
	dockerDNS      []string            // docker dns addresses
	zkclient       *coordclient.Client // zookeeper client
	shutdown       chan interface{}    // signal channel to shut down the host
	hostID         string              // the host ID
}

func NewHostAgent(master, uiport, dockerRegistry, varPath, vfs string, mount, dockerDNS, zookeepers []string) (*HostAgent, error) {
	agent := &HostAgent{
		master:         master,
		uiport:         uiport,
		dockerRegistry: dockerRegistry,
		varPath:        varPath,
		vfs:            vfs,
		mount:          mount,
		dockerDNS:      dockerDNS,
	}

	dsn := func() string {
		if len(zookeepers) == 0 {
			zookeepers = []string{"127.0.0.1:2181"}
		}
		return coordZK.DSN{Servers: zookeepers, Timeout: 15 * time.Second}.String()
	}()

	var err error

	agent.zkclient, err = coordclient.New("zookeeper", dsn, "", nil)
	if err != nil {
		return nil, err
	}

	agent.hostID, err = utils.HostID()
	if err != nil {
		panic("could not get host id")
	}

	agent.shutdown = make(chan interface{})
	go agent.start()
	return agent, nil
}

func (a *HostAgent) Shutdown() {
	glog.V(2).Info("Issuing shutdown signal")
	close(a.shutdown)
}

// AttachService attempts to attach to a running container
func (a *HostAgent) AttachService(done chan<- interface{}, svc *service.Service, state *servicestate.ServiceState) error {
	dc, err := docker.NewClient(dockerEndpoint)
	if err != nil {
		glog.Error("Could not create docker client: ", err)
		return err
	}

	container, err := dc.InspectContainer(state.DockerID)
	if err != nil {
		return err
	}

	glog.V(2).Infof("Agent.updateCurrentState got container state for docker ID %s: %v", state.DockerID, container)
	if !container.State.Running {
		return fmt.Errorf("container not running for %s", state.Id)
	}

	go a.waitInstance(dc, done, svc, state)
	return nil
}

// StopService terminates a particular service instance (serviceState) on the localhost
func (a *HostAgent) StopService(state *servicestate.ServiceState) error {
	dc, err := docker.NewClient(dockerEndpoint)
	if err != nil {
		glog.Error("Could not create docker client: ", err)
		return err
	}
	return dc.KillContainer(state.Id)
}

// CheckInstance looks up the docker container and updates the service state
func (a *HostAgent) CheckInstance(state *servicestate.ServiceState) error {
	dc, err := docker.NewClient(dockerEndpoint)
	if err != nil {
		glog.Error("Could not create docker client: ", err)
		return err
	}

	container, err := dc.InspectContainer(state.DockerID)
	if err != nil {
		return err
	}

	// update the service state
	state.DockerID = container.ID
	state.Started = container.Created
	state.PrivateIP = container.NetworkSettings.IPAddress
	state.PortMapping = make(map[string][]domain.HostIPAndPort)
	for k, v := range container.NetworkSettings.Ports {
		var pm []domain.HostIPAndPort
		for _, pb := range v {
			pm = append(pm, domain.HostIPAndPort{HostIP: pb.HostIp, HostPort: pb.HostPort})
		}
		state.PortMapping[string(k)] = pm
	}
	return nil
}

func (a *HostAgent) StartService(done chan<- interface{}, svc *service.Service, state *servicestate.ServiceState) error {
	glog.V(2).Infof("About to start service %s (%s)", svc.Name, svc.Id)
	client, err := NewControlClient(a.master)
	if err != nil {
		glog.Errorf("Could not start ControlPlane client: %s", err)
		return err
	}
	defer client.Close()

	dc, err := docker.NewClient(dockerEndpoint)
	if err != nil {
		glog.Errorf("Cannot create docker client: %s", err)
		return err
	}

	// start from a known good state
	if err := dc.KillContainer(state.Id); err != nil {
		glog.Errorf("Cannot kill container %s: %s", state.Id, err)
	} else if err := dc.RemoveContainer(docker.RemoveContainerOptions{ID: state.Id, RemoveVolumes: true}); err != nil {
		glog.Errorf("Unable to remove container %s: %s", state.Id, err)
	}

	eventmonitor, err := dc.MonitorEvents()
	if err != nil {
		glog.Errorf("Cannot monitor docker events: %s", err)
		return err
	}
	defer eventmonitor.Close()

	// create the docker client config and host config structures necessary to create and start the service
	dockerconfig, hostconfig, err := a.configureContainer(client, done, svc, state)
	if err != nil {
		glog.Errorf("Cannot configure container: %v", err)
		return err
	}

	cjson, _ := json.MarshalIndent(dockerconfig, "", "    ")
	glog.V(3).Info(">>> CreateContainerOptions:\n", string(cjson))
	hcjson, _ := json.MarshalIndent(hostconfig, "", "    ")
	glog.V(2).Info(">>> HostConfigOptions:\n", string(hcjson))

	// pull the image from the registry first if necessary, then attempt to create the container
	registry, err := commons.NewDockerRegistry(a.dockerRegistry)
	if err != nil {
		glog.Errorf("Cannot use docker registry for %s: %s", a.dockerRegistry, err)
		return err
	}

	ctr, err := commons.CreateContainer(registry, dc, docker.CreateContainerOptions{Name: state.Id, Config: dockerconfig})
	if err != nil {
		glog.Errorf("Cannot create container %v: %s", dockerconfig, err)
		return err
	}

	glog.V(2).Infof("Created container %s for service %s (%s): %v", ctr.ID, state.Id, svc.Name, svc.Id, dockerconfig.Cmd)

	// use the docker client EventMonitor to listen to events from this container
	subscription, err := eventmonitor.Subscribe(ctr.ID)
	if err != nil {
		glog.Errorf("Cannot subscribe to Docker events on container %s: %s", ctr.ID, err)
		return err
	}

	evtmonitorChan := make(chan struct{})
	subscription.Handle(docker.Start, func(e docker.Event) error {
		glog.V(2).Infof("Container %s starting instance %s for service %s (%s): %v", e["id"], state.Id, svc.Name, svc.Id, dockerconfig.Cmd)
		evtmonitorChan <- struct{}{}
		return nil
	})

	if err := dc.StartContainer(ctr.ID, hostconfig); err != nil {
		glog.Errorf("Canno start container %s for service %s (%s): %s", ctr.ID, svc.Name, svc.Id, err)
		return err
	}

	// wait until we get notified that the container has started, or 10 seconds, whichever comes first
	// TODO: make timeout configurable
	timeout := 10 * time.Second
	select {
	case <-evtmonitorChan:
		glog.V(0).Infof("Container %s started instance %s for service %s (%s)", ctr.ID, state.Id, svc.Name, svc.Id)
	case <-time.After(timeout):
		// FIXME: WORKAROUND for issue where docker.Start event doesn't always notify
		if container, err := dc.InspectContainer(ctr.ID); err != nil {
			glog.Warningf("Container %s could not be inspected: %s", ctr.ID, err)
		} else {
			glog.Warningf("Container %s inspected for state %s", ctr.ID, container.State)
			if container.State.Running == true {
				glog.Infof("Container %s start event timed out, but is running - will not retyrn start timed out", ctr.ID)
				break
			}
		}
		return fmt.Errorf("start timed out")
	}
	go a.waitInstance(dc, done, svc, state)
	return nil
}

func (a *HostAgent) waitInstance(dc *docker.Client, done chan<- interface{}, svc *service.Service, state *servicestate.ServiceState) {
	defer close(done)
	exited := make(chan error)
	go func() {
		rc, err := dc.WaitContainer(state.DockerID)
		if err != nil || rc != 0 || glog.GetVerbosity() > 0 {
			// TODO: output of docker logs is potentaially very large
			// this should be implemented another way, perhaps a docker attach
			// or extend docker to give the last N seconds
			if output, err := exec.Command("docker", "logs", state.DockerID).CombinedOutput(); err != nil {
				glog.Errorf("Could not get logs for container %s", state.DockerID)
			} else {
				var buffersize = 1000
				if index := len(output) - buffersize; index > 0 {
					output = output[index:]
				}
				glog.Warningf("Last %d bytes of container %s: %s", buffersize, state.DockerID, string(output))
			}
		}
		glog.Infof("docker wait %s exited", state.DockerID)
		exited <- err
		// remove the container
		if err := dc.RemoveContainer(docker.RemoveContainerOptions{ID: state.DockerID, RemoveVolumes: true}); err != nil {
			glog.Errorf("Could not remove container %s: %s", state.DockerID, err)
		}
	}()

	if err := func(interval time.Duration, retries int) (err error) {
		for i := 0; i < retries; i++ {
			if err = a.CheckInstance(state); err == nil {
				return nil
			}
			<-time.After(interval)
		}
		return err
	}(3*time.Second, 30); err != nil {
		glog.V(2).Infof("Could not get service state for %s: %s", state.Id, err)
		return
	}

	glog.V(4).Infof("Looking for address assignment in service %s (%s)", svc.Name, svc.Id)
	for _, endpoint := range svc.Endpoints {
		if addressconfig := endpoint.GetAssignment(); addressconfig != nil {
			glog.V(4).Infof("Found address assignment for service %s (%s) with endpoint %s", svc.Name, svc.Id, endpoint.Name)
			var (
				proxyID  = fmt.Sprintf("%s:%s", state.ServiceID, endpoint.Name)
				frontend = proxy.ProxyAddress{IP: addressconfig.IPAddr, Port: addressconfig.Port}
				backend  = proxy.ProxyAddress{IP: state.PrivateIP, Port: endpoint.PortNumber}
			)
			proxyRegistry := proxy.NewDefaultProxyRegistry()
			if err := proxyRegistry.CreateProxy(proxyID, endpoint.Protocol, frontend, backend); err != nil {
				glog.Warningf("Could not start external address proxy for %s: %s", proxyID, err)
			}
			defer proxyRegistry.RemoveProxy(proxyID)
		}
	}

	status, err := getExitCode(<-exited)
	if err != nil {
		glog.V(1).Info("Unable to determine exit code for %s: %s", state.Id, err)
		return
	}

	switch status {
	case 0:
		glog.V(0).Info("Finished processing instance ", state.Id)
	case 2:
		glog.V(1).Info("Docker process stopped for instance ", state.Id)
	case 137:
		glog.V(1).Info("Docker process killed for instance ", state.Id)
	default:
		glog.V(0).Infof("Docker process exited %s for instance", status, state.Id)
	}
}

// configureContainer creates and populates two structures, a docker client Config and a docker client HostConfig structure
// that are used to create and start a container respectively. The information used to populate the structures is pulled from
// the service, serviceState, and conn values that are passed into configureContainer.
func (a *HostAgent) configureContainer(client *ControlClient, done chan<- interface{}, svc *service.Service, state *servicestate.ServiceState) (*docker.Config, *docker.HostConfig, error) {
	var (
		dockerconfig docker.Config
		hostconfig   docker.HostConfig
	)

	// get this service's tenantID for volume mapping
	var tenantID string
	if err := client.GetTenantId(svc.Id, &tenantID); err != nil {
		glog.Errorf("Failed getting tenantID for service %s (%s): %s", svc.Name, svc.Id, err)
	}

	// get the system user
	var (
		unused     int
		systemuser user.User
	)
	if err := client.GetSystemUser(unused, &systemuser); err != nil {
		glog.Errorf("Unable to get system user account for agent %s", err)
	}
	glog.V(1).Infof("System User %v", systemuser)

	dockerconfig.Image = svc.ImageID

	// get the endpoints
	dockerconfig.ExposedPorts = make(map[docker.Port]struct{})
	hostconfig.PortBindings = make(map[docker.Port][]docker.PortBinding)

	if svc.Endpoints != nil {
		glog.V(1).Infof("Endpoints for service %s (%s): %v", svc.Name, svc.Id, svc.Endpoints)
		for _, endpoint := range svc.Endpoints {
			if endpoint.Purpose == "export" { // only expose remote endpoints
				var p string
				switch endpoint.Protocol {
				case commons.UDP:
					p = fmt.Sprintf("%d/%s", endpoint.PortNumber, "udp")
				default:
					p = fmt.Sprintf("%d/%s", endpoint.PortNumber, "tcp")
				}
				dockerconfig.ExposedPorts[docker.Port(p)] = struct{}{}
				bindings := hostconfig.PortBindings[docker.Port(p)]
				hostconfig.PortBindings[docker.Port(p)] = append(bindings, docker.PortBinding{})
			}
		}
	}

	if tenantID == "" && len(svc.Volumes) > 0 {
		// FIXME: find a better way of handling this error condition
		glog.Fatalf("Could not get tenant ID and need to mount a volume for instance %s under service %s (%s)", state.Id, svc.Name, svc.Id)
	}

	// make sure the image exists locally
	registry, err := commons.NewDockerRegistry(a.dockerRegistry)
	if err != nil {
		glog.Errorf("Error using docker registry %s: %s", a.dockerRegistry, err)
		return nil, nil, err
	}

	dc, err := docker.NewClient(dockerEndpoint)
	if err != nil {
		glog.Errorf("Cannot create docker client: %v", err)
		return nil, nil, err
	}

	if _, err := commons.InspectImage(registry, dc, svc.ImageID); err != nil {
		glog.Errorf("Cannot inspect docker image %s: %s", svc.ImageID, err)
		return nil, nil, err
	}

	dockerconfig.Volumes = make(map[string]struct{})
	hostconfig.Binds = []string{}

	for _, volume := range svc.Volumes {
		subvolume, err := getSubvolume(a.varPath, svc.PoolID, tenantID, a.vfs)
		if err != nil {
			glog.Fatalf("Could not create subvolume: %s", err)
		} else {
			glog.V(2).Infof("Volume for service %s (%s)", svc.Name, svc.Id)
			resourcePath := path.Join(subvolume.Path(), volume.ResourcePath)
			glog.V(2).Infof("FullResourcePath: %s", resourcePath)
			if err := os.MkdirAll(resourcePath, 0770); err != nil {
				glog.Fatalf("Could not create resource path %s: %s", resourcePath, err)
			}

			if err := createVolumeDir(resourcePath, volume.ContainerPath, svc.ImageID, volume.Owner, volume.Permission); err != nil {
				glog.Errorf("Error populating resource path %s with container path %s: %s", resourcePath, volume.ContainerPath, err)
			}

			binding := fmt.Sprintf("%s:%s", resourcePath, volume.ContainerPath)
			dockerconfig.Volumes[strings.Split(binding, ":")[1]] = struct{}{}
			hostconfig.Binds = append(hostconfig.Binds, strings.TrimSpace(binding))
		}
	}

	dir, binary, err := ExecPath()
	if err != nil {
		glog.Errorf("Error getting exec path: %s", err)
		return nil, nil, err
	}

	volumeBinding := fmt.Sprintf("%s:/serviced", dir)
	dockerconfig.Volumes[strings.Split(volumeBinding, ":")[1]] = struct{}{}
	hostconfig.Binds = append(hostconfig.Binds, strings.TrimSpace(volumeBinding))

	err = svc.Evaluate(func(serviceID string) (service.Service, error) {
		var svc service.Service
		err := client.GetService(serviceID, &svc)
		return svc, err
	})
	if err != nil {
		glog.Errorf("Error injecting context: %s", err)
		return nil, nil, err
	}

	// bind mount everything we need for logstash-forwarder
	if len(svc.LogConfigs) != 0 {
		const LOGSTASH_CONTAINER_DIRECTORY = "/use/local/serviced/resources/logstash"
		logstashPath := utils.ResourcesDir() + "/logstash"
		binding := fmt.Sprintf("%s:%s", logstashPath, LOGSTASH_CONTAINER_DIRECTORY)
		dockerconfig.Volumes[LOGSTASH_CONTAINER_DIRECTORY] = struct{}{}
		hostconfig.Binds = append(hostconfig.Binds, binding)
		glog.V(1).Infof("Added logstash bind mount: %s", binding)
	}

	// add arguments to mount requested directory (if requested)
	glog.V(2).Infof("Checking mount options for service %s (%s)", svc.Name, svc.Id)
	for _, bindmountStr := range a.mount {
		glog.V(2).Infof("Bindmount is %s", bindmountStr)

		if splitMount := strings.Split(bindmountStr, ","); len(splitMount) >= 2 {
			requestedImage, hostPath := splitMount[0], splitMount[1]
			// assume the container path is going to be the same as the host path
			containerPath := hostPath
			if len(splitMount) > 2 {
				containerPath = splitMount[2]
			}
			glog.V(2).Infof("Mount requested image: %s; host path: %s; container path: %s", requestedImage, hostPath, containerPath)

			// insert tenantID into requestedImage - see dao.DeployService
			matchedRequestedImage := requestedImage == "*"
			if !matchedRequestedImage {
				imageID, err := commons.ParseImageID(requestedImage)
				if err != nil {
					glog.Errorf("Error parsing imageID %s: %s", requestedImage, err)
					continue
				}
				svcImageID, err := commons.ParseImageID(svc.ImageID)
				if err != nil {
					glog.Errorf("Error parsint service imageID %s: %s", svc.ImageID, err)
					continue
				}
				glog.V(2).Infof("Mount checking %#v and %#v", imageID, svcImageID)
				matchedRequestedImage = (imageID.Repo == svcImageID.Repo)
			}
			if matchedRequestedImage {
				binding := fmt.Sprintf("%s:%s", hostPath, containerPath)
				dockerconfig.Volumes[strings.Split(binding, ":")[1]] = struct{}{}
				hostconfig.Binds = append(hostconfig.Binds, strings.TrimSpace(binding))
			} else {
				glog.Warningf("Could not bind mount %s", bindmountStr)
			}
		}
	}

	// Get host IP
	ip, err := utils.GetIPAddress()
	if err != nil {
		glog.Errorf("Error getting host IP address: %v", err)
		return nil, nil, err
	}

	// add arguments for environment variables
	dockerconfig.Env = []string{
		fmt.Sprint("CONTROLPLANE_SYSTEM_USER=", systemuser.Name),
		fmt.Sprint("CONTROLPLANE_SYSTEM_PASSWORD=", systemuser.Password),
		fmt.Sprint("CONTROLPLANE_HOST_IP=", ip),
		fmt.Sprint("SERVICED_NOREGISTRY=", os.Getenv("SERVICED_NOREGISTRY")),
	}

	// add dns values to setup
	for _, addr := range a.dockerDNS {
		_addr := strings.TrimSpace(addr)
		if _addr != "" {
			dockerconfig.Dns = append(dockerconfig.Dns, _addr)
		}
	}

	// add hostname if set
	if svc.Hostname != "" {
		dockerconfig.Hostname = svc.Hostname
	}

	dockerconfig.Cmd = []string{
		fmt.Sprintf("/serviced/%s", binary),
		"service",
		"proxy",
		svc.Id,
		strconv.Itoa(state.InstanceID),
		svc.Startup,
	}

	if svc.Privileged {
		hostconfig.Privileged = true
	}

	return &dockerconfig, &hostconfig, nil
}

// main loop of the HostAgent
func (a *HostAgent) start() {
	defer func() {
		glog.Infof("Host %s received signal to shutdown", a.hostID)
		a.zkclient.Close()
	}()

	listen := func(conn coordclient.Connection) {
		defer conn.Close()

		// Watch virtual ip zookeeper nodes
		go zkVirtualIP.NewVIPListener(a.shutdown, conn, a, a.hostID).Listen()

		// Watch docker action nodes
		go zkDocker.NewActionListener(a.shutdown, conn, a, a.hostID).Listen()

		// Watch host nodes
		zkService.NewHostListener(a.shutdown, conn, a, a.hostID).Listen()
	}

	for {
		select {
		case <-time.After(time.Second):
			if conn, err := a.zkclient.GetConnection(); err == nil {
				listen(conn)
			}
		case <-a.shutdown:
			return
		}
	}
}

func (agent *HostAgent) BindVirtualIP(vip *pool.VirtualIP, index int) error {
	// check if the ip exists
	if vmap, err := mapVirtualIPs(); err != nil {
		return err
	} else if _, ok := vmap[vip.IP]; ok {
		return fmt.Errorf("requested virtual ip already on this host")
	}

	viname := vip.BindInterface + viPrefix + strconv.Itoa(index)
	if err := bind(vip, viname); err != nil {
		return err
	}

	return nil
}

func (agent *HostAgent) UnbindVirtualIP(vip *pool.VirtualIP) error {
	// verify the address lives on this host
	if vmap, err := mapVirtualIPs(); err != nil {
		return err
	} else if _, ok := vmap[vip.IP]; !ok {
		glog.Warningf("Virtual IP %s not found on this host", vip.IP)
		return nil
	} else if err := unbind(vip.InterfaceName); err != nil {
		return err
	}
	return nil
}

func (agent *HostAgent) AttachAndRun(dockerID string, command []string) ([]byte, error) {
	return utils.AttachAndRun(dockerID, command)
}

func getExitCode(err error) (int, error) {
	if exiterr, ok := err.(*exec.ExitError); ok {
		if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus(), nil
		}
	}
	return 0, err
}

func getSubvolume(varPath, poolID, tenantID, fs string) (*volume.Volume, error) {
	baseDir, _ := filepath.Abs(path.Join(varPath, "volumes"))
	if _, err := volume.Mount(fs, poolID, baseDir); err != nil {
		return nil, err
	}
	baseDir, _ = filepath.Abs(path.Join(varPath, "volumes", poolID))
	return volume.Mount(fs, tenantID, baseDir)
}
