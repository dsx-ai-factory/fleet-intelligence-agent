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
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configopaque"
	"go.opentelemetry.io/collector/config/configtls"
	"go.opentelemetry.io/collector/extension"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testJWT(t *testing.T, customerID string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"assertion": map[string]string{"customer_id": customerID},
	})
	require.NoError(t, err)
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func TestConfigValidateRequiresSecureEndpoint(t *testing.T) {
	require.ErrorContains(t, (&Config{}).Validate(), "enroll_endpoint is required")
	require.ErrorContains(t, (&Config{
		EnrollEndpoint: "https://backend.example/api/v1/agent/enroll",
	}).Validate(), "sak_token is required")

	tests := []struct {
		name     string
		endpoint string
	}{
		{name: "HTTP", endpoint: "http://backend.example/enroll"},
		{name: "relative", endpoint: "/enroll"},
		{name: "credentials", endpoint: "https://user:pass@backend.example/enroll"},
		{name: "fragment", endpoint: "https://backend.example/enroll#secret"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{EnrollEndpoint: tc.endpoint, SAKToken: configopaque.String("sak")}
			require.Error(t, cfg.Validate())
		})
	}

	require.NoError(t, (&Config{
		EnrollEndpoint: "https://backend.example/api/v1/health/enroll",
		SAKToken:       configopaque.String("sak"),
	}).Validate())
	require.ErrorContains(t, (&Config{
		EnrollEndpoint: "https://backend.example/api/v1/health/enroll",
		SAKToken:       configopaque.String("sak"),
		TLS:            configtls.ClientConfig{Insecure: true},
	}).Validate(), "tls.insecure is not allowed")
	// insecure_skip_verify keeps the https:// scheme, so it passes every check
	// above and must be rejected on its own.
	require.ErrorContains(t, (&Config{
		EnrollEndpoint: "https://backend.example/api/v1/health/enroll",
		SAKToken:       configopaque.String("sak"),
		TLS:            configtls.ClientConfig{InsecureSkipVerify: true},
	}).Validate(), "tls.insecure_skip_verify is not allowed")

	cfg := &Config{SAKToken: configopaque.String("sak-token")}
	require.Equal(t, "[REDACTED]", cfg.SAKToken.String())
	require.NotContains(t, cfg.SAKToken.String(), "sak-token")
}

func TestExtractCustomerID(t *testing.T) {
	require.Equal(t, "customer-1", extractCustomerID(testJWT(t, "customer-1")))
	require.Empty(t, extractCustomerID(""))
	require.Empty(t, extractCustomerID("not.a.jwt.with.too.many.parts"))
	require.Empty(t, extractCustomerID("header.%%%.signature"))
	require.Empty(t, extractCustomerID("header.e30.signature"))
}

func TestEnrollResponseToken(t *testing.T) {
	require.Equal(t, "current", (enrollResponse{
		JWTAssertion:       "current",
		LegacyJWTAssertion: "legacy",
	}).token())
	require.Equal(t, "legacy", (enrollResponse{
		LegacyJWTAssertion: "legacy",
	}).token())
}

func TestFactoryCreatesExtension(t *testing.T) {
	cfg := createDefaultConfig()
	require.IsType(t, &Config{}, cfg)
	require.NotNil(t, NewFactory())

	authExtension, err := createExtension(context.Background(), extension.Settings{}, &Config{
		EnrollEndpoint: "https://backend.example/api/v1/agent/enroll",
		SAKToken:       configopaque.String("sak-token"),
	})
	require.NoError(t, err)
	require.IsType(t, &sakAuthExtension{}, authExtension)
	require.NoError(t, authExtension.Shutdown(context.Background()))

	_, err = createExtension(context.Background(), extension.Settings{}, &Config{})
	require.Error(t, err)
}

func TestRoundTripperDefaultsBaseTransport(t *testing.T) {
	ext := &sakAuthExtension{}
	transport, err := ext.RoundTripper(nil)
	require.NoError(t, err)
	require.NotNil(t, transport)
	require.Implements(t, (*component.Component)(nil), ext)
}

