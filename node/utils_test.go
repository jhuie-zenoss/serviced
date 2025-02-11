// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

// Package agent implements a service that runs on a serviced node. It is
// responsible for ensuring that a particular node is running the correct services
// and reporting the state and health of those services back to the master
// serviced.

package node

import (
	"testing"
)

// Test validOwnerSpec
func TestValidOwnerSpec(t *testing.T) {

	invalidSpecs := []string{
		"",
		":",
		".test:test",
		"test:.test",
		"test,test",
	}
	for _, spec := range invalidSpecs {
		if validOwnerSpec(spec) {
			t.Logf("%s should NOT be a valid owner spec")
			t.Fail()
		}
	}
	validSpecs := []string{
		"mysql:mysql",
		"root:root",
		"user.name:group.name",
		"user-name:group-name",
	}
	for _, spec := range validSpecs {
		if !validOwnerSpec(spec) {
			t.Logf("%s should be a valid owner spec")
			t.Fail()
		}
	}
}

// Test parsing docker version
func Test_parseDockerVersion(t *testing.T) {

	const exampleOutput = `Client version: 0.6.6
Go version (client): go1.2rc3
Git commit (client): 6d42040
Server version: 0.6.6
Git commit (server): 6d42040
Go version (server): go1.2rc3
Last stable version: 0.6.6
`
	const exampleOutput2 = `Client version: 0.6.6
Go version (client): go1.2rc3
Git commit (client): 6d42040
Server version: 0.6.6-dev
Git commit (server): b65e710
Go version (server): go1.2rc3
Last stable version: 0.6.6
`
	exampleVersion := DockerVersion{
		Client: []int{0, 6, 6},
		Server: []int{0, 6, 6},
	}

	version, err := parseDockerVersion(exampleOutput)
	if err != nil {
		t.Fatalf("Problem parsing example docker version: %s", err)
	}
	if !version.equals(&exampleVersion) {
		t.Fatalf("unexpected version: %v vs %v", version, exampleVersion)
	}

	version, err = parseDockerVersion(exampleOutput2)
	if err != nil {
		t.Fatalf("Problem parsing example2 docker version: %s", err)
	}
	if !version.equals(&exampleVersion) {
		t.Fatalf("unexpected version: %v vs %v", version, exampleVersion)
	}
}
