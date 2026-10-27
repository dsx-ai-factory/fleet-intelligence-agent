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

// Package agentidentity initializes and persists the stable agent UUID used by
// enrollment, inventory, attestation, and metrics workflows.
// The persisted agent UUID is stored in the legacy machine_id metadata key.
// It is separate from the backend canonical nodeUUID and the OS machine ID.
package agentidentity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	pkghost "github.com/NVIDIA/fleet-intelligence-sdk/pkg/host"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
)

// State is the local storage contract needed to persist agent identity.
type State interface {
	GetOrCreateAgentUUID(ctx context.Context, create func() (string, error)) (value string, created bool, err error)
}

var (
	readHardwareUUID = func() (string, error) { return agentUUIDFromSystemUUID(pkghost.SystemUUID()) }
	newAgentUUID     = uuid.NewString
)

// EnsureAgentUUID returns the persisted agent UUID, or initializes and persists it
// from the reported system UUID with a random UUID fallback.
func EnsureAgentUUID(ctx context.Context, state State) (string, error) {
	if state == nil {
		return "", errors.New("agent identity state is required")
	}

	agentUUID, created, err := state.GetOrCreateAgentUUID(ctx, func() (string, error) {
		agentUUID, err := readHardwareUUID()
		if err != nil || agentUUID == "" {
			agentUUID = newAgentUUID()
			log.Logger.Warnw("Failed to get hardware UUID, generated random agent ID",
				"error", err,
				"generated_id", agentUUID)
		} else {
			log.Logger.Infow("Initialized agent ID from hardware UUID", "agent_uuid", agentUUID)
		}
		return agentUUID, nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to ensure agent UUID: %w", err)
	}
	if created {
		log.Logger.Infow("Persisted agent ID to database", "agent_uuid", agentUUID)
	} else {
		log.Logger.Infow("Using persisted agent ID from database", "agent_uuid", agentUUID)
	}
	return agentUUID, nil
}

// agentUUIDFromSystemUUID accepts the system UUID reported in inventory as the
// agent UUID unless it is missing or an all-zero/all-f placeholder.
func agentUUIDFromSystemUUID(systemUUID string) (string, error) {
	if systemUUID == "" {
		return "", errors.New("system UUID not found")
	}
	parsedUUID, err := uuid.Parse(systemUUID)
	if err != nil {
		return "", fmt.Errorf("invalid system UUID %q: %w", systemUUID, err)
	}
	if parsedUUID == uuid.Nil || parsedUUID == uuid.Max {
		return "", fmt.Errorf("system UUID %q is a placeholder", systemUUID)
	}
	return systemUUID, nil
}
