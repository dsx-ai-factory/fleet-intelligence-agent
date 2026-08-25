// SPDX-FileCopyrightText: Copyright (c) 2025, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	pkgfaultinjector "github.com/NVIDIA/fleet-intelligence-sdk/pkg/fault-injector"
	pkgkmsgwriter "github.com/NVIDIA/fleet-intelligence-sdk/pkg/kmsg/writer"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
)

// MockComponent is a mock implementation of components.Component
type MockComponent struct {
	name         string
	tags         []string
	supported    bool
	healthStates apiv1.HealthStates
	events       apiv1.Events
	eventsError  error
	lastSince    time.Time
}

func (m *MockComponent) Name() string {
	return m.name
}

func (m *MockComponent) Tags() []string {
	return m.tags
}

func (m *MockComponent) IsSupported() bool {
	return m.supported
}

func (m *MockComponent) LastHealthStates() apiv1.HealthStates {
	return m.healthStates
}

func (m *MockComponent) Events(ctx context.Context, since time.Time) (apiv1.Events, error) {
	m.lastSince = since
	if m.eventsError != nil {
		return nil, m.eventsError
	}
	return m.events, nil
}

func (m *MockComponent) Check() components.CheckResult {
	return nil
}

func (m *MockComponent) Start() error {
	return nil
}

func (m *MockComponent) Close() error {
	return nil
}

// MockRegistry is a mock implementation of components.Registry
type MockRegistry struct {
	components []components.Component
}

func (m *MockRegistry) All() []components.Component {
	return m.components
}

func (m *MockRegistry) MustRegister(initFunc components.InitFunc) {
	// Mock implementation
}

func (m *MockRegistry) Register(initFunc components.InitFunc) (components.Component, error) {
	// Mock implementation
	return nil, nil
}

func (m *MockRegistry) Get(name string) components.Component {
	for _, c := range m.components {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func (m *MockRegistry) Deregister(name string) components.Component {
	for i, c := range m.components {
		if c.Name() == name {
			m.components = append(m.components[:i], m.components[i+1:]...)
			return c
		}
	}
	return nil
}

// MockMetricsStore is a mock implementation of pkgmetrics.Store
type MockMetricsStore struct {
	metrics []pkgmetrics.Metric
	readErr error
	lastOp  pkgmetrics.Op
}

func (m *MockMetricsStore) Record(ctx context.Context, metrics ...pkgmetrics.Metric) error {
	return nil
}

func (m *MockMetricsStore) Read(ctx context.Context, opts ...pkgmetrics.OpOption) (pkgmetrics.Metrics, error) {
	m.lastOp = pkgmetrics.Op{}
	_ = m.lastOp.ApplyOpts(opts)
	if m.readErr != nil {
		return nil, m.readErr
	}
	return m.metrics, nil
}

func (m *MockMetricsStore) Purge(ctx context.Context, before time.Time) (int, error) {
	return 0, nil
}

type erroringFaultInjector struct {
	kmsgWriter pkgkmsgwriter.KmsgWriter
	err        error
}

func (e *erroringFaultInjector) KmsgWriter() pkgkmsgwriter.KmsgWriter {
	return e.kmsgWriter
}

func (e *erroringFaultInjector) InjectEvent(ctx context.Context, registry interface{}, eventToInject *pkgfaultinjector.EventToInject) error {
	return e.err
}

// TestHealthz tests the healthz handler.
func TestHealthz(t *testing.T) {
	gin.SetMode(gin.TestMode)

	s := &Server{}
	router := gin.New()
	router.GET("/healthz", s.healthz())

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, "ok", response["status"])
	assert.Equal(t, "v1", response["version"])
}

// TestGetHealthStates tests the getHealthStates handler.
func TestGetHealthStates(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockRegistry := &MockRegistry{
		components: []components.Component{
			&MockComponent{
				name: "component1",
				healthStates: apiv1.HealthStates{
					{
						Name:   "state1",
						Health: apiv1.HealthStateTypeHealthy,
						Reason: "all good",
					},
				},
			},
			&MockComponent{
				name: "component2",
				healthStates: apiv1.HealthStates{
					{
						Name:   "state2",
						Health: apiv1.HealthStateTypeUnhealthy,
						Reason: "error",
					},
				},
			},
		},
	}

	handler := newGlobalHandler(&config.Config{
		RetentionPeriod: metav1.Duration{Duration: 5 * time.Minute},
	}, mockRegistry, nil, nil)

	tests := []struct {
		name           string
		queryParams    string
		expectedStatus int
		checkResponse  func(t *testing.T, body []byte)
	}{
		{
			name:           "get_all_states",
			queryParams:    "",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var states map[string]interface{}
				err := json.Unmarshal(body, &states)
				require.NoError(t, err)
				assert.Len(t, states, 2)
				assert.Contains(t, states, "component1")
				assert.Contains(t, states, "component2")
			},
		},
		{
			name:           "get_specific_component",
			queryParams:    "?components=component1",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var states map[string]interface{}
				err := json.Unmarshal(body, &states)
				require.NoError(t, err)
				assert.Len(t, states, 1)
				assert.Contains(t, states, "component1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/states", handler.getHealthStates)

			req := httptest.NewRequest("GET", "/states"+tt.queryParams, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w.Body.Bytes())
			}
		})
	}
}