func TestEnrollmentAndUnauthorizedRefresh(t *testing.T) {
	firstJWT := testJWT(t, "customer-1")
	secondJWT := testJWT(t, "customer-2")
	var enrollments atomic.Int32
	var exports atomic.Int32
	enrollmentAuth := make(chan string, 2)
	exportHeaders := make(chan [2]string, 2)
	handlerErrors := make(chan error, 2)

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/enroll":
			enrollmentAuth <- r.Header.Get("Authorization")
			count := enrollments.Add(1)
			token := firstJWT
			if count > 1 {
				token = secondJWT
			}
			if err := json.NewEncoder(w).Encode(enrollResponse{JWTAssertion: token}); err != nil {
				handlerErrors <- err
			}
		case "/v1/metrics":
			count := exports.Add(1)
			exportHeaders <- [2]string{
				r.Header.Get("Authorization"),
				r.Header.Get("Nv-Actor-Id"),
			}
			if count == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ext, err := newSAKAuthExtension(&Config{
		EnrollEndpoint: server.URL + "/enroll",
		SAKToken:       configopaque.String("sak-token"),
	})
	require.NoError(t, err)
	ext.client = server.Client()
	ext.client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	require.NoError(t, ext.Start(context.Background(), nil))

	transport, err := ext.RoundTripper(server.Client().Transport)
	require.NoError(t, err)
	client := &http.Client{Transport: transport}
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/metrics", bytes.NewReader([]byte("payload")))
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, int32(2), enrollments.Load())
	require.Equal(t, int32(2), exports.Load())
	require.Equal(t, "Bearer sak-token", <-enrollmentAuth)
	require.Equal(t, "Bearer sak-token", <-enrollmentAuth)
	require.Equal(t, [2]string{"Bearer " + firstJWT, "customer-1"}, <-exportHeaders)
	require.Equal(t, [2]string{"Bearer " + secondJWT, "customer-2"}, <-exportHeaders)
	select {
	case handlerErr := <-handlerErrors:
		require.NoError(t, handlerErr)
	default:
	}
}

func TestEnrollmentDoesNotFollowRedirects(t *testing.T) {
	var redirectTargetCalls atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectTargetCalls.Add(1)
	}))
	defer target.Close()

	redirector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	ext, err := newSAKAuthExtension(&Config{
		EnrollEndpoint: redirector.URL,
		SAKToken:       configopaque.String("sak-token"),
	})
	require.NoError(t, err)
	ext.client = redirector.Client()
	ext.client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	_, err = ext.performEnrollment(context.Background())
	require.ErrorContains(t, err, "HTTP 307")
	require.Zero(t, redirectTargetCalls.Load())
}

func TestEnrollmentRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), (1<<20)+1))
	}))
	defer server.Close()

	ext, err := newSAKAuthExtension(&Config{
		EnrollEndpoint: server.URL,
		SAKToken:       configopaque.String("sak-token"),
	})
	require.NoError(t, err)
	ext.client = server.Client()

	_, err = ext.performEnrollment(context.Background())
	require.ErrorContains(t, err, "exceeds 1048576 bytes")
}

func TestEnrollmentResponseErrors(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		errorContains string
	}{
		{
			name:          "HTTP status does not expose body",
			status:        http.StatusUnauthorized,
			body:          "sensitive backend details",
			errorContains: "HTTP 401",
		},
		{
			name:          "invalid JSON",
			status:        http.StatusOK,
			body:          "{",
			errorContains: "failed to parse enrollment response",
		},
		{
			name:          "missing assertion",
			status:        http.StatusOK,
			body:          "{}",
			errorContains: "missing jwtAssertion",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()

			ext, err := newSAKAuthExtension(&Config{
				EnrollEndpoint: server.URL,
				SAKToken:       configopaque.String("sak-token"),
			})
			require.NoError(t, err)
			ext.client = server.Client()

			_, err = ext.performEnrollment(context.Background())
			require.ErrorContains(t, err, tc.errorContains)
			require.NotContains(t, err.Error(), "sensitive backend details")
		})
	}
}

func TestEnrollmentHonorsCanceledContext(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	ext, err := newSAKAuthExtension(&Config{
		EnrollEndpoint: server.URL,
		SAKToken:       configopaque.String("sak-token"),
	})
	require.NoError(t, err)
	ext.client = server.Client()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ext.performEnrollment(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestUnauthorizedNonReplayableRequestDoesNotRefresh(t *testing.T) {
	ext := &sakAuthExtension{jwt: "jwt-token"}
	transport, err := ext.RoundTripper(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{},
			Body:       http.NoBody,
		}, nil
	}))
	require.NoError(t, err)

	req, err := http.NewRequest(
		http.MethodPost,
		"https://backend.example/v1/metrics",
		io.NopCloser(strings.NewReader("payload")),
	)
	require.NoError(t, err)
	require.Nil(t, req.GetBody)

	_, err = transport.RoundTrip(req)
	require.ErrorContains(t, err, "request body is not replayable")
}

func TestSecondUnauthorizedResponseIsReturnedWithoutRefreshLoop(t *testing.T) {
	firstJWT := testJWT(t, "customer-1")
	secondJWT := testJWT(t, "customer-2")
	var enrollments atomic.Int32
	var exports atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/enroll":
			token := firstJWT
			if enrollments.Add(1) > 1 {
				token = secondJWT
			}
			_ = json.NewEncoder(w).Encode(enrollResponse{JWTAssertion: token})
		case "/v1/metrics":
			exports.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ext, err := newSAKAuthExtension(&Config{
		EnrollEndpoint: server.URL + "/enroll",
		SAKToken:       configopaque.String("sak-token"),
	})
	require.NoError(t, err)
	ext.client = server.Client()
	require.NoError(t, ext.Start(context.Background(), nil))
	transport, err := ext.RoundTripper(server.Client().Transport)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/metrics", bytes.NewReader([]byte("payload")))
	require.NoError(t, err)
	resp, err := transport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Equal(t, int32(2), enrollments.Load())
	require.Equal(t, int32(2), exports.Load())
}

