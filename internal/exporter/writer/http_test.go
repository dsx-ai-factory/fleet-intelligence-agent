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

package writer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/backendclient"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter/collector"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter/converter"
)

func TestNewHTTPWriter(t *testing.T) {
	httpClient := &http.Client{}
	otlpConverter := &mockOTLPConverter{}

	writer := NewHTTPWriter(httpClient, otlpConverter)

	assert.NotNil(t, writer)
}

func TestHTTPWriter_Send_Success(t *testing.T) {
	metricsRequests := 0
	logsRequests := 0

	metricsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metricsRequests++
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/x-protobuf", r.Header.Get("Content-Type"))
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		assert.Equal(t, "metrics", r.Header.Get("X-Data-Type"))
		w.WriteHeader(http.StatusOK)
	}))
	defer metricsServer.Close()

	logsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logsRequests++
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "logs", r.Header.Get("X-Data-Type"))
		w.WriteHeader(http.StatusOK)
	}))
	defer logsServer.Close()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine",
		CollectionID: "test-collection",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{{}},
				},
				Logs: &logsv1.LogsData{
					ResourceLogs: []*logsv1.ResourceLogs{{}},
				},
			}
		},
	}

	httpClient := &http.Client{}
	writer := NewHTTPWriter(httpClient, otlpConverter)

	ctx := context.Background()
	newToken, err := writer.Send(ctx, testData, metricsServer.URL, logsServer.URL, 1, "test-token")

	require.NoError(t, err)
	assert.Empty(t, newToken)
	assert.Equal(t, 1, metricsRequests)
	assert.Equal(t, 1, logsRequests)
}

func TestHTTPWriter_Send_NilHealthData(t *testing.T) {
	writer := NewHTTPWriter(&http.Client{}, &mockOTLPConverter{})
	_, err := writer.Send(context.Background(), nil, "", "", 1, "test-token")
	require.ErrorContains(t, err, "nil HealthData")
}

func TestHTTPWriter_Send_EmptyMetrics(t *testing.T) {
	logsRequests := 0

	logsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logsRequests++
		w.WriteHeader(http.StatusOK)
	}))
	defer logsServer.Close()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine",
		CollectionID: "test-collection",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{},
				},
				Logs: &logsv1.LogsData{
					ResourceLogs: []*logsv1.ResourceLogs{{}},
				},
			}
		},
	}

	httpClient := &http.Client{}
	writer := NewHTTPWriter(httpClient, otlpConverter)

	ctx := context.Background()
	_, err := writer.Send(ctx, testData, "", logsServer.URL, 1, "test-token")

	require.NoError(t, err)
	assert.Equal(t, 1, logsRequests)
}

func TestHTTPWriter_Send_EmptyLogs(t *testing.T) {
	metricsRequests := 0

	metricsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metricsRequests++
		w.WriteHeader(http.StatusOK)
	}))
	defer metricsServer.Close()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine",
		CollectionID: "test-collection",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{{}},
				},
				Logs: &logsv1.LogsData{
					ResourceLogs: []*logsv1.ResourceLogs{},
				},
			}
		},
	}

	httpClient := &http.Client{}
	writer := NewHTTPWriter(httpClient, otlpConverter)

	ctx := context.Background()
	_, err := writer.Send(ctx, testData, metricsServer.URL, "", 1, "test-token")

	require.NoError(t, err)
	assert.Equal(t, 1, metricsRequests)
}

func TestHTTPWriter_Send_ValidationDoesNotBlock(t *testing.T) {
	metricsRequests := 0
	metricsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metricsRequests++
		w.WriteHeader(http.StatusOK)
	}))
	defer metricsServer.Close()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine",
		CollectionID: "test-collection",
	}
	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{{
						Resource: &resourcev1.Resource{
							Attributes: []*commonv1.KeyValue{
								{
									Key: "service.name",
									Value: &commonv1.AnyValue{
										Value: &commonv1.AnyValue_StringValue{StringValue: "fleet-intelligence-agent"},
									},
								},
							},
						},
						ScopeMetrics: []*metricsv1.ScopeMetrics{{
							Metrics: []*metricsv1.Metric{{
								Name: "bad.metric",
								Data: &metricsv1.Metric_Gauge{
									Gauge: &metricsv1.Gauge{
										DataPoints: []*metricsv1.NumberDataPoint{{
											TimeUnixNano: 0,
											Value: &metricsv1.NumberDataPoint_AsDouble{
												AsDouble: math.NaN(),
											},
										}},
									},
								},
							}},
						}},
					}},
				},
				Logs: nil,
			}
		},
	}

	writer := NewHTTPWriter(&http.Client{}, otlpConverter)
	_, err := writer.Send(context.Background(), testData, metricsServer.URL, "", 1, "test-token")
	require.NoError(t, err)
	assert.Equal(t, 1, metricsRequests)
}

