// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

type MinMax struct {
	Min int
	Max int
}

type HostIPAndPort struct {
	HostIP   string
	HostPort string
}

//Validate ensure that the values in min max are valid. Max >= Min >=0  returns error otherwise
func (minmax *MinMax) Validate() error {
	// Instances["min"] and Instances["max"] must be positive
	if minmax.Min < 0 || minmax.Max < 0 {
		return fmt.Errorf("Instances constraints must be positive: Min=%v; Max=%v", minmax.Min, minmax.Max)
	}

	// If "min" and "max" are both declared Instances["min"] < Instances["max"]
	if minmax.Max != 0 && minmax.Min > minmax.Max {
		return fmt.Errorf("Minimum instances larger than maximum instances: Min=%v; Max=%v", minmax.Min, minmax.Max)
	}
	return nil
}

// HealthCheck is a health check object
type HealthCheck struct {
	Script   string        // A script to execute to verify the health of a service.
	Interval time.Duration // The interval at which to execute the script.
}

type jsonHealthCheck struct {
	Script   string
	Interval float64 // the serialzed version will be in seconds
}

func (hc HealthCheck) MarshalJSON() ([]byte, error) {
	// in json, the interval is represented in seconds
	interval := float64(hc.Interval) / 1000000000.0
	return json.Marshal(jsonHealthCheck{
		Script:   hc.Script,
		Interval: interval,
	})
}

func (hc *HealthCheck) UnmarshalJSON(data []byte) error {
	var tempHc jsonHealthCheck
	if err := json.Unmarshal(data, &tempHc); err != nil {
		return err
	}
	hc.Script = tempHc.Script
	// interval in js is in seconds, convert to nanoseconds, then duration
	hc.Interval = time.Duration(tempHc.Interval * 1000000000.0)
	return nil
}

type HealthCheckResult struct {
	ServiceID string
	Name      string
	Timestamp string
	Passed    string
}

type Prereq struct {
	Name   string
	Script string
}

func (h *HealthCheckResult) ValidEntity() error {
	return nil
}
