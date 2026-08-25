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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/backendclient"
)

type testNonceClient struct {
	resp        *backendclient.NonceResponse
	gotNodeUUID string
	gotJWT      string
}

func (c *testNonceClient) GetNonce(_ context.Context, nodeUUID, jwt string) (*backendclient.NonceResponse, error) {
	c.gotNodeUUID = nodeUUID
	c.gotJWT = jwt
	return c.resp, nil
}

func TestBackendNonceProvider(t *testing.T) {
	refreshTS := time.Now().UTC()
	client := &testNonceClient{
		resp: &backendclient.NonceResponse{
			Nonce:                 "abc123",
			NonceRefreshTimestamp: refreshTS,
			JWTAssertion:          "new-jwt",
		},
	}
	provider := NewBackendNonceProvider(client)

	nonce, ts, jwt, err := provider.GetNonce(context.Background(), "node-1", "jwt-token")
	require.NoError(t, err)
	require.Equal(t, "abc123", nonce)
	require.Equal(t, refreshTS, ts)
	require.Equal(t, "new-jwt", jwt)
	require.Equal(t, "node-1", client.gotNodeUUID)
	require.Equal(t, "jwt-token", client.gotJWT)
}