func TestHTTPWriter_Send_RetryOnFailure(t *testing.T) {
	attempts := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine",
		CollectionID: "test-collection",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{{}},
				},
				Logs: nil,
			}
		},
	}

	httpClient := &http.Client{}
	writer := NewHTTPWriter(httpClient, otlpConverter)

	ctx := context.Background()
	_, err := writer.Send(ctx, testData, server.URL, "", 3, "test-token")

	require.NoError(t, err)
	assert.Equal(t, 3, attempts)
}

func TestHTTPWriter_Send_RetryExhausted(t *testing.T) {
	attempts := 0
	maxAttempts := 2 // Reduce attempts to speed up test

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine",
		CollectionID: "test-collection",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: nil, // No metrics to avoid metrics failure continuing
				Logs: &logsv1.LogsData{
					ResourceLogs: []*logsv1.ResourceLogs{{}},
				},
			}
		},
	}

	httpClient := &http.Client{}
	writer := NewHTTPWriter(httpClient, otlpConverter)

	ctx := context.Background()
	_, err := writer.Send(ctx, testData, "", server.URL, maxAttempts, "test-token")

	require.Error(t, err)
	assert.Equal(t, maxAttempts, attempts)
	assert.Contains(t, err.Error(), "failed after")
}

func TestHTTPWriter_Send_UnauthorizedWithJWTRefresh(t *testing.T) {
	attempts := 0
	refreshCalled := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		authHeader := r.Header.Get("Authorization")

		switch authHeader {
		case "Bearer old-token":
			w.WriteHeader(http.StatusUnauthorized)
		case "Bearer new-token":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine",
		CollectionID: "test-collection",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{{}},
				},
				Logs: nil,
			}
		},
	}

	httpClient := &http.Client{}
	writer := NewHTTPWriter(httpClient, otlpConverter)
	writer.SetJWTRefreshFunc(func(ctx context.Context) (string, error) {
		refreshCalled = true
		return "new-token", nil
	})

	ctx := context.Background()
	_, err := writer.Send(ctx, testData, server.URL, "", 3, "old-token")

	require.NoError(t, err)
	assert.True(t, refreshCalled)
	assert.Equal(t, 2, attempts) // First attempt with old token, second with new token
}

func TestHTTPWriter_Send_UnauthorizedJWTRefreshFails(t *testing.T) {
	attempts := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine",
		CollectionID: "test-collection",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: nil, // No metrics
				Logs: &logsv1.LogsData{
					ResourceLogs: []*logsv1.ResourceLogs{{}},
				},
			}
		},
	}

	httpClient := &http.Client{}
	writer := NewHTTPWriter(httpClient, otlpConverter)
	writer.SetJWTRefreshFunc(func(ctx context.Context) (string, error) {
		return "", assert.AnError
	})

	ctx := context.Background()
	_, err := writer.Send(ctx, testData, "", server.URL, 2, "old-token") // Reduce retries

	require.Error(t, err)
	assert.GreaterOrEqual(t, attempts, 1)
}

func TestHTTPWriter_Send_JWTTokenRefreshFromHeader(t *testing.T) {
	newToken := "refreshed-jwt-token"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("jwt_assertion", newToken)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine",
		CollectionID: "test-collection",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{{}},
				},
				Logs: nil,
			}
		},
	}

	httpClient := &http.Client{}
	writer := NewHTTPWriter(httpClient, otlpConverter)

	ctx := context.Background()
	returnedToken, err := writer.Send(ctx, testData, server.URL, "", 1, "test-token")

	require.NoError(t, err)
	assert.Equal(t, newToken, returnedToken)
}

