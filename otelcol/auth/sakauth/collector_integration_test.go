// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
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

package sakauth

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
)

const collectorIntegrationEnv = "FLEETINT_OTELCOL_INTEGRATION"

type backendRequest struct {
	authorization string
	actorID       string
	contentType   string
	metrics       pmetric.Metrics
	logs          plog.Logs
	err           error
}

func TestCollectorGatewayEndToEnd(t *testing.T) {
	if os.Getenv(collectorIntegrationEnv) != "1" {
		t.Skipf("set %s=1 after building otelcol/bin/fleetint-otelcol", collectorIntegrationEnv)
	}

	collectorBinary := filepath.Clean(filepath.Join("..", "..", "bin", "fleetint-otelcol"))
	info, err := os.Stat(collectorBinary)
	require.NoError(t, err, "build the collector before running this integration test")
	require.False(t, info.IsDir())

	jwt := testJWT(t, "integration-customer")
	enrollmentRequests := make(chan backendRequest, 1)
	metricRequests := make(chan backendRequest, 1)
	logRequests := make(chan backendRequest, 1)
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/enroll":
			enrollmentRequests <- backendRequest{
				authorization: r.Header.Get("Authorization"),
				contentType:   r.Header.Get("Content-Type"),
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(enrollResponse{JWTAssertion: jwt})
		case "/metrics":
			payload, readErr := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			request := pmetricotlp.NewExportRequest()
			if readErr == nil {
				readErr = request.UnmarshalProto(payload)
			}
			metricRequests <- backendRequest{
				authorization: r.Header.Get("Authorization"),
				actorID:       r.Header.Get("Nv-Actor-Id"),
				contentType:   r.Header.Get("Content-Type"),
				metrics:       request.Metrics(),
				err:           readErr,
			}
			if readErr != nil {
				http.Error(w, "invalid OTLP payload", http.StatusBadRequest)
				return
			}
			responsePayload, marshalErr := pmetricotlp.NewExportResponse().MarshalProto()
			if marshalErr != nil {
				http.Error(w, "failed to encode OTLP response", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/x-protobuf")
			_, _ = w.Write(responsePayload)
		case "/logs":
			payload, readErr := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			request := plogotlp.NewExportRequest()
			if readErr == nil {
				readErr = request.UnmarshalProto(payload)
			}
			logRequests <- backendRequest{
				authorization: r.Header.Get("Authorization"),
				actorID:       r.Header.Get("Nv-Actor-Id"),
				contentType:   r.Header.Get("Content-Type"),
				logs:          request.Logs(),
				err:           readErr,
			}
			if readErr != nil {
				http.Error(w, "invalid OTLP payload", http.StatusBadRequest)
				return
			}
			responsePayload, marshalErr := plogotlp.NewExportResponse().MarshalProto()
			if marshalErr != nil {
				http.Error(w, "failed to encode OTLP response", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/x-protobuf")
			_, _ = w.Write(responsePayload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()

	receiverPort := availableTCPPort(t)
	healthPort := availableTCPPort(t)

	// sakauth rejects insecure_skip_verify, so trust the test server's
	// certificate explicitly. This also makes the test exercise real
	// certificate verification rather than skipping it.
	caPath := filepath.Join(t.TempDir(), "backend-ca.pem")
	require.NoError(t, os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: backend.Certificate().Raw,
	}), 0o600))

	configPath := filepath.Join(t.TempDir(), "collector.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(fmt.Sprintf(`
extensions:
  sakauth:
    enroll_endpoint: %[1]s/enroll
    sak_token: integration-sak
    tls:
      ca_file: %[4]s
  health_check:
    endpoint: 127.0.0.1:%[2]d
receivers:
  otlp:
    protocols:
      http:
        endpoint: 127.0.0.1:%[3]d
processors:
  batch:
    timeout: 100ms
    send_batch_size: 1
exporters:
  otlp_http/backend:
    metrics_endpoint: %[1]s/metrics
    logs_endpoint: %[1]s/logs
    compression: none
    tls:
      ca_file: %[4]s
    auth:
      authenticator: sakauth
service:
  extensions: [sakauth, health_check]
  pipelines:
    metrics:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp_http/backend]
    logs:
      receivers: [otlp]
      processors: [batch]
      exporters: [otlp_http/backend]
`, backend.URL, healthPort, receiverPort, caPath)), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	logPath := filepath.Join(t.TempDir(), "collector.log")
	logFile, err := os.Create(logPath)
	require.NoError(t, err)
	cmd := exec.CommandContext(ctx, collectorBinary, "--config="+configPath)
	cmd.Env = os.Environ()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	require.NoError(t, cmd.Start())
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()
	var stopOnce sync.Once
	stopCollector := func() {
		stopOnce.Do(func() {
			cancel()
			select {
			case <-waitCh:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
				<-waitCh
			}
			_ = logFile.Close()
		})
	}
	t.Cleanup(stopCollector)

	healthURL := "http://127.0.0.1:" + strconv.Itoa(healthPort) + "/"
	if err := waitForCollector(ctx, healthURL); err != nil {
		stopCollector()
		logs, _ := os.ReadFile(logPath)
		t.Fatalf("collector failed to become healthy: %v\n%s", err, logs)
	}

	select {
	case request := <-enrollmentRequests:
		require.Equal(t, "Bearer integration-sak", request.authorization)
		require.Empty(t, request.contentType)
	case <-ctx.Done():
		t.Fatal("timed out waiting for gateway enrollment")
	}

	payload := marshalTestMetrics(t)
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"http://127.0.0.1:"+strconv.Itoa(receiverPort)+"/v1/metrics",
		bytes.NewReader(payload),
	)
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/x-protobuf")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)

	select {
	case backendRequest := <-metricRequests:
		require.NoError(t, backendRequest.err)
		require.Equal(t, "Bearer "+jwt, backendRequest.authorization)
		require.Empty(t, backendRequest.actorID, "gateway must not set Nv-Actor-Id; Envoy injects it from the verified JWT")
		require.Equal(t, "application/x-protobuf", backendRequest.contentType)
		require.Equal(t, 1, backendRequest.metrics.MetricCount())
		resourceMetrics := backendRequest.metrics.ResourceMetrics()
		require.Equal(t, 1, resourceMetrics.Len())
		machineID, ok := resourceMetrics.At(0).Resource().Attributes().Get("machine.id")
		require.True(t, ok)
		require.Equal(t, "integration-node", machineID.Str())
	case <-ctx.Done():
		t.Fatal("timed out waiting for gateway OTLP export")
	}

	request, err = http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"http://127.0.0.1:"+strconv.Itoa(receiverPort)+"/v1/logs",
		bytes.NewReader(marshalTestLogs(t)),
	)
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/x-protobuf")
	response, err = http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)

	select {
	case backendRequest := <-logRequests:
		require.NoError(t, backendRequest.err)
		require.Equal(t, "Bearer "+jwt, backendRequest.authorization)
		require.Empty(t, backendRequest.actorID, "gateway must not set Nv-Actor-Id; Envoy injects it from the verified JWT")
		require.Equal(t, "application/x-protobuf", backendRequest.contentType)
		require.Equal(t, 1, backendRequest.logs.LogRecordCount())
		resourceLogs := backendRequest.logs.ResourceLogs()
		require.Equal(t, 1, resourceLogs.Len())
		machineID, ok := resourceLogs.At(0).Resource().Attributes().Get("machine.id")
		require.True(t, ok)
		require.Equal(t, "integration-node", machineID.Str())
	case <-ctx.Done():
		t.Fatal("timed out waiting for gateway OTLP log export")
	}
}

func availableTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func waitForCollector(ctx context.Context, healthURL string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func marshalTestMetrics(t *testing.T) []byte {
	t.Helper()
	metrics := pmetric.NewMetrics()
	resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
	resourceMetrics.Resource().Attributes().PutStr("machine.id", "integration-node")
	metric := resourceMetrics.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	metric.SetName("fleetint.gateway.integration")
	dataPoint := metric.SetEmptyGauge().DataPoints().AppendEmpty()
	dataPoint.SetIntValue(1)
	dataPoint.SetTimestamp(pcommon.Timestamp(time.Now().UnixNano()))

	payload, err := pmetricotlp.NewExportRequestFromMetrics(metrics).MarshalProto()
	require.NoError(t, err)
	return payload
}

func marshalTestLogs(t *testing.T) []byte {
	t.Helper()
	logs := plog.NewLogs()
	resourceLogs := logs.ResourceLogs().AppendEmpty()
	resourceLogs.Resource().Attributes().PutStr("machine.id", "integration-node")
	logRecord := resourceLogs.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	logRecord.SetTimestamp(pcommon.Timestamp(time.Now().UnixNano()))
	logRecord.Body().SetStr("gateway integration test")

	payload, err := plogotlp.NewExportRequestFromLogs(logs).MarshalProto()
	require.NoError(t, err)
	return payload
}
