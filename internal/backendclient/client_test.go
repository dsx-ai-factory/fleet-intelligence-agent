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

package backendclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_Enroll(t *testing.T) {
	t.Parallel()

	var (
		gotMethod string
		gotPath   string
		gotAuth   string
		gotUA     string
	)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"jwtAssertion": "jwt-token"})
	}))
	defer server.Close()

	c := NewWithHTTPClient(mustParseURL(t, server.URL), server.Client())

	jwt, err := c.Enroll(context.Background(), "sak-token")
	require.NoError(t, err)
	require.Equal(t, "jwt-token", jwt)
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/v1/agent/enroll", gotPath)
	require.Equal(t, "Bearer sak-token", gotAuth)
	require.Equal(t, userAgent, gotUA)
}

func TestNew(t *testing.T) {
	t.Parallel()

	c, err := New("https://backend.example.com")
	require.NoError(t, err)
	require.NotNil(t, c)
}

func TestClient_UpsertNode(t *testing.T) {
	t.Parallel()

	var (
		gotMethod string
		gotPath   string
		gotAuth   string
		gotReq    NodeUpsertRequest
	)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		_ = json.NewEncoder(w).Encode(NodeUpsertResponse{
			NodeUUID:    "node-1",
			NodeGroup:   "resolved-group",
			ComputeZone: "resolved-zone",
		})
	}))
	defer server.Close()

	c := NewWithHTTPClient(mustParseURL(t, server.URL), server.Client())
	resp, err := c.UpsertNode(context.Background(), "node-1", &NodeUpsertRequest{Hostname: "node-1"}, "jwt-token")
	require.NoError(t, err)
	require.Equal(t, &NodeUpsertResponse{
		NodeUUID:    "node-1",
		NodeGroup:   "resolved-group",
		ComputeZone: "resolved-zone",
	}, resp)
	require.Equal(t, http.MethodPut, gotMethod)
	require.Equal(t, "/v1/agent/nodes/node-1", gotPath)
	require.Equal(t, "Bearer jwt-token", gotAuth)
	require.Equal(t, "node-1", gotReq.Hostname)
}

func TestClient_GetNonce(t *testing.T) {
	t.Parallel()

	var (
		gotMethod string
		gotPath   string
		gotAuth   string
	)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(NonceResponse{
			Nonce:        "abc123",
			JWTAssertion: "new-jwt",
		})
	}))
	defer server.Close()

	c := NewWithHTTPClient(mustParseURL(t, server.URL), server.Client())
	resp, err := c.GetNonce(context.Background(), "node-1", "jwt-token")
	require.NoError(t, err)
	require.Equal(t, "abc123", resp.Nonce)
	require.Equal(t, "new-jwt", resp.JWTAssertion)
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/v1/agent/nodes/node-1/nonce", gotPath)
	require.Equal(t, "Bearer jwt-token", gotAuth)
}

func TestClient_SubmitAttestation(t *testing.T) {
	t.Parallel()

	var (
		gotMethod string
		gotPath   string
		gotAuth   string
	)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := NewWithHTTPClient(mustParseURL(t, server.URL), server.Client())
	err := c.SubmitAttestation(context.Background(), "node-1", &AttestationRequest{}, "jwt-token")
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/v1/agent/nodes/node-1/attestation", gotPath)
	require.Equal(t, "Bearer jwt-token", gotAuth)
}

func TestClient_EnrollMapsHTTPStatus(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad token", http.StatusUnauthorized)
	}))
	defer server.Close()

	c := NewWithHTTPClient(mustParseURL(t, server.URL), server.Client())
	_, err := c.Enroll(context.Background(), "sak-token")
	require.Error(t, err)
	require.Contains(t, err.Error(), "incorrect")
}

func TestClient_ValidationErrors(t *testing.T) {
	t.Parallel()

	c := NewWithHTTPClient(mustParseURL(t, "https://backend.example.com"), nil)

	_, err := c.Enroll(context.Background(), "")
	require.ErrorContains(t, err, "sakToken cannot be empty")

	_, err = c.UpsertNode(context.Background(), "", &NodeUpsertRequest{}, "jwt")
	require.ErrorContains(t, err, "nodeUUID cannot be empty")
	_, err = c.UpsertNode(context.Background(), "node-1", nil, "jwt")
	require.ErrorContains(t, err, "cannot be nil")
	_, err = c.UpsertNode(context.Background(), "node-1", &NodeUpsertRequest{}, "")
	require.ErrorContains(t, err, "jwt cannot be empty")

	_, err = c.GetNonce(context.Background(), "", "jwt")
	require.ErrorContains(t, err, "nodeUUID cannot be empty")
	_, err = c.GetNonce(context.Background(), "node-1", "")
	require.ErrorContains(t, err, "jwt cannot be empty")

	err = c.SubmitAttestation(context.Background(), "", &AttestationRequest{}, "jwt")
	require.ErrorContains(t, err, "nodeUUID cannot be empty")
	err = c.SubmitAttestation(context.Background(), "node-1", nil, "jwt")
	require.ErrorContains(t, err, "cannot be nil")
	err = c.SubmitAttestation(context.Background(), "node-1", &AttestationRequest{}, "")
	require.ErrorContains(t, err, "jwt cannot be empty")

	c = NewWithHTTPClient(nil, nil)
	_, err = c.Enroll(context.Background(), "sak-token")
	require.ErrorIs(t, err, errNilBaseURL)
}