func TestHTTPWriter_sendOTLPRequest_DoesNotFollowRedirect(t *testing.T) {
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected redirect follow")
	}))
	defer redirectTarget.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	metricsData := &metricsv1.MetricsData{
		ResourceMetrics: []*metricsv1.ResourceMetrics{},
	}
	reqData, err := proto.Marshal(metricsData)
	require.NoError(t, err)

	httpClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	otlpConverter := &mockOTLPConverter{}
	writer := NewHTTPWriter(httpClient, otlpConverter).(*httpWriter)

	ctx := context.Background()
	returnedToken, err := writer.sendOTLPRequest(ctx, reqData, "metrics", "test-collection-id", "test-machine-id", server.URL, "test-token")

	require.Error(t, err)
	assert.Empty(t, returnedToken)
	assert.Contains(t, err.Error(), "HTTP 307")
}

func TestHTTPWriter_sendOTLPRequest_RejectsRedirectedHost(t *testing.T) {
	newToken := "poisoned-jwt-token"

	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("jwt_assertion", newToken)
		w.WriteHeader(http.StatusOK)
	}))
	defer redirectTarget.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	metricsData := &metricsv1.MetricsData{
		ResourceMetrics: []*metricsv1.ResourceMetrics{},
	}
	reqData, err := proto.Marshal(metricsData)
	require.NoError(t, err)

	httpClient := &http.Client{}
	otlpConverter := &mockOTLPConverter{}
	writer := NewHTTPWriter(httpClient, otlpConverter).(*httpWriter)

	ctx := context.Background()
	token, err := writer.sendOTLPRequest(ctx, reqData, "metrics", "test-collection-id", "test-machine-id", server.URL, "test-token")

	require.Error(t, err)
	assert.Empty(t, token)
	assert.Contains(t, err.Error(), "response origin mismatch")
}

func TestHTTPWriter_Send_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine",
		CollectionID: "test-collection",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: nil, // No metrics
				Logs: &logsv1.LogsData{
					ResourceLogs: []*logsv1.ResourceLogs{{}},
				},
			}
		},
	}

	httpClient := &http.Client{}
	writer := NewHTTPWriter(httpClient, otlpConverter)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := writer.Send(ctx, testData, "", server.URL, 1, "test-token")

	require.Error(t, err)
}

func TestHTTPWriter_Send_LogsFailure(t *testing.T) {
	metricsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer metricsServer.Close()

	logsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer logsServer.Close()

	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine",
		CollectionID: "test-collection",
	}

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{{}},
				},
				Logs: &logsv1.LogsData{
					ResourceLogs: []*logsv1.ResourceLogs{{}},
				},
			}
		},
	}

	httpClient := &http.Client{}
	writer := NewHTTPWriter(httpClient, otlpConverter)

	ctx := context.Background()
	_, err := writer.Send(ctx, testData, metricsServer.URL, logsServer.URL, 1, "test-token")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send critical logs data")
}

