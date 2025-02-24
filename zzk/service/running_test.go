// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/control-center/serviced/domain"
	"github.com/control-center/serviced/domain/service"
	"github.com/control-center/serviced/domain/servicedefinition"
	"github.com/control-center/serviced/domain/servicestate"
)

func TestNewRunningService(t *testing.T) {
	sd := servicedefinition.ServiceDefinition{
		MonitoringProfile: domain.MonitorProfile{
			MetricConfigs: []domain.MetricConfig{
				domain.MetricConfig{
					ID:          "jvm.memory",
					Name:        "JVM Memory",
					Description: "JVM heap vs. non-heap memory usage",
					Metrics: []domain.Metric{
						domain.Metric{ID: "jvm.memory.heap", Name: "JVM Heap Usage"},
						domain.Metric{ID: "jvm.memory.non_heap", Name: "JVM Non-Heap Usage"},
					},
				},
			},
		},
	}
	svc, err := service.BuildService(sd, "", "", 0, "")
	if err != nil {
		t.Errorf("BuildService Failed w/err=%s", err)
	}
	dataHeapRequest := fmt.Sprintf("{\"metric\":\"jvm.memory.heap\",\"tags\":{\"controlplane_service_id\":[\"%s\"]}}", svc.ID)
	dataNonHeapRequest := fmt.Sprintf("{\"metric\":\"jvm.memory.non_heap\",\"tags\":{\"controlplane_service_id\":[\"%s\"]}}", svc.ID)
	data := fmt.Sprintf("{\"metrics\":[%s,%s],\"start\":\"1h-ago\"}", dataHeapRequest, dataNonHeapRequest)
	svc.MonitoringProfile = domain.MonitorProfile{
		MetricConfigs: []domain.MetricConfig{
			domain.MetricConfig{
				ID:          "jvm.memory",
				Name:        "JVM Memory",
				Description: "JVM heap vs. non-heap memory usage",
				Query: domain.QueryConfig{
					RequestURI: "/metrics/api/performance/query",
					Method:     "POST",
					Headers: map[string][]string{
						"Content-Type": []string{"application/json"},
					},
					Data: data,
				},
				Metrics: []domain.Metric{
					domain.Metric{ID: "jvm.memory.heap", Name: "JVM Heap Usage"},
				},
			},
		},
	}

	svcstate, err := servicestate.BuildFromService(svc, "fakehostid")
	if err != nil {
		t.Error("%v", err)
	}

	rs, err := NewRunningService(svc, svcstate)
	if err != nil {
		t.Error("%v", err)
	}

	var query interface{}
	json.Unmarshal([]byte(rs.MonitoringProfile.MetricConfigs[0].Query.Data), &query)

	metrics := query.(map[string]interface{})["metrics"].([]interface{})[0].(map[string]interface{})

	tags := metrics["tags"].(map[string]interface{})

	controlplaneInstanceID := tags["controlplane_instance_id"].([]interface{})[0]
	if controlplaneInstanceID != "0" {
		t.Errorf("Expected %+v, got %+v", "0", controlplaneInstanceID)
	}

	controlplaneServiceID := tags["controlplane_service_id"].([]interface{})[0]
	if controlplaneServiceID != svc.ID {
		t.Errorf("Expected %+v, got %+v", svc.ID, controlplaneServiceID)
	}
}
