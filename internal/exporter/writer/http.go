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
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	"google.golang.org/protobuf/proto"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter/collector"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/exporter/converter"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/validation/outbound"
)

const (
	// defaultRetryDelay is the default delay between retry attempts
	defaultRetryDelay = 5 * time.Second
)

// HTTPError represents an HTTP error with status code
type HTTPError struct {
	StatusCode int
	Status     string
	Message    string
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d %s: %s", e.StatusCode, e.Status, e.Message)
	}
	return fmt.Sprintf("HTTP %d %s", e.StatusCode, e.Status)
}

// JWTRefreshFunc is a function type for refreshing JWT tokens
type JWTRefreshFunc func(ctx context.Context) (string, error)

// HTTPWriter defines the interface for sending health data via HTTP
type HTTPWriter interface {
	Send(ctx context.Context, data *collector.HealthData, metricsEndpoint string, logsEndpoint string, maxRetries int, authToken string) (newToken string, err error)
	SetJWTRefreshFunc(refreshFunc JWTRefreshFunc)
}

// httpWriter implements the HTTPWriter interface
type httpWriter struct {
	httpClient     *http.Client
	otlpConverter  converter.OTLPConverter
	jwtRefreshFunc JWTRefreshFunc
}

// NewHTTPWriter creates a new HTTP writer
func NewHTTPWriter(httpClient *http.Client, otlpConverter converter.OTLPConverter) HTTPWriter {
	return &httpWriter{
		httpClient:    httpClient,
		otlpConverter: otlpConverter,
	}
}

// SetJWTRefreshFunc sets the JWT refresh function
func (w *httpWriter) SetJWTRefreshFunc(refreshFunc JWTRefreshFunc) {
	w.jwtRefreshFunc = refreshFunc
}

// Send sends health data to the specified endpoint
func (w *httpWriter) Send(ctx context.Context, data *collector.HealthData, metricsEndpoint string, logsEndpoint string, maxRetries int, authToken string) (string, error) {
	if data == nil {
		return "", fmt.Errorf("nil HealthData")
	}

	// Convert to OTLP format
	otlpData := w.otlpConverter.Convert(data)
	outbound.LogIssues("exporter-http-writer", "OTLPData", outbound.ValidateOTLPPayload(otlpData),
		"collection_id", data.CollectionID,
		"machine_id", data.MachineID)

	var newToken string

	// Send metrics first
	if otlpData.Metrics != nil && len(otlpData.Metrics.ResourceMetrics) > 0 && metricsEndpoint != "" {
		metricsBytes, err := proto.Marshal(otlpData.Metrics)
		if err != nil {
			return "", fmt.Errorf("failed to marshal OTLP metrics data: %w", err)
		}

		token, err := w.sendOTLPRequestWithRetry(ctx, metricsBytes, "metrics", data.CollectionID, data.MachineID, metricsEndpoint, maxRetries, authToken)
		if err != nil {
			log.Logger.Errorw("Failed to send metrics data after all retries",
				"collection_id", data.CollectionID,
				"error", err,
				"size_bytes", len(metricsBytes))
			// Continue to send logs even if metrics fail
		} else if token != "" {
			newToken = token
		}
	}

	// Send logs
	if otlpData.Logs != nil && len(otlpData.Logs.ResourceLogs) > 0 && logsEndpoint != "" {
		logsBytes, err := proto.Marshal(otlpData.Logs)
		if err != nil {
			return newToken, fmt.Errorf("failed to marshal OTLP logs data: %w", err)
		}

		token, err := w.sendOTLPRequestWithRetry(ctx, logsBytes, "logs", data.CollectionID, data.MachineID, logsEndpoint, maxRetries, authToken)
		if err != nil {
			return newToken, fmt.Errorf("failed to send critical logs data (includes events): %w", err)
		} else if token != "" {
			newToken = token
		}
	}

	log.Logger.Infow("Successfully sent health data to both endpoints", "metrics_endpoint", metricsEndpoint, "logs_endpoint", logsEndpoint)
	return newToken, nil
}

