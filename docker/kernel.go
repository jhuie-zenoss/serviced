package docker

import (
	"fmt"
	"os"
	"syscall"
	"time"

	dockerclient "github.com/zenoss/go-dockerclient"
)

const (
	dockerep = "unix:///var/run/docker.sock"
)

type request struct {
	errchan chan error
}

type inspectreq struct {
	request
	args struct {
		id string
	}
	respchan chan *dockerclient.Container
}

type listreq struct {
	request
	respchan chan []string
}

type oneventreq struct {
	request
	args struct {
		id    string
		event string
	}
}

type onstopreq struct {
	request
	args struct {
		id     string
		action ContainerActionFunc
	}
}

type startreq struct {
	request
	args struct {
		containerOptions *dockerclient.CreateContainerOptions
		hostConfig       *dockerclient.HostConfig
		action           ContainerActionFunc
	}
	respchan chan string
}

type stopreq struct {
	request
	args struct {
		id      string
		timeout uint
	}
}

var (
	cmds = struct {
		Inspect         chan inspectreq
		List            chan listreq
		OnContainerStop chan onstopreq
		OnEvent         chan oneventreq
		Start           chan startreq
		Stop            chan stopreq
	}{
		make(chan inspectreq),
		make(chan listreq),
		make(chan onstopreq),
		make(chan oneventreq),
		make(chan startreq),
		make(chan stopreq),
	}
	done  = make(chan struct{})
	srin  = make(chan startreq)
	srout = make(chan startreq)
)

// init starts up the kernel loop that is responsible for handling all the API calls
// in a goroutine.
func init() {
	client, err := dockerclient.NewClient(dockerep)
	if err != nil {
		panic(fmt.Sprintf("can't create Docker client: %v", err))
	}

	go kernel(client, done)
}

// kernel is responsible for executing all the Docker client commands.
func kernel(dc *dockerclient.Client, done chan struct{}) error {
	em, err := dc.MonitorEvents()
	if err != nil {
		panic(fmt.Sprintf("can't monitor Docker events: %v", err))
	}

	s, err := em.Subscribe(dockerclient.AllThingsDocker)
	if err != nil {
		panic(fmt.Sprintf("can't subscribe to Docker events: %v", err))
	}

	s.Handle(dockerclient.Create, eventToKernel)
	s.Handle(dockerclient.Delete, eventToKernel)
	s.Handle(dockerclient.Destroy, eventToKernel)
	s.Handle(dockerclient.Die, eventToKernel)
	s.Handle(dockerclient.Export, eventToKernel)
	s.Handle(dockerclient.Kill, eventToKernel)
	s.Handle(dockerclient.Restart, eventToKernel)
	s.Handle(dockerclient.Start, eventToKernel)
	s.Handle(dockerclient.Stop, eventToKernel)
	s.Handle(dockerclient.Untag, eventToKernel)

	eventactions := make(map[string]map[string]ContainerActionFunc)

	go startq(srin, srout)
	go scheduler(dc, srout, done)

	for {
		select {
		case req := <-cmds.Inspect:
			ctr, err := dc.InspectContainer(req.args.id)
			if err != nil {
				req.errchan <- err
				continue
			}
			close(req.errchan)
			req.respchan <- ctr
		case req := <-cmds.List:
			apictrs, err := dc.ListContainers(dockerclient.ListContainersOptions{All: true})
			if err != nil {
				req.errchan <- err
				continue
			}
			resp := []string{}
			for _, apictr := range apictrs {
				resp = append(resp, apictr.ID)
			}
			close(req.errchan)
			req.respchan <- resp
		case req := <-cmds.OnEvent:
			if action, ok := eventactions[req.args.event][req.args.id]; ok {
				action(req.args.id)
			}
			close(req.errchan)
		case req := <-cmds.OnContainerStop:
			if _, ok := eventactions[dockerclient.Stop]; !ok {
				eventactions[dockerclient.Stop] = make(map[string]ContainerActionFunc)
			}
			eventactions[dockerclient.Stop][req.args.id] = req.args.action
			close(req.errchan)
		case req := <-cmds.Start:
			srin <- req
		case req := <-cmds.Stop:
			err := dc.StopContainer(req.args.id, req.args.timeout)
			if err != nil {
				req.errchan <- err
				continue
			}
			close(req.errchan)
		case <-done:
			return nil
		}
	}
}

// scheduler handles starting up containers. Container startup can take a long time so
// the scheduler runs in its own goroutine and pulls requests off of the start queue.
func scheduler(dc *dockerclient.Client, rc <-chan startreq, done chan struct{}) {
	for {
		select {
		case sr := <-rc:
			ctr, err := dc.CreateContainer(*sr.args.containerOptions)
			switch {
			case err == dockerclient.ErrNoSuchImage:
				if pullerr := dc.PullImage(dockerclient.PullImageOptions{
					Repository:   sr.args.containerOptions.Config.Image,
					OutputStream: os.NewFile(uintptr(syscall.Stdout), "/def/stdout"),
				}, dockerclient.AuthConfiguration{}); pullerr != nil {
					sr.errchan <- err
					continue
				}

				ctr, err = dc.CreateContainer(*sr.args.containerOptions)
				if err != nil {
					sr.errchan <- err
					continue
				}
			case err != nil:
				sr.errchan <- err
				continue
			}

			err = dc.StartContainer(ctr.ID, sr.args.hostConfig)
			if err != nil {
				sr.errchan <- err
			}

			close(sr.errchan)

			if sr.args.action != nil {
				sr.args.action(ctr.ID)
			}

			sr.respchan <- ctr.ID
		case <-done:
			return
		}
	}
}

// startq implements an inifinite buffered channel of start requests. Requests are added via the
// in channel and received on the next channel.
func startq(in <-chan startreq, next chan<- startreq) {
	defer close(next)

	pending := []startreq{}

restart:
	for {
		if len(pending) == 0 {
			v, ok := <-in
			if !ok {
				break
			}

			pending = append(pending, v)
		}

		select {
		case v, ok := <-in:
			if !ok {
				break restart
			}

			pending = append(pending, v)
		case next <- pending[0]:
			pending = pending[1:]
		}
	}

	for _, v := range pending {
		next <- v
	}
}

func eventToKernel(e dockerclient.Event) error {
	ec := make(chan error)

	cmds.OnEvent <- oneventreq{
		request{ec},
		struct {
			id    string
			event string
		}{e["id"].(string), e["status"].(string)},
	}

	select {
	case <-time.After(1 * time.Second):
		return ErrRequestTimeout
	case <-done:
		return ErrKernelShutdown
	default:
		switch err, ok := <-ec; {
		case !ok:
			return nil
		default:
			return fmt.Errorf("docker: event handler failed: %v", err)
		}
	}
}
