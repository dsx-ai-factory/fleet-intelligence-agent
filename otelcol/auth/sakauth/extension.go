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

// Package sakauth implements an OpenTelemetry Collector auth.Client extension
// that enrolls with the Fleet Intelligence backend using a SAK token
// (Authorization: Bearer) to obtain a short-lived JWT, and automatically
// refreshes it via response headers or on 401 responses.
//
// The customer ID is not configured explicitly — it is extracted from the
// JWT's assertion.customer_id claim after enrollment and forwarded as
// Nv-Actor-Id on outbound OTLP requests.
package sakauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configtls"
	"go.opentelemetry.io/collector/extension/extensionauth"
)

// enrollResponse mirrors the backend enrollment response.
type enrollResponse struct {
	JWTAssertion       string `json:"jwtAssertion"`
	LegacyJWTAssertion string `json:"jwt_assertion"`
}

func (r enrollResponse) token() string {
	if r.JWTAssertion != "" {
		return r.JWTAssertion
	}
	return r.LegacyJWTAssertion
}

// sakAuthExtension implements HTTP client authentication using the SAK→JWT enrollment flow.
type sakAuthExtension struct {
	cfg        *Config
	client     *http.Client
	jwt        string
	customerID string // extracted from JWT assertion.customer_id after enrollment
	mu         sync.RWMutex
	refreshMu  sync.Mutex
}

// Ensure the extension satisfies the HTTP client authenticator interface.
var _ extensionauth.HTTPClient = (*sakAuthExtension)(nil)

func newSAKAuthExtension(cfg *Config) (*sakAuthExtension, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	client, err := newEnrollmentHTTPClient(&cfg.TLS)
	if err != nil {
		return nil, fmt.Errorf("sakauth: invalid TLS configuration: %w", err)
	}
	return &sakAuthExtension{
		cfg:    cfg,
		client: client,
	}, nil
}

func newEnrollmentHTTPClient(tlsConfig *configtls.ClientConfig) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	loadedTLSConfig, err := tlsConfig.LoadTLSConfig(context.Background())
	if err != nil {
		return nil, err
	}
	transport.TLSClientConfig = loadedTLSConfig
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
		// Never forward the SAK to a redirect target.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

// Start fetches the initial JWT and extracts the customer ID from it.
func (e *sakAuthExtension) Start(ctx context.Context, _ component.Host) error {
	jwt, err := e.performEnrollment(ctx)
	if err != nil {
		return fmt.Errorf("sakauth: initial enrollment failed: %w", err)
	}
	e.mu.Lock()
	e.jwt = jwt
	e.customerID = extractCustomerID(jwt)
	e.mu.Unlock()
	return nil
}

// Shutdown is a no-op for this extension.
func (e *sakAuthExtension) Shutdown(_ context.Context) error {
	return nil
}

// RoundTripper returns an http.RoundTripper that injects the current JWT into
// each outbound request and refreshes it on a 401 response.
func (e *sakAuthExtension) RoundTripper(base http.RoundTripper) (http.RoundTripper, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	return &sakRoundTripper{base: base, ext: e}, nil
}

func (e *sakAuthExtension) getJWT() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.jwt
}

func (e *sakAuthExtension) snapshot() (jwt, customerID string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.jwt, e.customerID
}

func (e *sakAuthExtension) refreshJWT(ctx context.Context, staleJWT string) (string, error) {
	e.refreshMu.Lock()
	defer e.refreshMu.Unlock()

	// Another request may already have refreshed the shared token while this
	// request was waiting.
	if current := e.getJWT(); current != "" && current != staleJWT {
		return current, nil
	}

	jwt, err := e.performEnrollment(ctx)
	if err != nil {
		return "", err
	}
	e.mu.Lock()
	e.jwt = jwt
	e.customerID = extractCustomerID(jwt)
	e.mu.Unlock()
	return jwt, nil
}

// performEnrollment calls the backend enrollment endpoint with the SAK token
// and returns the JWT from the response.
func (e *sakAuthExtension) performEnrollment(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.EnrollEndpoint, nil)
	if err != nil {
		return "", fmt.Errorf("failed to build enrollment request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+string(e.cfg.SAKToken))
	req.Header.Set("User-Agent", "fleetint-otelcol")

	resp, err := e.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("enrollment request failed: %w", err)
	}
	defer resp.Body.Close()

	const maxEnrollmentResponseSize = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxEnrollmentResponseSize+1))
	if err != nil {
		return "", fmt.Errorf("failed to read enrollment response: %w", err)
	}
	if len(body) > maxEnrollmentResponseSize {
		return "", fmt.Errorf("enrollment response exceeds %d bytes", maxEnrollmentResponseSize)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("enrollment returned HTTP %d", resp.StatusCode)
	}

	var result enrollResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse enrollment response: %w", err)
	}
	jwt := result.token()
	if jwt == "" {
		return "", fmt.Errorf("enrollment response missing jwtAssertion field")
	}
	return jwt, nil
}

// extractCustomerID decodes the JWT payload and returns assertion.customer_id.
// Returns an empty string if the claim is absent or the token is malformed.
func extractCustomerID(token string) string {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return ""
	}
	payload := parts[1]
	if r := len(payload) % 4; r != 0 {
		payload += strings.Repeat("=", 4-r)
	}
	b, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}
	var claims struct {
		Assertion struct {
			CustomerID string `json:"customer_id"`
		} `json:"assertion"`
	}
	if err := json.Unmarshal(b, &claims); err != nil {
		return ""
	}
	return claims.Assertion.CustomerID
}

// sakRoundTripper injects the JWT and handles transparent token refresh on 401.
type sakRoundTripper struct {
	base http.RoundTripper
	ext  *sakAuthExtension
}

func (rt *sakRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	usedJWT, customerID := rt.ext.snapshot()
	req.Header.Set("Authorization", "Bearer "+usedJWT)
	if customerID != "" {
		req.Header.Set("Nv-Actor-Id", customerID)
	} else {
		req.Header.Del("Nv-Actor-Id")
	}

	resp, err := rt.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	// The backend proactively sends a refreshed JWT in the response header when
	// the current token is near expiry (>80% of max age). Store it immediately.
	if newJWT := resp.Header.Get("jwt_assertion"); newJWT != "" {
		rt.ext.mu.Lock()
		rt.ext.jwt = newJWT
		rt.ext.customerID = extractCustomerID(newJWT)
		rt.ext.mu.Unlock()
	}

	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	// 401 fallback: re-enroll and retry once.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()

	// Restore the request body for the retry. The OTel exporter sets GetBody
	// so we can get a fresh reader; if it didn't, we can't retry safely.
	if req.GetBody == nil {
		return nil, fmt.Errorf("sakauth: 401 received but request body is not replayable; cannot retry")
	}
	newBody, err := req.GetBody()
	if err != nil {
		return nil, fmt.Errorf("sakauth: failed to replay request body for retry: %w", err)
	}

	newJWT, err := rt.ext.refreshJWT(req.Context(), usedJWT)
	if err != nil {
		return nil, fmt.Errorf("sakauth: token refresh after 401 failed: %w", err)
	}

	req = req.Clone(req.Context())
	req.Body = newBody
	req.Header.Set("Authorization", "Bearer "+newJWT)
	if customerID := extractCustomerID(newJWT); customerID != "" {
		req.Header.Set("Nv-Actor-Id", customerID)
	} else {
		req.Header.Del("Nv-Actor-Id")
	}
	return rt.base.RoundTrip(req)
}