// TestGetEvents tests the getEvents handler.
func TestGetEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockRegistry := &MockRegistry{
		components: []components.Component{
			&MockComponent{
				name: "component1",
				events: apiv1.Events{
					{
						Name:      "event1",
						Type:      apiv1.EventTypeInfo,
						Message:   "test event",
						ExtraInfo: map[string]string{"xid": "31"},
					},
				},
			},
		},
	}

	handler := newGlobalHandler(&config.Config{
		RetentionPeriod: metav1.Duration{Duration: 5 * time.Minute},
	}, mockRegistry, nil, nil)

	tests := []struct {
		name           string
		queryParams    string
		expectedStatus int
		checkResponse  func(t *testing.T, body []byte)
	}{
		{
			name:           "get_all_events",
			queryParams:    "",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var events map[string]interface{}
				err := json.Unmarshal(body, &events)
				require.NoError(t, err)
				assert.Contains(t, events, "component1")
				componentEvents, ok := events["component1"].([]interface{})
				require.True(t, ok)
				require.Len(t, componentEvents, 1)
				event, ok := componentEvents[0].(map[string]interface{})
				require.True(t, ok)
				extraInfo, ok := event["extra_info"].(map[string]interface{})
				require.True(t, ok)
				assert.Equal(t, "31", extraInfo["xid"])
			},
		},
		{
			name:           "get_events_with_time",
			queryParams:    fmt.Sprintf("?since=%d", time.Now().Unix()-3600),
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var events map[string]interface{}
				err := json.Unmarshal(body, &events)
				require.NoError(t, err)
				assert.NotNil(t, events)
			},
		},
		{
			name:           "clamps_events_since_to_retention",
			queryParams:    fmt.Sprintf("?since=%d", time.Now().Add(-24*time.Hour).Unix()),
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var events map[string]interface{}
				err := json.Unmarshal(body, &events)
				require.NoError(t, err)
				assert.NotNil(t, events)
				component := mockRegistry.Get("component1").(*MockComponent)
				retention := handler.cfg.RetentionPeriod.Duration
				assert.WithinDuration(t, time.Now().Add(-retention), component.lastSince, 2*time.Second)
			},
		},
		{
			name:           "invalid_since_time",
			queryParams:    "?since=invalid",
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, body []byte) {
				var response map[string]string
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Contains(t, response["error"], "invalid 'since' parameter")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/events", handler.getEvents)

			req := httptest.NewRequest("GET", "/events"+tt.queryParams, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w.Body.Bytes())
			}
		})
	}
}

// TestGetInfo tests the getInfo handler.
func TestGetInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockRegistry := &MockRegistry{
		components: []components.Component{
			&MockComponent{
				name:      "component1",
				tags:      []string{"tag1", "tag2"},
				supported: true,
				healthStates: apiv1.HealthStates{
					{Name: "state1", Health: apiv1.HealthStateTypeHealthy},
				},
			},
		},
	}

	handler := newGlobalHandler(&config.Config{}, mockRegistry, nil, nil)

	router := gin.New()
	router.GET("/info", handler.getInfo)

	req := httptest.NewRequest("GET", "/info", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var info map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &info)
	require.NoError(t, err)

	assert.Contains(t, info, "component1")
	comp1Info := info["component1"].(map[string]interface{})
	assert.Equal(t, "component1", comp1Info["name"])
	assert.True(t, comp1Info["supported"].(bool))
}

