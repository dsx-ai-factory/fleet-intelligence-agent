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
	"bytes"
	"testing"

	pkgmetadata "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metadata"
	"github.com/stretchr/testify/require"
)

func TestWriteMetadataPreservesAgentIdentityAndMasksSecrets(t *testing.T) {
	metadata := map[string]string{
		"machine_id": "persisted-agent-uuid",
		"sak_token":  "secret-token-value",
		"hostname":   "node-01",
		"agent":      "custom-agent-label",
	}
	var out bytes.Buffer
	writeMetadata(&out, metadata)
	require.Equal(t, "agent: custom-agent-label\nagent_uuid: persisted-agent-uuid\nhostname: node-01\nmachine_id: persisted-agent-uuid\nsak_token: "+pkgmetadata.MaskToken("secret-token-value")+"\n", out.String())
	require.NotContains(t, metadata, "agent_uuid", "display alias must not migrate or duplicate persisted state")
}
