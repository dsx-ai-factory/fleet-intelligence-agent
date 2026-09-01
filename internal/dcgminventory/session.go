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

// Package dcgminventory owns the short-lived DCGM resources used to collect
// machine inventory outside the long-running daemon.
package dcgminventory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	pkgmachineinfo "github.com/NVIDIA/fleet-intelligence-sdk/pkg/machine-info"
	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
)

// Session owns a non-reconnecting DCGM instance and its machine-inventory
// field cache. Call Close when the one-shot inventory operation finishes.
type Session struct {
	Instance   nvidiadcgm.Instance
	FieldCache *nvidiadcgm.FieldValueCache

	closeOnce sync.Once
	closeErr  error
}

// Open initializes and polls the DCGM fields required by machine inventory.
// Field watch and poll failures degrade to static device inventory, matching
// the daemon's inventory behavior when live DCGM fields are unavailable.
func Open(ctx context.Context, owner string, pollInterval time.Duration) (*Session, error) {
	groupNames := components.NewDCGMGroupNames(owner)
	dcgmInstance, err := nvidiadcgm.NewOnceWithContextAndGroupName(ctx, groupNames.HealthMonitoringGroup)
	if err != nil {
		return nil, fmt.Errorf("initialize DCGM for machine inventory: %w", err)
	}

	if err := pkgmachineinfo.RegisterDCGMFields(dcgmInstance); err != nil {
		_ = dcgmInstance.Shutdown()
		return nil, fmt.Errorf("register DCGM machine inventory fields: %w", err)
	}

	fieldCache := nvidiadcgm.NewFieldValueCache(ctx, dcgmInstance, pollInterval)
	session := &Session{
		Instance:   dcgmInstance,
		FieldCache: fieldCache,
	}

	if err := fieldCache.SetupFieldWatchingWithName(groupNames.GPUFieldGroup); err != nil {
		log.Logger.Warnw("DCGM inventory field setup failed; continuing with static inventory", "error", err)
	}
	if err := fieldCache.Poll(); err != nil {
		log.Logger.Warnw("initial DCGM inventory poll failed; continuing with static inventory", "error", err)
	}

	return session, nil
}

// Close releases the field cache before shutting down its DCGM instance.
// It is safe to call more than once.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}

	s.closeOnce.Do(func() {
		if s.FieldCache != nil {
			s.FieldCache.Stop()
		}
		if s.Instance != nil {
			s.closeErr = s.Instance.Shutdown()
		}
	})
	return s.closeErr
}
