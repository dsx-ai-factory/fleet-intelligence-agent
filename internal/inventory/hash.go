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

package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// ComputeHash returns a deterministic hash for the stable inventory contents.
func ComputeHash(snap *Snapshot) (string, error) {
	if snap == nil {
		return "", fmt.Errorf("inventory snapshot is nil")
	}
	normalized := *snap
	normalized.CollectedAt = time.Time{}
	normalized.InventoryHash = ""
	normalized.AgentConfig.EnabledComponents = sortedStrings(snap.AgentConfig.EnabledComponents)
	normalized.AgentConfig.DisabledComponents = sortedStrings(snap.AgentConfig.DisabledComponents)

	var err error
	normalized.Resources.GPUInfo.GPUs, err = sortedByCanonicalJSON(snap.Resources.GPUInfo.GPUs)
	if err != nil {
		return "", fmt.Errorf("canonicalize GPU inventory: %w", err)
	}
	normalized.Resources.DiskInfo.BlockDevices, err = sortedByCanonicalJSON(snap.Resources.DiskInfo.BlockDevices)
	if err != nil {
		return "", fmt.Errorf("canonicalize disk inventory: %w", err)
	}
	normalized.Resources.NICInfo.PrivateIPInterfaces, err = sortedByCanonicalJSON(snap.Resources.NICInfo.PrivateIPInterfaces)
	if err != nil {
		return "", fmt.Errorf("canonicalize NIC inventory: %w", err)
	}

	payload, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshal inventory snapshot: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func sortedStrings(values []string) []string {
	if values == nil {
		return nil
	}
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	return sorted
}

// sortedByCanonicalJSON treats a slice as order-insensitive for hashing purposes.
// Comparing the complete serialized values avoids relying on optional identity
// fields and automatically includes fields added to an inventory item in the future.
func sortedByCanonicalJSON[T any](values []T) ([]T, error) {
	if values == nil {
		return nil, nil
	}

	type sortableValue struct {
		value   T
		encoded string
	}
	items := make([]sortableValue, len(values))
	for i, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal inventory list item: %w", err)
		}
		items[i] = sortableValue{value: value, encoded: string(encoded)}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].encoded < items[j].encoded
	})

	sorted := make([]T, len(items))
	for i := range items {
		sorted[i] = items[i].value
	}
	return sorted, nil
}
