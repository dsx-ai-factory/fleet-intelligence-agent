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

package attestation

import (
	"context"
	"fmt"
	"time"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/backendclient"
)

// NonceBackendClient is the backend client view required by the nonce provider.
type NonceBackendClient interface {
	GetNonce(ctx context.Context, nodeUUID string, jwt string) (*backendclient.NonceResponse, error)
}

type backendNonceProvider struct {
	client NonceBackendClient
}

// NewBackendNonceProvider creates a nonce provider backed by the agent backend client.
func NewBackendNonceProvider(client NonceBackendClient) NonceProvider {
	return &backendNonceProvider{client: client}
}

func (p *backendNonceProvider) GetNonce(ctx context.Context, nodeUUID, jwt string) (string, time.Time, string, error) {
	if p.client == nil {
		return "", time.Time{}, "", fmt.Errorf("nonce provider requires backend client")
	}
	if nodeUUID == "" {
		return "", time.Time{}, "", fmt.Errorf("nonce provider requires node UUID")
	}
	if jwt == "" {
		return "", time.Time{}, "", fmt.Errorf("nonce provider requires jwt")
	}

	resp, err := p.client.GetNonce(ctx, nodeUUID, jwt)
	if err != nil {
		return "", time.Time{}, "", err
	}
	if resp == nil {
		return "", time.Time{}, "", fmt.Errorf("nonce response is nil")
	}
	return resp.Nonce, resp.NonceRefreshTimestamp, resp.JWTAssertion, nil
}
