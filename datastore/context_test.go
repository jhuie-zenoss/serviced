// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package datastore

import (
	"testing"
)

type testDriver struct{}

func (d *testDriver) GetConnection() (Connection, error) {
	return &testConn{}, nil
}

type testConn struct{}

func (c testConn) Put(key Key, data JSONMessage) error {
	return nil
}

func (c testConn) Get(key Key) (JSONMessage, error) {
	return nil, nil
}

func (c testConn) Delete(key Key) error {
	return nil
}

func (c testConn) Query(interface{}) ([]JSONMessage, error) {
	return nil, nil
}

func TestContext(t *testing.T) {

	driver := testDriver{}
	ctx := newCtx(&driver)

	conn, _ := ctx.Connection()
	if conn == nil {
		t.Error("Expected connection, got nil")
	}
}
