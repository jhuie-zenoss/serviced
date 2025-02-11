// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/control-center/serviced/domain"
	"github.com/control-center/serviced/domain/addressassignment"
	"github.com/control-center/serviced/domain/servicedefinition"
	"github.com/control-center/serviced/utils"
)

// Desired states of services.
const (
	SVCRun     = 1
	SVCStop    = 0
	SVCRestart = -1
)

// Service A Service that can run in serviced.
type Service struct {
	ID                string
	Name              string
	Context           string
	Startup           string
	Description       string
	Tags              []string
	OriginalConfigs   map[string]servicedefinition.ConfigFile
	ConfigFiles       map[string]servicedefinition.ConfigFile
	Instances         int
	InstanceLimits    domain.MinMax
	ChangeOptions     []string
	ImageID           string
	PoolID            string
	DesiredState      int
	HostPolicy        servicedefinition.HostPolicy
	Hostname          string
	Privileged        bool
	Launch            string
	Endpoints         []ServiceEndpoint
	Tasks             []servicedefinition.Task
	ParentServiceID   string
	Volumes           []servicedefinition.Volume
	CreatedAt         time.Time
	UpdatedAt         time.Time
	DeploymentID      string
	DisableImage      bool
	LogConfigs        []servicedefinition.LogConfig
	Snapshot          servicedefinition.SnapshotCommands
	Runs              map[string]string
	RAMCommitment     uint64
	CPUCommitment     uint64
	Actions           map[string]string
	HealthChecks      map[string]domain.HealthCheck // A health check for the service.
	Prereqs           []domain.Prereq               // Optional list of scripts that must be successfully run before kicking off the service command.
	MonitoringProfile domain.MonitorProfile
}

//ServiceEndpoint endpoint exported or imported by a service
type ServiceEndpoint struct {
	servicedefinition.EndpointDefinition
	AddressAssignment addressassignment.AddressAssignment
}

// NewService Create a new Service.
func NewService() (s *Service, err error) {
	s = &Service{}
	s.ID, err = utils.NewUUID36()
	return s, err
}

// HasImports Does the service have endpoint imports
func (s *Service) HasImports() bool {
	if s.Endpoints == nil {
		return false
	}

	for _, ep := range s.Endpoints {
		if ep.Purpose == "import" {
			return true
		}
	}
	return false
}

//BuildServiceEndpoint build a ServiceEndpoint from a EndpointDefinition
func BuildServiceEndpoint(epd servicedefinition.EndpointDefinition) ServiceEndpoint {
	return ServiceEndpoint{EndpointDefinition: epd}
}

//BuildService build a service from a ServiceDefinition.
func BuildService(sd servicedefinition.ServiceDefinition, parentServiceID string, poolID string, desiredState int, deploymentID string) (*Service, error) {
	svcuuid, err := utils.NewUUID36()
	if err != nil {
		return nil, err
	}

	ctx, err := json.Marshal(sd.Context)
	if err != nil {
		return nil, err
	}

	now := time.Now()

	svc := Service{}
	svc.ID = svcuuid
	svc.Name = sd.Name
	svc.Context = string(ctx)
	svc.Startup = sd.Command
	svc.Description = sd.Description
	svc.Tags = sd.Tags
	svc.Instances = sd.Instances.Min
	svc.InstanceLimits = sd.Instances
	svc.ChangeOptions = sd.ChangeOptions
	svc.ImageID = sd.ImageID
	svc.PoolID = poolID
	svc.DesiredState = desiredState
	svc.Launch = sd.Launch
	svc.HostPolicy = sd.HostPolicy
	svc.Hostname = sd.Hostname
	svc.Privileged = sd.Privileged
	svc.OriginalConfigs = sd.ConfigFiles
	svc.ConfigFiles = sd.ConfigFiles
	svc.Tasks = sd.Tasks
	svc.ParentServiceID = parentServiceID
	svc.CreatedAt = now
	svc.UpdatedAt = now
	svc.Volumes = sd.Volumes
	svc.DeploymentID = deploymentID
	svc.LogConfigs = sd.LogConfigs
	svc.Snapshot = sd.Snapshot
	svc.RAMCommitment = sd.RAMCommitment
	svc.Runs = sd.Runs
	svc.Actions = sd.Actions
	svc.HealthChecks = sd.HealthChecks
	svc.Prereqs = sd.Prereqs

	svc.Endpoints = make([]ServiceEndpoint, 0)
	for _, ep := range sd.Endpoints {
		svc.Endpoints = append(svc.Endpoints, ServiceEndpoint{EndpointDefinition: ep})
	}

	tags := map[string][]string{
		"controlplane_service_id": []string{svc.ID},
	}
	profile, err := sd.MonitoringProfile.ReBuild("1h-ago", tags)
	if err != nil {
		return nil, err
	}
	svc.MonitoringProfile = *profile

	return &svc, nil
}

// GetServiceImports retrieves service endpoints whose purpose is "import"
func (s *Service) GetServiceImports() []ServiceEndpoint {
	result := []ServiceEndpoint{}

	if s.Endpoints != nil {
		for _, ep := range s.Endpoints {
			if ep.Purpose == "import" || ep.Purpose == "import_all" {
				result = append(result, ep)
			}
		}
	}

	return result
}

