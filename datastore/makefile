# Copyright 2014, The Serviced Authors. All rights reserved.
# Use of this source code is governed by the Apache 2.0
# license that can be found in the LICENSE file.

.PHONY: build test clean

default: build

build:
	cd elastic && go build
	go build

test: build
	cd elastic && go test $(GOTEST_FLAGS)
	cd integration_test && go test $(GOTEST_FLAGS)
	go test $(GOTEST_FLAGS)

clean:
	cd elastic && go clean
	go clean