func TestHTTPWriter_Send_SignalFailures(t *testing.T) {
	tests := []struct {
		name            string
		metricsStatus   int
		logsStatus      int
		wantErrContains []string
	}{
		{
			name:            "metrics_fail_logs_still_sent",
			metricsStatus:   http.StatusInternalServerError,
			logsStatus:      http.StatusOK,
			wantErrContains: []string{"failed to send metrics data"},
		},
		{
			name:            "both_fail",
			metricsStatus:   http.StatusInternalServerError,
			logsStatus:      http.StatusInternalServerError,
			wantErrContains: []string{"failed to send metrics data", "failed to send critical logs data"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logsRequests := 0

			metricsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.metricsStatus)
			}))
			defer metricsServer.Close()

			logsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				logsRequests++
				if tt.logsStatus == http.StatusOK {
					w.Header().Set("jwt_assertion", "refreshed-token")
				}
				w.WriteHeader(tt.logsStatus)
			}))
			defer logsServer.Close()

			testData := &collector.HealthData{
				Timestamp:    time.Now(),
				MachineID:    "test-machine",
				CollectionID: "test-collection",
			}

			otlpConverter := &mockOTLPConverter{
				convertFunc: func(data *collector.HealthData) *converter.OTLPData {
					return &converter.OTLPData{
						Metrics: &metricsv1.MetricsData{
							ResourceMetrics: []*metricsv1.ResourceMetrics{{}},
						},
						Logs: &logsv1.LogsData{
							ResourceLogs: []*logsv1.ResourceLogs{{}},
						},
					}
				},
			}

			writer := NewHTTPWriter(&http.Client{}, otlpConverter)
			newToken, err := writer.Send(context.Background(), testData, metricsServer.URL, logsServer.URL, 1, "test-token")

			require.Error(t, err)
			for _, want := range tt.wantErrContains {
				assert.Contains(t, err.Error(), want)
			}
			assert.Equal(t, 1, logsRequests, "logs should still be attempted after a metrics failure")
			if tt.logsStatus == http.StatusOK {
				assert.Equal(t, "refreshed-token", newToken, "token from the successful signal should be returned with the error")
			} else {
				assert.Empty(t, newToken)
			}
		})
	}
}

func TestHTTPWriter_Send_NonRetryableStatus(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusInternalServerError,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				w.WriteHeader(status)
			}))
			defer server.Close()

			otlpConverter := &mockOTLPConverter{
				convertFunc: func(data *collector.HealthData) *converter.OTLPData {
					return &converter.OTLPData{
						Metrics: &metricsv1.MetricsData{
							ResourceMetrics: []*metricsv1.ResourceMetrics{{}},
						},
					}
				},
			}
			testData := &collector.HealthData{
				Timestamp:    time.Now(),
				MachineID:    "test-machine",
				CollectionID: "test-collection",
			}

			writer := NewHTTPWriter(&http.Client{}, otlpConverter)
			start := time.Now()
			_, err := writer.Send(context.Background(), testData, server.URL, "", 3, "test-token")

			require.Error(t, err)
			assert.Contains(t, err.Error(), "non-retryable error on attempt 1")
			assert.Equal(t, int32(1), attempts.Load(), "a non-retryable status must not be retried")
			assert.Less(t, time.Since(start), defaultRetryDelay, "a non-retryable status must not wait for a retry")
		})
	}
}

func TestHTTPWriter_Send_RetryableStatus(t *testing.T) {
	for _, status := range []int{
		http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if attempts.Add(1) == 1 {
					w.WriteHeader(status)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			otlpConverter := &mockOTLPConverter{
				convertFunc: func(data *collector.HealthData) *converter.OTLPData {
					return &converter.OTLPData{
						Metrics: &metricsv1.MetricsData{
							ResourceMetrics: []*metricsv1.ResourceMetrics{{}},
						},
					}
				},
			}
			testData := &collector.HealthData{
				Timestamp:    time.Now(),
				MachineID:    "test-machine",
				CollectionID: "test-collection",
			}

			writer := NewHTTPWriter(&http.Client{}, otlpConverter)
			_, err := writer.Send(context.Background(), testData, server.URL, "", 2, "test-token")

			require.NoError(t, err)
			assert.Equal(t, int32(2), attempts.Load(), "a retryable status must be retried")
		})
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "transport_error", err: assert.AnError, want: true},
		{name: "401", err: &HTTPError{StatusCode: http.StatusUnauthorized}, want: false},
		{name: "429", err: &HTTPError{StatusCode: http.StatusTooManyRequests}, want: true},
		{name: "502", err: &HTTPError{StatusCode: http.StatusBadGateway}, want: true},
		{name: "503", err: &HTTPError{StatusCode: http.StatusServiceUnavailable}, want: true},
		{name: "504", err: &HTTPError{StatusCode: http.StatusGatewayTimeout}, want: true},
		{name: "wrapped_503", err: fmt.Errorf("send: %w", &HTTPError{StatusCode: http.StatusServiceUnavailable}), want: true},
		{name: "307", err: &HTTPError{StatusCode: http.StatusTemporaryRedirect}, want: false},
		{name: "400", err: &HTTPError{StatusCode: http.StatusBadRequest}, want: false},
		{name: "403", err: &HTTPError{StatusCode: http.StatusForbidden}, want: false},
		{name: "404", err: &HTTPError{StatusCode: http.StatusNotFound}, want: false},
		{name: "500", err: &HTTPError{StatusCode: http.StatusInternalServerError}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isRetryableError(tt.err))
		})
	}
}