// GetServiceExports retrieves service endpoints whose purpose is "export"
func (s *Service) GetServiceExports() []ServiceEndpoint {
	result := []ServiceEndpoint{}

	if s.Endpoints != nil {
		for _, ep := range s.Endpoints {
			if ep.Purpose == "export" {
				result = append(result, ep)
			}
		}
	}

	return result
}

// GetServiceVHosts retrieves service endpoints that specify a virtual HostPort
func (s *Service) GetServiceVHosts() []ServiceEndpoint {
	result := []ServiceEndpoint{}

	if s.Endpoints != nil {
		for _, ep := range s.Endpoints {
			if len(ep.VHosts) > 0 {
				result = append(result, ep)
			}
		}
	}

	return result
}

// AddVirtualHost Add a virtual host for given service, this method avoids duplicates vhosts
func (s *Service) AddVirtualHost(application, vhostName string) error {
	if s.Endpoints != nil {

		//find the matching endpoint
		for i := range s.Endpoints {
			ep := &s.Endpoints[i]

			if ep.Application == application && ep.Purpose == "export" {
				_vhostName := strings.ToLower(vhostName)
				vhosts := make([]string, 0)
				for _, vhost := range ep.VHosts {
					if strings.ToLower(vhost) != _vhostName {
						vhosts = append(vhosts, vhost)
					}
				}
				ep.VHosts = append(vhosts, _vhostName)
				return nil
			}
		}
	}

	return fmt.Errorf("unable to find application %s in service: %s", application, s.Name)
}

// RemoveVirtualHost Remove a virtual host for given service
func (s *Service) RemoveVirtualHost(application, vhostName string) error {
	if s.Endpoints != nil {

		//find the matching endpoint
		for i := range s.Endpoints {
			ep := &s.Endpoints[i]

			if ep.Application == application && ep.Purpose == "export" {
				if len(ep.VHosts) == 0 {
					break
				}

				_vhostName := strings.ToLower(vhostName)
				if len(ep.VHosts) == 1 && ep.VHosts[0] == _vhostName {
					return fmt.Errorf("cannot delete last vhost: %s", _vhostName)
				}

				found := false
				vhosts := make([]string, 0)
				for _, vhost := range ep.VHosts {
					if vhost != _vhostName {
						vhosts = append(vhosts, vhost)
					} else {
						found = true
					}
				}
				//error removing an unknown vhost
				if !found {
					break
				}

				ep.VHosts = vhosts
				return nil
			}
		}
	}

	return fmt.Errorf("unable to find application %s in service: %s", application, s.Name)
}

// GetPath uses the GetService function to determine the / delimited name path i.e. /test/app/sevicename
func (s Service) GetPath(gs GetService) (string, error) {
	var err error
	svc := s
	path := fmt.Sprintf("/%s", s.Name)
	for svc.ParentServiceID != "" {
		svc, err = gs(svc.ParentServiceID)
		if err != nil {
			return "", err
		}
		path = fmt.Sprintf("/%s%s", svc.Name, path)
	}
	return path, nil
}

//SetAssignment sets the AddressAssignment for the endpoint
func (se *ServiceEndpoint) SetAssignment(aa *addressassignment.AddressAssignment) error {
	if se.AddressConfig.Port == 0 {
		return errors.New("cannot assign address to endpoint without AddressResourceConfig")
	}
	se.AddressAssignment = *aa
	return nil
}

//RemoveAssignment resets a service endpoints to nothing
func (se *ServiceEndpoint) RemoveAssignment() error {
	se.AddressAssignment = addressassignment.AddressAssignment{}
	return nil
}

//GetAssignment Returns nil if no assignment set
func (se *ServiceEndpoint) GetAssignment() *addressassignment.AddressAssignment {
	if se.AddressAssignment.ID == "" {
		return nil
	}
	//return reference to copy
	result := se.AddressAssignment
	return &result
}

//Equals are they the same
func (s *Service) Equals(b *Service) bool {
	if s.ID != b.ID {
		return false
	}
	if s.Name != b.Name {
		return false
	}
	if s.Context != b.Context {
		return false
	}
	if s.Startup != b.Startup {
		return false
	}
	if s.Description != b.Description {
		return false
	}
	if s.Instances != b.Instances {
		return false
	}
	if s.ImageID != b.ImageID {
		return false
	}
	if s.PoolID != b.PoolID {
		return false
	}
	if s.DesiredState != b.DesiredState {
		return false
	}
	if s.Launch != b.Launch {
		return false
	}
	if s.Hostname != b.Hostname {
		return false
	}
	if s.Privileged != b.Privileged {
		return false
	}
	if s.HostPolicy != b.HostPolicy {
		return false
	}
	if s.ParentServiceID != b.ParentServiceID {
		return false
	}
	if s.CreatedAt.Unix() != b.CreatedAt.Unix() {
		return false
	}
	if s.UpdatedAt.Unix() != b.CreatedAt.Unix() {
		return false
	}
	if !s.MonitoringProfile.Equals(&b.MonitoringProfile) {
		return false
	}
	return true
}
