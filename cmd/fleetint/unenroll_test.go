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

package main

import (
	"context"
	"path/filepath"
	"testing"

	pkgmetadata "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metadata"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/sqlite"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/agentstate"
)

func TestRemoveEnrollmentMetadata(t *testing.T) {
	t.Parallel()

	db, err := sqlite.Open(filepath.Join(t.TempDir(), "agent.state"))
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	require.NoError(t, pkgmetadata.CreateTableMetadata(ctx, db))

	for key, value := range map[string]string{
		pkgmetadata.MetadataKeyToken:         "jwt-token",
		agentstate.MetadataKeySAKToken:       "sak-token",
		agentstate.MetadataKeyBackendBaseURL: "https://backend.example.com",
		agentstate.MetadataKeyNodeGroup:      "group-a",
		agentstate.MetadataKeyComputeZone:    "zone-a",
		"enroll_endpoint":                    "https://backend.example.com/api/v1/enroll",
		"metrics_endpoint":                   "https://backend.example.com/api/v1/health/metrics",
		"logs_endpoint":                      "https://backend.example.com/api/v1/health/logs",
		"nonce_endpoint":                     "https://backend.example.com/api/v1/attest/nonce",
		"keep_me":                            "still-here",
	} {
		require.NoError(t, pkgmetadata.SetMetadata(ctx, db, key, value))
	}

	require.NoError(t, removeEnrollmentMetadata(ctx, db))

	for _, key := range []string{
		pkgmetadata.MetadataKeyToken,
		agentstate.MetadataKeySAKToken,
		agentstate.MetadataKeyBackendBaseURL,
		agentstate.MetadataKeyNodeGroup,
		agentstate.MetadataKeyComputeZone,
		"enroll_endpoint",
		"metrics_endpoint",
		"logs_endpoint",
		"nonce_endpoint",
	} {
		value, err := pkgmetadata.ReadMetadata(ctx, db, key)
		require.NoError(t, err)
		require.Empty(t, value)
	}

	value, err := pkgmetadata.ReadMetadata(ctx, db, "keep_me")
	require.NoError(t, err)
	require.Equal(t, "still-here", value)
}
