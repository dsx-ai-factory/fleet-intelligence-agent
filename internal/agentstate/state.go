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

// Package agentstate centralizes access to local persisted agent state.
package agentstate

import (
	"context"
	"time"
)

const (
	MetadataKeyBackendBaseURL = "backend_base_url"
	MetadataKeySAKToken       = "sak_token"
	MetadataKeyEnrolledAt     = "enrolled_at"
	MetadataKeyNodeGroup      = "node_group"
	MetadataKeyComputeZone    = "compute_zone"
)

// State provides local persisted metadata/state access for backend workflows.
type State interface {
	GetBackendBaseURL(ctx context.Context) (value string, ok bool, err error)
	SetBackendBaseURL(ctx context.Context, value string) error

	GetJWT(ctx context.Context) (value string, ok bool, err error)
	SetJWT(ctx context.Context, value string) error

	GetSAK(ctx context.Context) (value string, ok bool, err error)
	SetSAK(ctx context.Context, value string) error

	// GetNodeUUID reads the committed node UUID without creating one.
	GetNodeUUID(ctx context.Context) (value string, ok bool, err error)
	SetNodeUUID(ctx context.Context, value string) error
	// GetOrCreateNodeUUID creates and commits the node UUID when no value exists.
	GetOrCreateNodeUUID(ctx context.Context, create func() (string, error)) (value string, created bool, err error)

	GetEnrollmentTime(ctx context.Context) (value time.Time, ok bool, err error)
	SetEnrollmentTime(ctx context.Context, value time.Time) error

	GetNodeGroup(ctx context.Context) (value string, ok bool, err error)
	SetNodeGroup(ctx context.Context, value string) error

	GetComputeZone(ctx context.Context) (value string, ok bool, err error)
	SetComputeZone(ctx context.Context, value string) error

	GetNodePlacement(ctx context.Context) (nodeGroup string, nodeGroupOK bool, computeZone string, computeZoneOK bool, err error)
	SetNodePlacement(ctx context.Context, nodeGroup, computeZone string) error
}
