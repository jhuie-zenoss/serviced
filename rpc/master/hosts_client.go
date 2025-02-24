// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package master

import (
	"github.com/control-center/serviced/domain/host"
)

//GetHost gets the host for the given hostID or nil
func (c *Client) GetHost(hostID string) (*host.Host, error) {
	response := host.New()
	if err := c.call("GetHost", hostID, response); err != nil {
		return nil, err
	}
	return response, nil
}

//GetHosts returns all hosts or empty array
func (c *Client) GetHosts() ([]*host.Host, error) {
	response := make([]*host.Host, 0)
	if err := c.call("GetHosts", empty, &response); err != nil {
		return []*host.Host{}, err
	}
	return response, nil
}

//AddHost adds a Host
func (c *Client) AddHost(host host.Host) error {
	return c.call("AddHost", host, nil)
}

//UpdateHost updates a host
func (c *Client) UpdateHost(host host.Host) error {
	return c.call("UpdateHost", host, nil)
}

//RemoveHost removes a host
func (c *Client) RemoveHost(hostID string) error {
	return c.call("RemoveHost", hostID, nil)
}

//FindHostsInPool returns all hosts in a pool
func (c *Client) FindHostsInPool(poolID string) ([]*host.Host, error) {
	response := make([]*host.Host, 0)
	if err := c.call("FindHostsInPool", poolID, &response); err != nil {
		return []*host.Host{}, err
	}
	return response, nil
}