func TestClient_RejectsRedirects(t *testing.T) {
	t.Parallel()

	redirected := false
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected = true
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	c := NewWithHTTPClient(mustParseURL(t, server.URL), server.Client())
	_, err := c.Enroll(context.Background(), "sak-token")
	require.ErrorContains(t, err, errRedirectNotAllowed.Error())
	require.False(t, redirected)
}

func TestClient_ResponseValidationAndErrors(t *testing.T) {
	t.Parallel()

	t.Run("missing jwt assertion in enroll", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{})
		}))
		defer server.Close()

		c := NewWithHTTPClient(mustParseURL(t, server.URL), server.Client())
		_, err := c.Enroll(context.Background(), "sak-token")
		require.ErrorContains(t, err, "missing jwtAssertion")
	})

	t.Run("missing nonce field", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{})
		}))
		defer server.Close()

		c := NewWithHTTPClient(mustParseURL(t, server.URL), server.Client())
		_, err := c.GetNonce(context.Background(), "node-1", "jwt-token")
		require.ErrorContains(t, err, "missing nonce")
	})

	t.Run("invalid json response", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("{invalid"))
		}))
		defer server.Close()

		c := NewWithHTTPClient(mustParseURL(t, server.URL), server.Client())
		_, err := c.UpsertNode(context.Background(), "node-1", &NodeUpsertRequest{Hostname: "node-1"}, "jwt-token")
		require.ErrorContains(t, err, "failed to parse backend response")

		_, err = c.GetNonce(context.Background(), "node-1", "jwt-token")
		require.ErrorContains(t, err, "failed to parse backend response")
	})

	t.Run("http client error", func(t *testing.T) {
		c := NewWithHTTPClient(mustParseURL(t, "https://backend.example.com"), &http.Client{
			Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("network boom")
			}),
		})
		_, err := c.UpsertNode(context.Background(), "node-1", &NodeUpsertRequest{Hostname: "node-1"}, "jwt-token")
		require.ErrorContains(t, err, "failed to make backend request")
	})

	t.Run("missing node uuid in upsert response", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(NodeUpsertResponse{NodeGroup: "group-a", ComputeZone: "zone-a"})
		}))
		defer server.Close()

		c := NewWithHTTPClient(mustParseURL(t, server.URL), server.Client())
		_, err := c.UpsertNode(context.Background(), "node-1", &NodeUpsertRequest{}, "jwt-token")
		require.ErrorContains(t, err, "missing nodeUUID")
	})

	t.Run("mismatched node uuid in upsert response", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(NodeUpsertResponse{NodeUUID: "node-2"})
		}))
		defer server.Close()

		c := NewWithHTTPClient(mustParseURL(t, server.URL), server.Client())
		_, err := c.UpsertNode(context.Background(), "node-1", &NodeUpsertRequest{}, "jwt-token")
		require.ErrorContains(t, err, "does not match requested nodeUUID")
	})

	t.Run("missing resolved membership field in upsert response", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"nodeUUID":  "node-1",
				"nodeGroup": "group-a",
			})
		}))
		defer server.Close()

		c := NewWithHTTPClient(mustParseURL(t, server.URL), server.Client())
		_, err := c.UpsertNode(context.Background(), "node-1", &NodeUpsertRequest{}, "jwt-token")
		require.ErrorContains(t, err, "missing computeZone")
	})

	t.Run("oversized response body", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("a", maxResponseBodyBytes+10)))
		}))
		defer server.Close()

		c := NewWithHTTPClient(mustParseURL(t, server.URL), server.Client())
		_, err := c.GetNonce(context.Background(), "node-1", "jwt-token")
		require.ErrorContains(t, err, "response too large")
	})
}

func TestClient_HandlerAssertionsDoNotRace(t *testing.T) {
	t.Parallel()

	var (
		mu     sync.Mutex
		method string
	)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		method = r.Method
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"jwtAssertion": "jwt-token"})
	}))
	defer server.Close()

	c := NewWithHTTPClient(mustParseURL(t, server.URL), server.Client())
	_, err := c.Enroll(context.Background(), "sak-token")
	require.NoError(t, err)

	mu.Lock()
	gotMethod := method
	mu.Unlock()
	assert.Equal(t, http.MethodPost, gotMethod)
}

func TestMapEnrollErrorStatuses(t *testing.T) {
	t.Parallel()

	cases := map[int]string{
		http.StatusBadRequest:         "correct format",
		http.StatusUnauthorized:       "incorrect",
		http.StatusForbidden:          "incorrect/expired",
		http.StatusNotFound:           "not found",
		http.StatusTooManyRequests:    "retry after some time",
		http.StatusBadGateway:         "temporary issue",
		http.StatusServiceUnavailable: "unavailable",
		http.StatusGatewayTimeout:     "slow to respond",
	}

	for status, want := range cases {
		got := mapEnrollError(&HTTPStatusError{StatusCode: status})
		require.ErrorContains(t, got, want)
	}

	other := &HTTPStatusError{StatusCode: http.StatusTeapot}
	require.Equal(t, other, mapEnrollError(other))

	plain := errors.New("plain")
	require.Equal(t, plain, mapEnrollError(plain))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	return parsed
}