func TestHTTPWriter_Send_UnauthorizedAfterRefreshNotRetried(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{{}},
				},
			}
		},
	}
	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine",
		CollectionID: "test-collection",
	}

	writer := NewHTTPWriter(&http.Client{}, otlpConverter)
	writer.SetJWTRefreshFunc(func(ctx context.Context) (string, error) {
		return "new-token", nil
	})
	start := time.Now()
	_, err := writer.Send(context.Background(), testData, server.URL, "", 3, "old-token")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-retryable error on attempt 1")
	assert.Equal(t, int32(2), attempts.Load(), "only the request with the refreshed token may follow the first 401")
	assert.Less(t, time.Since(start), defaultRetryDelay, "a 401 after the refresh must not wait for a retry")
}

// refreshTest sends to a server that accepts only "new-token", with refresh as
// the JWT refresh, and counts the requests and refreshes.
func refreshTest(t *testing.T, maxRetries int, refresh func(call int32) (string, error)) (attempts, refreshes int32, err error) {
	t.Helper()
	var attemptCount, refreshCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount.Add(1)
		if r.Header.Get("Authorization") == "Bearer new-token" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{{}},
				},
			}
		},
	}
	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine",
		CollectionID: "test-collection",
	}

	writer := NewHTTPWriter(&http.Client{}, otlpConverter)
	writer.SetJWTRefreshFunc(func(ctx context.Context) (string, error) {
		return refresh(refreshCount.Add(1))
	})
	_, err = writer.Send(context.Background(), testData, server.URL, "", maxRetries, "old-token")
	return attemptCount.Load(), refreshCount.Load(), err
}

// The refresh errors as refreshJWTToken returns them for a backend that is
// temporarily unavailable or does not answer in time.
func temporaryRefreshErrors() map[string]error {
	return map[string]error{
		"503": fmt.Errorf("failed to refresh JWT token: %w", &backendclient.HTTPStatusError{StatusCode: http.StatusServiceUnavailable}),
		"timeout": fmt.Errorf("failed to refresh JWT token: %w",
			&url.Error{Op: "Post", URL: "https://backend.example/v1/agent/enroll", Err: context.DeadlineExceeded}),
	}
}

func TestHTTPWriter_Send_TemporaryRefreshFailureRetried(t *testing.T) {
	for name, refreshErr := range temporaryRefreshErrors() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			attempts, refreshes, err := refreshTest(t, 3, func(call int32) (string, error) {
				if call == 1 {
					return "", refreshErr
				}
				return "new-token", nil
			})

			require.NoError(t, err)
			assert.Equal(t, int32(2), refreshes, "the refresh must be tried again on the next attempt")
			assert.Equal(t, int32(3), attempts, "old token, old token after the failed refresh, then the new token")
		})
	}
}

func TestHTTPWriter_Send_TemporaryRefreshFailureExhaustsRetries(t *testing.T) {
	for name, refreshErr := range temporaryRefreshErrors() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			attempts, refreshes, err := refreshTest(t, 2, func(int32) (string, error) {
				return "", refreshErr
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), "failed after 2 attempts")
			assert.ErrorIs(t, err, refreshErr)
			assert.Equal(t, int32(2), attempts)
			assert.Equal(t, int32(2), refreshes)
		})
	}
}

func TestHTTPWriter_Send_PermanentRefreshFailureNotRetried(t *testing.T) {
	for name, refreshErr := range map[string]error{
		"rejected SAK token": fmt.Errorf("failed to refresh JWT token: %w", &backendclient.HTTPStatusError{StatusCode: http.StatusForbidden}),
		"no SAK token":       errors.New("no SAK token available for JWT refresh"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			start := time.Now()
			attempts, refreshes, err := refreshTest(t, 3, func(int32) (string, error) {
				return "", refreshErr
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), "non-retryable JWT refresh error on attempt 1")
			assert.ErrorIs(t, err, refreshErr)
			assert.Equal(t, int32(1), attempts)
			assert.Equal(t, int32(1), refreshes)
			assert.Less(t, time.Since(start), defaultRetryDelay, "a permanent refresh failure must not wait for a retry")
		})
	}
}