// sendOTLPRequestWithRetry sends the OTLP data with retry logic
func (w *httpWriter) sendOTLPRequestWithRetry(ctx context.Context, reqData []byte, dataType, collectionID, machineID, endpoint string, maxRetries int, authToken string) (string, error) {
	if maxRetries <= 0 {
		maxRetries = 1 // At least one attempt
	}

	currentAuthToken := authToken
	var lastErr error
	jwtRefreshAttempted := false

	for attempt := 1; attempt <= maxRetries; attempt++ {
		token, err := w.sendOTLPRequest(ctx, reqData, dataType, collectionID, machineID, endpoint, currentAuthToken)
		if err == nil {
			if attempt > 1 {
				log.Logger.Infow("Request succeeded after retries",
					"data_type", dataType,
					"collection_id", collectionID,
					"attempt", attempt,
					"total_attempts", maxRetries)
			}
			return token, nil
		}

		lastErr = err

		// Check if this is a 401 Unauthorized error and we haven't tried JWT refresh yet
		if w.isUnauthorizedError(err) && !jwtRefreshAttempted && w.jwtRefreshFunc != nil {
			log.Logger.Infow("Received 401 Unauthorized, attempting JWT token refresh",
				"data_type", dataType,
				"collection_id", collectionID,
				"attempt", attempt)

			// Attempt to refresh JWT token
			newJWT, refreshErr := w.jwtRefreshFunc(ctx)
			if refreshErr != nil {
				log.Logger.Errorw("Failed to refresh JWT token",
					"error", refreshErr,
					"data_type", dataType,
					"collection_id", collectionID)
			} else {
				log.Logger.Infow("Successfully refreshed JWT token, retrying request",
					"data_type", dataType,
					"collection_id", collectionID)
				currentAuthToken = newJWT
				jwtRefreshAttempted = true
				// Don't increment attempt counter for JWT refresh retry
				// Next push should use the new JWT token and succeed
				attempt--
				continue
			}
		}

		// If this was the last attempt, don't wait
		if attempt >= maxRetries {
			break
		}

		// Add jitter (0-50% of base delay) to prevent thundering herd
		jitter := calculateJitter(defaultRetryDelay / 2)
		delay := defaultRetryDelay + jitter
		log.Logger.Warnw("Request failed, retrying",
			"data_type", dataType,
			"collection_id", collectionID,
			"attempt", attempt,
			"total_attempts", maxRetries,
			"delay_seconds", delay.Seconds(),
			"jitter_ms", jitter.Milliseconds(),
			"error", err)

		// Wait before retrying (with context cancellation support)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
			// Continue to next attempt
		}
	}

	log.Logger.Errorw("All retry attempts failed",
		"data_type", dataType,
		"collection_id", collectionID,
		"endpoint", endpoint,
		"total_attempts", maxRetries,
		"final_error", lastErr)

	return "", fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr)
}

// sendOTLPRequest sends a single OTLP request
func (w *httpWriter) sendOTLPRequest(ctx context.Context, reqData []byte, dataType, collectionID, machineID, endpoint string, authToken string) (string, error) {
	contentType := "application/x-protobuf"

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(reqData))
	if err != nil {
		return "", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "fleetint-exporter")
	req.Header.Set("X-Machine-ID", machineID)
	req.Header.Set("X-Data-Type", dataType)
	req.Header.Set("X-Collection-ID", collectionID)
	req.Header.Set("X-Agent-Mode", "direct-inventory-write")

	if authToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", authToken))
	}

	// Send request
	resp, err := w.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.Request == nil || resp.Request.URL == nil || req.URL == nil {
		return "", fmt.Errorf("failed to validate response origin")
	}
	if resp.Request.URL.Scheme != req.URL.Scheme || resp.Request.URL.Host != req.URL.Host {
		log.Logger.Warnw("rejecting response from mismatched origin",
			"configured_origin", req.URL.Scheme+"://"+req.URL.Host,
			"response_origin", resp.Request.URL.Scheme+"://"+resp.Request.URL.Host,
			"endpoint", endpoint,
			"data_type", dataType)
		return "", fmt.Errorf("response origin mismatch")
	}

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &HTTPError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Message:    "request failed",
		}
	}

	// Check for JWT token refresh in response headers
	var newToken string
	if headerToken := resp.Header.Get("jwt_assertion"); headerToken != "" {
		newToken = headerToken
		log.Logger.Infow("Received refreshed JWT token from response header",
			"endpoint", endpoint,
			"data_type", dataType,
			"token_length", len(newToken))
	}

	return newToken, nil
}

// isUnauthorizedError checks if the error indicates a 401 Unauthorized response
func (w *httpWriter) isUnauthorizedError(err error) bool {
	if err == nil {
		return false
	}

	// Use errors.As to check for HTTPError type and inspect status code
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusUnauthorized
	}

	return false
}

// calculateJitter returns a random duration between 0 and maxJitter to prevent thundering herd
func calculateJitter(maxJitter time.Duration) time.Duration {
	if maxJitter <= 0 {
		return 0
	}

	// Generate cryptographically secure random number
	maxMs := int64(maxJitter / time.Millisecond)
	if maxMs <= 0 {
		return 0
	}

	randomMs, err := rand.Int(rand.Reader, big.NewInt(maxMs))
	if err != nil {
		log.Logger.Warnw("Failed to generate secure random jitter, using fallback", "error", err)
		// Fallback to simple time-based pseudo-random
		return time.Duration(time.Now().UnixNano()%maxMs) * time.Millisecond
	}

	return time.Duration(randomMs.Int64()) * time.Millisecond
}
