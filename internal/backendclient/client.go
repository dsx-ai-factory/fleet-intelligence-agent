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

// Package backendclient provides the agent-facing client for backend workflows.
package backendclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/endpoint"
)

const (
	userAgent            = "fleet-intelligence-agent"
	maxResponseBodyBytes = 1 << 20
)

var errRedirectNotAllowed = errors.New("backend redirects are not allowed")
var errNilBaseURL = errors.New("backend base URL is required")

// Client is the backend workflow client used by enrollment, inventory, and attestation paths.
type Client interface {
	Enroll(ctx context.Context, sakToken string) (jwt string, err error)
	UpsertNode(ctx context.Context, nodeUUID string, req *NodeUpsertRequest, jwt string) (*NodeUpsertResponse, error)
	GetNonce(ctx context.Context, nodeUUID string, jwt string) (*NonceResponse, error)
	SubmitAttestation(ctx context.Context, nodeUUID string, req *AttestationRequest, jwt string) error
}

type client struct {
	httpClient *http.Client
	baseURL    *url.URL
}

// New creates a backend client from a validated backend base URL.
func New(rawBaseURL string) (Client, error) {
	baseURL, err := endpoint.ValidateBackendEndpoint(rawBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid backend base URL: %w", err)
	}

	return NewWithHTTPClient(baseURL, &http.Client{Timeout: 30 * time.Second}), nil
}

// NewWithHTTPClient creates a backend client with an explicit HTTP client.
func NewWithHTTPClient(baseURL *url.URL, httpClient *http.Client) Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	if httpClient.CheckRedirect == nil {
		httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
			return errRedirectNotAllowed
		}
	}
	return &client{
		httpClient: httpClient,
		baseURL:    baseURL,
	}
}

func (c *client) Enroll(ctx context.Context, sakToken string) (string, error) {
	if sakToken == "" {
		return "", fmt.Errorf("sakToken cannot be empty")
	}

	var resp struct {
		JWTAssertion string `json:"jwtAssertion"`
	}
	if err := c.doJSON(ctx, http.MethodPost, []string{"v1", "agent", "enroll"}, sakToken, nil, &resp); err != nil {
		return "", mapEnrollError(err)
	}
	if resp.JWTAssertion == "" {
		return "", fmt.Errorf("enrollment response missing jwtAssertion field")
	}
	return resp.JWTAssertion, nil
}

func (c *client) UpsertNode(ctx context.Context, nodeUUID string, req *NodeUpsertRequest, jwt string) (*NodeUpsertResponse, error) {
	if nodeUUID == "" {
		return nil, fmt.Errorf("nodeUUID cannot be empty")
	}
	if jwt == "" {
		return nil, fmt.Errorf("jwt cannot be empty")
	}
	if req == nil {
		return nil, fmt.Errorf("node upsert request cannot be nil")
	}
	var wireResp struct {
		NodeUUID    string  `json:"nodeUUID"`
		ComputeZone *string `json:"computeZone"`
		NodeGroup   *string `json:"nodeGroup"`
	}
	if err := c.doJSON(ctx, http.MethodPut, []string{"v1", "agent", "nodes", nodeUUID}, jwt, req, &wireResp); err != nil {
		return nil, err
	}
	if wireResp.NodeUUID == "" {
		return nil, fmt.Errorf("node upsert response missing nodeUUID field")
	}
	if wireResp.NodeUUID != nodeUUID {
		return nil, fmt.Errorf("node upsert response nodeUUID %q does not match requested nodeUUID %q", wireResp.NodeUUID, nodeUUID)
	}
	if wireResp.NodeGroup == nil {
		return nil, fmt.Errorf("node upsert response missing nodeGroup field")
	}
	if wireResp.ComputeZone == nil {
		return nil, fmt.Errorf("node upsert response missing computeZone field")
	}
	return &NodeUpsertResponse{
		NodeUUID:    wireResp.NodeUUID,
		NodeGroup:   *wireResp.NodeGroup,
		ComputeZone: *wireResp.ComputeZone,
	}, nil
}

func (c *client) GetNonce(ctx context.Context, nodeUUID string, jwt string) (*NonceResponse, error) {
	if nodeUUID == "" {
		return nil, fmt.Errorf("nodeUUID cannot be empty")
	}
	if jwt == "" {
		return nil, fmt.Errorf("jwt cannot be empty")
	}

	var resp NonceResponse
	if err := c.doJSON(ctx, http.MethodPost, []string{"v1", "agent", "nodes", nodeUUID, "nonce"}, jwt, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Nonce == "" {
		return nil, fmt.Errorf("nonce response missing nonce field")
	}
	return &resp, nil
}

func (c *client) SubmitAttestation(ctx context.Context, nodeUUID string, req *AttestationRequest, jwt string) error {
	if nodeUUID == "" {
		return fmt.Errorf("nodeUUID cannot be empty")
	}
	if jwt == "" {
		return fmt.Errorf("jwt cannot be empty")
	}
	if req == nil {
		return fmt.Errorf("attestation request cannot be nil")
	}
	return c.doJSON(ctx, http.MethodPost, []string{"v1", "agent", "nodes", nodeUUID, "attestation"}, jwt, req, nil)
}

func (c *client) doJSON(ctx context.Context, method string, pathElems []string, bearerToken string, reqBody any, respBody any) error {
	if c.baseURL == nil {
		return errNilBaseURL
	}
	requestURL, err := endpoint.JoinPath(c.baseURL, pathElems...)
	if err != nil {
		return fmt.Errorf("failed to construct request URL: %w", err)
	}

	var bodyReader io.Reader
	if reqBody != nil {
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if bearerToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", bearerToken))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make backend request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return fmt.Errorf("failed to read backend response: %w", err)
	}
	if len(data) > maxResponseBodyBytes {
		return fmt.Errorf("backend response too large (max %d bytes)", maxResponseBodyBytes)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Body:       string(bytes.TrimSpace(data)),
		}
	}

	if respBody == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, respBody); err != nil {
		return fmt.Errorf("failed to parse backend response: %w", err)
	}
	return nil
}

func mapEnrollError(err error) error {
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		return err
	}

	switch statusErr.StatusCode {
	case http.StatusBadRequest:
		return fmt.Errorf("the token used in the enrollment is not in the correct format")
	case http.StatusUnauthorized:
		return fmt.Errorf("the token used in the enrollment is incorrect")
	case http.StatusForbidden:
		return fmt.Errorf("the token used in the enrollment is incorrect/expired")
	case http.StatusNotFound:
		return fmt.Errorf("the endpoint is not found")
	case http.StatusTooManyRequests:
		return fmt.Errorf("please retry after some time; server is under heavy load")
	case http.StatusBadGateway:
		return fmt.Errorf("some temporary issue caused enrollment to fail")
	case http.StatusServiceUnavailable:
		return fmt.Errorf("service is unavailable currently")
	case http.StatusGatewayTimeout:
		return fmt.Errorf("service is experiencing load and is slow to respond")
	default:
		return err
	}
}