func TestHTTPWriter_Send_RetryAfter(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if attempts.Add(1) == 1 {
					w.Header().Set("Retry-After", "1")
					w.WriteHeader(status)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			otlpConverter := &mockOTLPConverter{
				convertFunc: func(data *collector.HealthData) *converter.OTLPData {
					return &converter.OTLPData{
						Metrics: &metricsv1.MetricsData{
							ResourceMetrics: []*metricsv1.ResourceMetrics{{}},
						},
					}
				},
			}
			testData := &collector.HealthData{
				Timestamp:    time.Now(),
				MachineID:    "test-machine",
				CollectionID: "test-collection",
			}

			writer := NewHTTPWriter(&http.Client{}, otlpConverter)
			start := time.Now()
			_, err := writer.Send(context.Background(), testData, server.URL, "", 2, "test-token")
			elapsed := time.Since(start)

			require.NoError(t, err)
			assert.Equal(t, int32(2), attempts.Load())
			assert.GreaterOrEqual(t, elapsed, time.Second, "the retry must wait for Retry-After")
			assert.Less(t, elapsed, defaultRetryDelay, "Retry-After must replace the default delay")
		})
	}
}

func TestHTTPWriter_sendOTLPRequest_RetryAfter(t *testing.T) {
	tests := []struct {
		name   string
		header string
		status int
		want   time.Duration
	}{
		{name: "429", status: http.StatusTooManyRequests, header: "7", want: 7 * time.Second},
		{name: "503", status: http.StatusServiceUnavailable, header: "7", want: 7 * time.Second},
		{name: "503_without_header", status: http.StatusServiceUnavailable, want: 0},
		{name: "502_ignores_header", status: http.StatusBadGateway, header: "7", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.header != "" {
					w.Header().Set("Retry-After", tt.header)
				}
				w.WriteHeader(tt.status)
			}))
			defer server.Close()

			writer := NewHTTPWriter(&http.Client{}, &mockOTLPConverter{}).(*httpWriter)
			_, err := writer.sendOTLPRequest(context.Background(), []byte{}, "metrics", "test-collection-id", "test-machine-id", server.URL, "test-token")

			var httpErr *HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, tt.want, httpErr.RetryAfter)
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	date := func(d time.Duration) string { return now.Add(d).Format(http.TimeFormat) }

	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "empty", value: "", want: 0},
		{name: "seconds", value: "2", want: 2 * time.Second},
		{name: "seconds_with_spaces", value: " 3 ", want: 3 * time.Second},
		{name: "zero", value: "0", want: 0},
		{name: "negative", value: "-1", want: 0},
		{name: "at_cap", value: "60", want: maxRetryAfter},
		{name: "above_cap", value: "3600", want: maxRetryAfter},
		{name: "date", value: date(10 * time.Second), want: 10 * time.Second},
		{name: "date_in_past", value: date(-10 * time.Second), want: 0},
		{name: "date_above_cap", value: date(2 * time.Hour), want: maxRetryAfter},
		{name: "invalid", value: "soon", want: 0},
		{name: "fractional", value: "1.5", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseRetryAfter(tt.value, now))
		})
	}
}

func TestRetryDelay(t *testing.T) {
	delay, jitter := retryDelay(&HTTPError{StatusCode: http.StatusServiceUnavailable, RetryAfter: 3 * time.Second})
	assert.Equal(t, 3*time.Second, delay)
	assert.Zero(t, jitter)

	for _, err := range []error{
		assert.AnError,
		&HTTPError{StatusCode: http.StatusServiceUnavailable},
	} {
		delay, jitter = retryDelay(err)
		assert.Equal(t, defaultRetryDelay+jitter, delay)
		assert.GreaterOrEqual(t, jitter, time.Duration(0))
		assert.Less(t, jitter, defaultRetryDelay/2)
	}
}