// TestGetMetrics tests the getMetrics handler.
func TestGetMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockMetrics := []pkgmetrics.Metric{
		{
			UnixMilliseconds: time.Now().UnixMilli(),
			Component:        "test-component",
			Name:             "test_metric",
			Value:            100.0,
		},
	}

	tests := []struct {
		name           string
		metricsStore   *MockMetricsStore
		queryParams    string
		expectedStatus int
		checkResponse  func(t *testing.T, body []byte)
	}{
		{
			name: "get_metrics_success",
			metricsStore: &MockMetricsStore{
				metrics: mockMetrics,
			},
			queryParams:    "",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var metrics []pkgmetrics.Metric
				err := json.Unmarshal(body, &metrics)
				require.NoError(t, err)
				assert.Len(t, metrics, 1)
				assert.Equal(t, "test-component", metrics[0].Component)
			},
		},
		{
			name: "get_metrics_with_time",
			metricsStore: &MockMetricsStore{
				metrics: mockMetrics,
			},
			queryParams:    fmt.Sprintf("?startTime=%d", time.Now().Unix()-3600),
			expectedStatus: http.StatusOK,
			checkResponse:  nil,
		},
		{
			name: "clamps_metrics_start_time_to_retention",
			metricsStore: &MockMetricsStore{
				metrics: mockMetrics,
			},
			queryParams:    fmt.Sprintf("?startTime=%d", time.Now().Add(-24*time.Hour).Unix()),
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var metrics []pkgmetrics.Metric
				err := json.Unmarshal(body, &metrics)
				require.NoError(t, err)
				assert.Len(t, metrics, 1)
			},
		},
		{
			name: "get_metrics_with_components",
			metricsStore: &MockMetricsStore{
				metrics: mockMetrics,
			},
			queryParams:    "?components=test-component",
			expectedStatus: http.StatusOK,
			checkResponse:  nil,
		},
		{
			name: "invalid_start_time",
			metricsStore: &MockMetricsStore{
				metrics: mockMetrics,
			},
			queryParams:    "?startTime=invalid",
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, body []byte) {
				var response map[string]string
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Contains(t, response["error"], "invalid 'startTime' parameter")
			},
		},
		{
			name: "metrics_store_error",
			metricsStore: &MockMetricsStore{
				readErr: assert.AnError,
			},
			queryParams:    "",
			expectedStatus: http.StatusInternalServerError,
			checkResponse: func(t *testing.T, body []byte) {
				var response map[string]string
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Contains(t, response["error"], "failed to retrieve metrics")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newGlobalHandler(&config.Config{
				RetentionPeriod: metav1.Duration{Duration: 5 * time.Minute},
			}, &MockRegistry{}, tt.metricsStore, nil)

			router := gin.New()
			router.GET("/metrics", handler.getMetrics)

			req := httptest.NewRequest("GET", "/metrics"+tt.queryParams, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w.Body.Bytes())
			}
			if tt.name == "clamps_metrics_start_time_to_retention" {
				assert.WithinDuration(t, time.Now().Add(-handler.cfg.RetentionPeriod.Duration), tt.metricsStore.lastOp.Since, 2*time.Second)
			}
		})
	}
}

// TestMachineInfo tests the machineInfo handler.
func TestMachineInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		gpudInstance   *components.GPUdInstance
		expectedStatus int
		checkResponse  func(t *testing.T, body []byte)
	}{
		{
			name:           "nil_gpud_instance",
			gpudInstance:   nil,
			expectedStatus: http.StatusInternalServerError,
			checkResponse: func(t *testing.T, body []byte) {
				var response map[string]string
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Equal(t, "gpud instance not available", response["error"])
			},
		},
		{
			name: "valid_gpud_instance",
			gpudInstance: &components.GPUdInstance{
				MachineID: "test-machine-id",
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, body []byte) {
				var response map[string]interface{}
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Equal(t, "test-machine-id", response["machine_id"])
				assert.Equal(t, "fleetint", response["service"])
				assert.Equal(t, false, response["nvidia_available"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newGlobalHandler(&config.Config{}, &MockRegistry{}, nil, tt.gpudInstance)

			router := gin.New()
			router.GET("/machine-info", handler.machineInfo)

			req := httptest.NewRequest("GET", "/machine-info", nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w.Body.Bytes())
			}
		})
	}
}

