// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package container

import (
	"github.com/zenoss/glog"
	rest "github.com/zenoss/go-json-rest"

	"fmt"
	"io"
	"net"
	"net/http"
)

// MetricForwarder contains all configuration parameters required to provide a
// forward metrics inside a docker container.
type MetricForwarder struct {
	port               string
	metricsRedirectURL string
	listener           *net.Listener
}

// NewMetricForwarder creates a new metric forwarder at port, all metrics are forwarded to metricsRedirectURL
func NewMetricForwarder(port, metricsRedirectURL string) (config *MetricForwarder, err error) {
	if len(port) < 4 {
		return nil, fmt.Errorf("invalid port specification: '%s'", port)
	}
	config = &MetricForwarder{
		port:               port,
		metricsRedirectURL: metricsRedirectURL,
	}
	listener, err := net.Listen("tcp", port)
	if err != nil {
		return nil, err
	}
	config.listener = &listener
	go config.loop()
	return config, err
}

// loop() configures all http method handlers for the container controller.
// Then starts the server.  This method blocks.
func (forwarder *MetricForwarder) loop() {
	routes := []rest.Route{
		rest.Route{
			HttpMethod: "POST",
			PathExp:    "/api/metrics/store",
			Func:       postAPIMetricsStore(forwarder.metricsRedirectURL),
		},
	}

	handler := rest.ResourceHandler{}
	handler.SetRoutes(routes...)
	http.Serve(*forwarder.listener, &handler)
}

// Close shuts down the forwarder.
func (forwarder *MetricForwarder) Close() error {
	if forwarder != nil && forwarder.listener != nil {
		(*forwarder.listener).Close()
		forwarder.listener = nil
	}
	return nil
}

// postAPIMetricsStore redirects the post request to the configured address
// Any additional parameters should be encoded in the redirect url.  For
// example, encode the containers tenant and service id.
func postAPIMetricsStore(redirectURL string) func(*rest.ResponseWriter, *rest.Request) {
	return func(w *rest.ResponseWriter, request *rest.Request) {
		client := &http.Client{}

		proxyRequest, _ := http.NewRequest(request.Method, redirectURL, request.Body)
		for k, v := range request.Header {
			proxyRequest.Header[k] = v
		}
		proxyResponse, err := client.Do(proxyRequest)
		if err == nil {
			defer proxyResponse.Body.Close()
			w.WriteHeader(proxyResponse.StatusCode)
			io.Copy(w, proxyResponse.Body)
		} else {
			glog.Errorf("Failed to proxy request: %s", err)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}