func TestHTTPError_Error(t *testing.T) {
	tests := []struct {
		name     string
		httpErr  *HTTPError
		expected string
	}{
		{
			name: "with_message",
			httpErr: &HTTPError{
				StatusCode: 500,
				Status:     "Internal Server Error",
				Message:    "Something went wrong",
			},
			expected: "HTTP 500 Internal Server Error: Something went wrong",
		},
		{
			name: "without_message",
			httpErr: &HTTPError{
				StatusCode: 404,
				Status:     "Not Found",
			},
			expected: "HTTP 404 Not Found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errMsg := tt.httpErr.Error()
			assert.Equal(t, tt.expected, errMsg)
		})
	}
}

func TestHTTPWriter_isUnauthorizedError(t *testing.T) {
	httpClient := &http.Client{}
	otlpConverter := &mockOTLPConverter{}
	writer := NewHTTPWriter(httpClient, otlpConverter).(*httpWriter)

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil_error",
			err:      nil,
			expected: false,
		},
		{
			name: "unauthorized_error",
			err: &HTTPError{
				StatusCode: http.StatusUnauthorized,
				Status:     "Unauthorized",
			},
			expected: true,
		},
		{
			name: "other_http_error",
			err: &HTTPError{
				StatusCode: http.StatusInternalServerError,
				Status:     "Internal Server Error",
			},
			expected: false,
		},
		{
			name:     "non_http_error",
			err:      assert.AnError,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := writer.isUnauthorizedError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHTTPWriter_Interface(t *testing.T) {
	// Verify that httpWriter implements HTTPWriter interface
	var _ HTTPWriter = (*httpWriter)(nil)

	httpClient := &http.Client{}
	otlpConverter := &mockOTLPConverter{}

	writer := NewHTTPWriter(httpClient, otlpConverter)

	// Verify both methods are available
	assert.NotNil(t, writer)

	// Verify SetJWTRefreshFunc is available
	writer.SetJWTRefreshFunc(func(ctx context.Context) (string, error) {
		return "", nil
	})
}

func TestHTTPWriter_MarshalError(t *testing.T) {
	testData := &collector.HealthData{
		Timestamp:    time.Now(),
		MachineID:    "test-machine",
		CollectionID: "test-collection",
	}

	// Create a mock converter that will cause marshal to succeed but return invalid protobuf
	otlpConverter := &mockOTLPConverter{
		convertFunc: func(data *collector.HealthData) *converter.OTLPData {
			// Return a mock message that might cause issues
			return &converter.OTLPData{
				Metrics: &metricsv1.MetricsData{
					ResourceMetrics: []*metricsv1.ResourceMetrics{{}},
				},
				Logs: nil,
			}
		},
	}

	httpClient := &http.Client{}
	writer := NewHTTPWriter(httpClient, otlpConverter)

	ctx := context.Background()
	// Pass empty endpoints - should not send anything
	newToken, err := writer.Send(ctx, testData, "", "", 1, "test-token")

	require.NoError(t, err)
	assert.Empty(t, newToken)
}

func TestHTTPWriter_sendOTLPRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		assert.Equal(t, "application/x-protobuf", r.Header.Get("Content-Type"))
		assert.Equal(t, "fleetint-exporter", r.Header.Get("User-Agent"))
		// X-Machine-ID might be empty in test environments - just check it exists as header
		assert.Contains(t, r.Header, "X-Machine-Id")
		assert.Equal(t, "metrics", r.Header.Get("X-Data-Type"))
		assert.Equal(t, "test-collection-id", r.Header.Get("X-Collection-ID"))
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create test OTLP data
	metricsData := &metricsv1.MetricsData{
		ResourceMetrics: []*metricsv1.ResourceMetrics{},
	}
	reqData, err := proto.Marshal(metricsData)
	require.NoError(t, err)

	httpClient := &http.Client{}
	otlpConverter := &mockOTLPConverter{}
	writer := NewHTTPWriter(httpClient, otlpConverter).(*httpWriter)

	ctx := context.Background()
	token, err := writer.sendOTLPRequest(ctx, reqData, "metrics", "test-collection-id", "test-machine-id", server.URL, "test-token")

	require.NoError(t, err)
	assert.Empty(t, token)
}
