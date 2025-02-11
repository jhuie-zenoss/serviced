// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package docker

import (
	"fmt"
	"testing"
	"time"

	"github.com/control-center/serviced/coordinator/client"
)

type ActionResult struct {
	Duration time.Duration
	Result   []byte
	Err      error
}

func (result *ActionResult) do() ([]byte, error) {
	<-time.After(result.Duration)
	return result.Result, result.Err
}

type TestActionHandler struct {
	ResultMap map[string]ActionResult
}

func (handler *TestActionHandler) AttachAndRun(dockerID string, command []string) ([]byte, error) {
	if result, ok := handler.ResultMap[dockerID]; ok {
		return result.do()
	}

	return nil, fmt.Errorf("action not found")
}

func TestActionListener_Listen(t *testing.T) {
	conn := client.NewTestConnection()
	defer conn.Close()
	handler := &TestActionHandler{
		ResultMap: map[string]ActionResult{
			"success": ActionResult{2 * time.Second, []byte("success"), nil},
			"failure": ActionResult{time.Second, []byte("message failure"), fmt.Errorf("failure")},
		},
	}

	t.Log("Start actions and shutdown")
	shutdown := make(chan interface{})
	done := make(chan interface{})

	listener := NewActionListener(conn, handler, "test-host-1")
	go func() {
		listener.Listen(shutdown)
		close(done)
	}()

	// send actions
	success, err := SendAction(conn, &Action{
		HostID:   listener.hostID,
		DockerID: "success",
		Command:  []string{"do", "some", "command"},
	})
	if err != nil {
		t.Fatal("Could not send success action")
	}
	successW, err := conn.GetW(actionPath(listener.hostID, success), &Action{})
	if err != nil {
		t.Fatal("Failed creating watch for success action: ", err)
	}

	failure, err := SendAction(conn, &Action{
		HostID:   listener.hostID,
		DockerID: "failure",
		Command:  []string{"do", "some", "bad", "command"},
	})
	if err != nil {
		t.Fatalf("Could not send fail action")
	}
	failureW, err := conn.GetW(actionPath(listener.hostID, failure), &Action{})
	if err != nil {
		t.Fatal("Failed creating watch for failure action: ", err)
	}

	// wait for actions to complete and shutdown
	<-successW
	<-failureW
	close(shutdown)
	<-done

}