func TestResponseHeaderRefreshesJWT(t *testing.T) {
	firstJWT := testJWT(t, "customer-1")
	secondJWT := testJWT(t, "customer-2")
	ext := &sakAuthExtension{jwt: firstJWT, customerID: "customer-1"}

	var requests atomic.Int32
	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		count := requests.Add(1)
		if count == 1 {
			require.Equal(t, "Bearer "+firstJWT, req.Header.Get("Authorization"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Jwt_assertion": []string{secondJWT}},
				Body:       http.NoBody,
			}, nil
		}
		require.Equal(t, "Bearer "+secondJWT, req.Header.Get("Authorization"))
		require.Equal(t, "customer-2", req.Header.Get("Nv-Actor-Id"))
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody}, nil
	})

	transport, err := ext.RoundTripper(base)
	require.NoError(t, err)
	for range 2 {
		req, err := http.NewRequest(http.MethodPost, "https://backend.example/v1/metrics", bytes.NewReader([]byte("payload")))
		require.NoError(t, err)
		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
	require.Equal(t, int32(2), requests.Load())
}

// Two exports start with the same token and their responses complete out of
// order, each carrying a different refreshed JWT. The response that finishes
// last is the one carrying the older token, and it must not overwrite the
// newer one already installed by the response that finished first.
func TestResponseHeaderRefreshIgnoresStaleToken(t *testing.T) {
	initialJWT := testJWT(t, "customer-1")
	newerJWT := testJWT(t, "customer-2")
	staleJWT := testJWT(t, "customer-3")
	ext := &sakAuthExtension{jwt: initialJWT, customerID: "customer-1"}

	slowSnapshotted := make(chan struct{})
	fastStored := make(chan struct{})

	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		header := http.Header{}
		switch req.URL.Path {
		case "/slow":
			// initialJWT is already snapshotted by the time base is reached,
			// so let the other request overtake this one.
			close(slowSnapshotted)
			<-fastStored
			header.Set("jwt_assertion", staleJWT)
		case "/fast":
			<-slowSnapshotted
			header.Set("jwt_assertion", newerJWT)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: header, Body: http.NoBody}, nil
	})

	transport, err := ext.RoundTripper(base)
	require.NoError(t, err)

	// Errors are returned rather than asserted, since require must only be
	// called from the goroutine running the test.
	export := func(path string) error {
		req, reqErr := http.NewRequest(http.MethodPost, "https://backend.example"+path, http.NoBody)
		if reqErr != nil {
			return reqErr
		}
		resp, rtErr := transport.RoundTrip(req)
		if rtErr != nil {
			return rtErr
		}
		return resp.Body.Close()
	}

	errs := make(chan error, 2)
	go func() { errs <- export("/slow") }()
	go func() {
		exportErr := export("/fast")
		close(fastStored)
		errs <- exportErr
	}()
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)

	jwt, customerID := ext.snapshot()
	require.Equal(t, newerJWT, jwt, "late response overwrote a newer token")
	require.Equal(t, "customer-2", customerID)
}

func TestConcurrentUnauthorizedResponsesSingleRefresh(t *testing.T) {
	firstJWT := testJWT(t, "customer-1")
	secondJWT := testJWT(t, "customer-2")
	var enrollments atomic.Int32

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/enroll":
			count := enrollments.Add(1)
			token := firstJWT
			if count > 1 {
				token = secondJWT
			}
			_ = json.NewEncoder(w).Encode(enrollResponse{JWTAssertion: token})
		case "/v1/metrics":
			if r.Header.Get("Authorization") == "Bearer "+firstJWT {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ext, err := newSAKAuthExtension(&Config{
		EnrollEndpoint: server.URL + "/enroll",
		SAKToken:       configopaque.String("sak-token"),
	})
	require.NoError(t, err)
	ext.client = server.Client()
	require.NoError(t, ext.Start(context.Background(), nil))
	transport, err := ext.RoundTripper(server.Client().Transport)
	require.NoError(t, err)

	const concurrency = 8
	start := make(chan struct{})
	errs := make(chan error, concurrency)
	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/metrics", bytes.NewReader([]byte("payload")))
			if err != nil {
				errs <- err
				return
			}
			resp, err := transport.RoundTrip(req)
			if err == nil {
				err = resp.Body.Close()
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(2), enrollments.Load())
}
