// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

// Package proxy is used to create and register proxies that forward traffic from a port/ip combination, address, to a set
// of backends
package proxy

import (
	"github.com/dotcloud/docker/pkg/proxy"
	"github.com/control-center/serviced/commons"

	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
)

// ProxyAddress is a IP and port grouping
type ProxyAddress struct {
	IP   string
	Port uint16
}

// ProxyRegistry is an interface of a proxy registration service
type ProxyRegistry interface {
	//CreateProxy create, registers and starts a proxy identified by key
	//protocol is TCP or UDP
	//frontEnd is the IP/Port to listen on
	//backends are the what is being proxied, It is up to the proxy implementation on how it distributes requests to the backends
	CreateProxy(key string, protocol string, frontend ProxyAddress, backEnds ...ProxyAddress) error

	//RemoveProxy stops and removes proxy.
	RemoveProxy(key string) (Proxy, error)
}

// Proxy is the interface of a proxy.
type Proxy interface {
	Run() error
	Close() error
}

// ProxyFactory is a function declaration for a proxy factory.
type ProxyFactory func(protocol string, frontend ProxyAddress, backEnds ...ProxyAddress) (Proxy, error)

// NewProxyRegistry Create a new ProxyRegistry using the supplied ProxyFactory
func NewProxyRegistry(factory ProxyFactory) ProxyRegistry {
	return &proxyRegistry{
		registry:     make(map[string]Proxy),
		proxyFactory: factory,
	}
}

// NewDefaultProxyRegistry Create a new ProxyRegistry
func NewDefaultProxyRegistry() ProxyRegistry {
	return NewProxyRegistry(proxyFactory)
}

// ------ implementations below -------

type proxyRegistry struct {
	sync.Mutex
	registry     map[string]Proxy //Map of identifer to Proxy
	proxyFactory ProxyFactory
}

//make sure proxyRegistry implements interface
var _ ProxyRegistry = &proxyRegistry{}

func (pr *proxyRegistry) CreateProxy(key string, protocol string, frontend ProxyAddress, backEnds ...ProxyAddress) error {
	pr.Lock()
	defer pr.Unlock()
	if _, found := pr.registry[key]; found {
		return fmt.Errorf("proxy already registered for %v", key)
	}
	proxy, err := pr.proxyFactory(protocol, frontend, backEnds...)
	if err != nil {
		return err
	}
	err = proxy.Run()
	if err != nil {
		return err
	}
	pr.registry[key] = proxy
	return nil
}

func (pr *proxyRegistry) RemoveProxy(key string) (Proxy, error) {
	pr.Lock()
	defer pr.Unlock()
	proxy, found := pr.registry[key]
	if found {
		delete(pr.registry, key)
		return proxy, proxy.Close()
	}
	return nil, nil
}

//proxyFactory creates docker proxy implementations
func proxyFactory(protocol string, frontend ProxyAddress, backends ...ProxyAddress) (Proxy, error) {

	if len(backends) == 0 {
		return nil, errors.New("default proxy only requies one backend")
	}
	if len(backends) > 1 {
		return nil, errors.New("default proxy only supports one backend")
	}

	backendIP := net.ParseIP(backends[0].IP)
	if backendIP == nil {
		return nil, fmt.Errorf("not a valid IP format: %v", backendIP)
	}

	frontendIP := net.ParseIP(frontend.IP)
	if frontendIP == nil {
		return nil, fmt.Errorf("not a valid IP format: %v", frontendIP)
	}

	var frontendAddr, backendAddr net.Addr
	switch strings.Trim(strings.ToLower(protocol), " ") {
	case commons.TCP:
		frontendAddr = &net.TCPAddr{IP: frontendIP, Port: int(frontend.Port)}
		backendAddr = &net.TCPAddr{IP: backendIP, Port: int(backends[0].Port)}

	case commons.UDP:
		frontendAddr = &net.UDPAddr{IP: frontendIP, Port: int(frontend.Port)}
		backendAddr = &net.UDPAddr{IP: backendIP, Port: int(backends[0].Port)}

	default:
		return nil, fmt.Errorf("unsupported protocol %v", protocol)

	}

	proxy, err := proxy.NewProxy(frontendAddr, backendAddr)
	if err != nil {
		return nil, err
	}
	return &proxyWrapper{proxy}, nil
}

type proxyWrapper struct {
	proxy proxy.Proxy
}

func (pw *proxyWrapper) Run() error {
	go pw.proxy.Run()
	return nil
}
func (pw *proxyWrapper) Close() error {
	pw.proxy.Close()
	return nil
}