// TestNewGlobalHandler tests the newGlobalHandler function.
func TestNewGlobalHandler(t *testing.T) {
	mockRegistry := &MockRegistry{
		components: []components.Component{
			&MockComponent{name: "comp2"},
			&MockComponent{name: "comp1"},
			&MockComponent{name: "comp3"},
		},
	}

	handler := newGlobalHandler(&config.Config{}, mockRegistry, nil, nil)

	require.NotNil(t, handler)
	assert.NotNil(t, handler.cfg)
	assert.NotNil(t, handler.componentsRegistry)
	assert.Len(t, handler.componentNames, 3)

	// Component names should be sorted
	assert.Equal(t, "comp1", handler.componentNames[0])
	assert.Equal(t, "comp2", handler.componentNames[1])
	assert.Equal(t, "comp3", handler.componentNames[2])
}

// TestGetReqComponents tests the getReqComponents method.
func TestGetReqComponents(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mockRegistry := &MockRegistry{
		components: []components.Component{
			&MockComponent{name: "comp1"},
			&MockComponent{name: "comp2"},
		},
	}

	handler := newGlobalHandler(&config.Config{}, mockRegistry, nil, nil)

	tests := []struct {
		name           string
		queryParams    string
		expectedResult []string
	}{
		{
			name:           "no_query_params",
			queryParams:    "",
			expectedResult: []string{"comp1", "comp2"},
		},
		{
			name:           "specific_component",
			queryParams:    "?components=comp1",
			expectedResult: []string{"comp1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			var result []string
			var err error
			router.GET("/test", func(c *gin.Context) {
				result, err = handler.getReqComponents(c)
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest("GET", "/test"+tt.queryParams, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			require.NoError(t, err)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

// TestInstallMiddlewares tests the installMiddlewares function.
func TestInstallMiddlewares(t *testing.T) {
	gin.SetMode(gin.TestMode)

	s := &Server{}
	router := gin.New()

	// Install middlewares
	s.installMiddlewares(router)

	// Add a test endpoint
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "ok"})
	})

	tests := []struct {
		name           string
		method         string
		checkHeaders   func(t *testing.T, w *httptest.ResponseRecorder)
		expectedStatus int
	}{
		{
			name:   "GET_request_without_CORS",
			method: "GET",
			checkHeaders: func(t *testing.T, w *httptest.ResponseRecorder) {
				assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
				assert.Empty(t, w.Header().Get("Access-Control-Allow-Methods"))
				assert.Empty(t, w.Header().Get("Access-Control-Allow-Headers"))
				assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
				assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
				assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "OPTIONS_request",
			method:         "OPTIONS",
			checkHeaders:   nil,
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/test", nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkHeaders != nil {
				tt.checkHeaders(t, w)
			}
		})
	}
}

// TestInjectFault tests the injectFault handler.
func TestInjectFault(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		faultInjector  pkgfaultinjector.Injector
		requestBody    interface{}
		remoteAddr     string // Set to test security check
		expectedStatus int
		checkResponse  func(t *testing.T, body []byte)
	}{
		{
			name:           "non_localhost_rejected",
			faultInjector:  pkgfaultinjector.NewInjector(pkgkmsgwriter.NewWriter("/dev/null")),
			requestBody:    map[string]interface{}{},
			remoteAddr:     "192.168.1.100:12345", // Non-localhost address
			expectedStatus: http.StatusForbidden,
			checkResponse: func(t *testing.T, body []byte) {
				var response map[string]interface{}
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Contains(t, response, "message")
				assert.Contains(t, response["message"], "access denied")
			},
		},
		{
			name:           "nil_fault_injector",
			faultInjector:  nil,
			requestBody:    map[string]interface{}{},
			remoteAddr:     "127.0.0.1:54321", // Localhost address
			expectedStatus: http.StatusNotFound,
			checkResponse: func(t *testing.T, body []byte) {
				var response map[string]interface{}
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Contains(t, response, "message")
				assert.Contains(t, response["message"], "fault injector not enabled")
			},
		},
		{
			name:           "invalid_json",
			faultInjector:  pkgfaultinjector.NewInjector(pkgkmsgwriter.NewWriter("/dev/null")),
			requestBody:    "invalid json",
			remoteAddr:     "127.0.0.1:54321",
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, body []byte) {
				var response map[string]interface{}
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Contains(t, response, "message")
				assert.Contains(t, response["message"], "failed to decode request body")
			},
		},
		{
			name:          "empty_request",
			faultInjector: pkgfaultinjector.NewInjector(pkgkmsgwriter.NewWriter("/dev/null")),
			requestBody:   &pkgfaultinjector.Request{
				// Empty request - should fail validation
			},
			remoteAddr:     "127.0.0.1:54321",
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, body []byte) {
				var response map[string]interface{}
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Contains(t, response, "message")
				assert.Contains(t, response["message"], "invalid request")
			},
		},
		{
			name:           "ipv6_localhost",
			faultInjector:  nil,
			requestBody:    map[string]interface{}{},
			remoteAddr:     "[::1]:54321", // IPv6 localhost
			expectedStatus: http.StatusNotFound,
			checkResponse: func(t *testing.T, body []byte) {
				var response map[string]interface{}
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Contains(t, response, "message")
				assert.Contains(t, response["message"], "fault injector not enabled")
			},
		},
		{
			name: "internal_event_injection_error_is_sanitized",
			faultInjector: &erroringFaultInjector{
				kmsgWriter: pkgkmsgwriter.NewWriter("/dev/null"),
				err:        fmt.Errorf("secret backend failure"),
			},
			requestBody: map[string]interface{}{
				"event": map[string]interface{}{
					"component": "component1",
					"name":      "test-event",
					"type":      "info",
					"message":   "test message",
				},
			},
			remoteAddr:     "127.0.0.1:54321",
			expectedStatus: http.StatusInternalServerError,
			checkResponse: func(t *testing.T, body []byte) {
				var response map[string]interface{}
				err := json.Unmarshal(body, &response)
				require.NoError(t, err)
				assert.Contains(t, response, "message")
				assert.Equal(t, "failed to inject event", response["message"])
				assert.NotContains(t, fmt.Sprint(response["message"]), "secret backend failure")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{
				faultInjector: tt.faultInjector,
			}

			router := gin.New()
			router.POST("/inject-fault", s.injectFault)

			var body []byte
			var err error
			if str, ok := tt.requestBody.(string); ok {
				body = []byte(str)
			} else {
				body, err = json.Marshal(tt.requestBody)
				require.NoError(t, err)
			}

			req := httptest.NewRequest("POST", "/inject-fault", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			// Set RemoteAddr for security check testing
			if tt.remoteAddr != "" {
				req.RemoteAddr = tt.remoteAddr
			}

			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.checkResponse != nil {
				tt.checkResponse(t, w.Body.Bytes())
			}
		})
	}
}

// Benchmark tests

// BenchmarkHealthz benchmarks the healthz handler.
func BenchmarkHealthz(b *testing.B) {
	gin.SetMode(gin.TestMode)

	s := &Server{}
	router := gin.New()
	router.GET("/healthz", s.healthz())

	req := httptest.NewRequest("GET", "/healthz", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
	}
}

// BenchmarkGetHealthStates benchmarks the getHealthStates handler.
func BenchmarkGetHealthStates(b *testing.B) {
	gin.SetMode(gin.TestMode)

	mockRegistry := &MockRegistry{
		components: []components.Component{
			&MockComponent{name: "component1", healthStates: apiv1.HealthStates{{Name: "state1", Health: apiv1.HealthStateTypeHealthy}}},
			&MockComponent{name: "component2", healthStates: apiv1.HealthStates{{Name: "state2", Health: apiv1.HealthStateTypeHealthy}}},
		},
	}

	handler := newGlobalHandler(&config.Config{}, mockRegistry, nil, nil)

	router := gin.New()
	router.GET("/states", handler.getHealthStates)

	req := httptest.NewRequest("GET", "/states", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
	}
}
