// Copyright 2014, The Serviced Authors. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

// Package domain contains metric data objects for all domain objects

package domain

import (
	"github.com/zenoss/glog"

	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
)

// Metric defines the meta-data for a single metric
type Metric struct {
	ID          string //id is a unique idenitifier for the metric
	Name        string //name is a canonical name for the metric
	Description string //description of this metric
	Counter     bool
	CounterMax  *int64 `json:"CounterMax,omitempty"`
	ResetValue  int64
	Unit        string
}

// MetricBuilder contains data to build the MetricConfig.Metrics and QueryConfig.Data
type MetricBuilder struct {
	Metric
	Tags map[string][]string //tags required for querying a metric
}

// SetTag puts a tag into the metric request object
func (request *MetricBuilder) SetTag(Name string, Values ...string) *MetricBuilder {
	request.Tags[Name] = Values
	return request
}

// SetTags sets tags to value
func (request *MetricBuilder) SetTags(tags map[string][]string) *MetricBuilder {
	request.Tags = tags
	return request
}

// QueryConfig defines the parameters to request a collection of metrics
type QueryConfig struct {
	RequestURI string      // the http request uri for grabbing metrics
	Method     string      // the http method to retrieve metrics
	Headers    http.Header // http headers required to make request
	Data       string      // the http request body to request metrics
}

// Equals compares two QueryConfig objects for equality
func (config *QueryConfig) Equals(that *QueryConfig) bool {
	return reflect.DeepEqual(config, that)
}

// MetricConfig defines a collection of metrics and the query to request said metrics
type MetricConfig struct {
	ID          string      // a unique identifier for the metrics
	Name        string      // a canonical name for the metrics
	Description string      // a description of the metrics
	Query       QueryConfig // the http query to request metrics
	Metrics     []Metric    // meta-data describing all metrics
}

// Equals equality test for MetricConfig
func (config *MetricConfig) Equals(that *MetricConfig) bool {
	return reflect.DeepEqual(config, that)
}

// Builder aggregates a url, method, and metrics for building a MetricConfig
type Builder struct {
	url     *url.URL        //url to request a metrics
	method  string          //method to retrieve metrics
	metrics []MetricBuilder //metrics available in url
}

// Metric appends a metric configuration to the Builder
func (builder *Builder) Metric(metric Metric) *MetricBuilder {
	newMetric := MetricBuilder{
		Metric{
			ID:          metric.ID,
			Name:        metric.Name,
			Description: metric.Description,
			Counter:     metric.Counter,
			CounterMax:  metric.CounterMax,
			ResetValue:  metric.ResetValue,
			Unit:        metric.Unit,
		},
		make(map[string][]string),
	}
	builder.metrics = append(builder.metrics, newMetric)
	return &builder.metrics[len(builder.metrics)-1]
}

// Config builds a MetricConfig using all defined MetricRequests and resets the metrics requets
func (builder *Builder) Config(ID, Name, Description, Start string) (*MetricConfig, error) {
	//config object to build
	headers := make(http.Header)
	headers["Content-Type"] = []string{"application/json"}
	config := &MetricConfig{
		ID:          ID,
		Name:        Name,
		Description: Description,
		Query: QueryConfig{
			RequestURI: builder.url.RequestURI(),
			Method:     builder.method,
			Headers:    headers,
		},
		Metrics: make([]Metric, len(builder.metrics)),
	}

	//define a metric type to build json
	type metric struct {
		Metric string              `json:"metric"`
		Tags   map[string][]string `json:"tags"`
	}

	//aggregate request object
	type metrics struct {
		Metrics []metric `json:"metrics"`
		Start   string   `json:"start"`
	}

	// build an array of metric requests to central query and setup config metrics
	request := metrics{
		Metrics: make([]metric, len(builder.metrics)),
		Start:   Start,
	}

	//build the metrics
	for i := range builder.metrics {
		id := &builder.metrics[i].ID
		tags := &builder.metrics[i].Tags
		request.Metrics[i] = metric{*id, *tags}
		config.Metrics[i] = builder.metrics[i].Metric
	}

	//build the query body object
	bodyBytes, err := json.Marshal(request)
	if err != nil {
		glog.Errorf("Failed to marshal query body: %+v", err)
		return nil, err
	}

	builder.metrics = make([]MetricBuilder, 0)
	config.Query.Data = string(bodyBytes)
	return config, nil
}

// NewMetricConfigBuilder creates a factory to create MetricConfig instances.
func NewMetricConfigBuilder(RequestURI, Method string) (*Builder, error) {
	//strip leading '/' it's added back below
	requestURI := RequestURI
	if len(RequestURI) > 0 && RequestURI[0] == '/' {
		requestURI = RequestURI[1:]
	}

	//use url.Parse to ensure proper RequestURI. 'http://localhost' is removed when Config is built
	url, err := url.Parse("http://localhost/" + requestURI)
	if err != nil {
		glog.Errorf("Invalid Url: RequestURI=%s, method=%s, err=%+v", RequestURI, Method, err)
		return nil, err
	}

	method := strings.ToUpper(Method)
	switch method {
	case "GET":
	case "PUT":
	case "POST":
	default:
		glog.Errorf("Invalid http method: RequestURI=%s, method=%s", RequestURI, Method)
		return nil, errors.New("invalid method")
	}

	return &Builder{
		url:     url,
		method:  Method,
		metrics: make([]MetricBuilder, 0),
	}, nil
}
