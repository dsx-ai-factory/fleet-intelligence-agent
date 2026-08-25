// SPDX-FileCopyrightText: Copyright (c) 2025, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
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

// Package enrollment provides shared enrollment functionality for the Fleet Intelligence agent.
package enrollment

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	pkgmetadata "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metadata"
	nvidianvml "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/nvml"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/sqlite"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/agentstate"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/backendclient"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/endpoint"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/inventory"
	inventorysink "github.com/dsx-ai-factory/fleet-intelligence-agent/internal/inventory/sink"
	inventorysource "github.com/dsx-ai-factory/fleet-intelligence-agent/internal/inventory/source"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/machineinfo"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/nodeidentity"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/registry"
)

var (
	newBackendClient               = backendclient.New
	syncInventoryAfterEnroll       = syncInventoryOnce
	ensureNodeUUID                 = nodeidentity.EnsureNodeUUID
	storeEnrollmentConfig          = storeConfigInMetadata
	postEnrollInventorySyncTimeout = time.Minute
)

// EnrollMetadata contains optional enrollment metadata values persisted for runtime use.
type EnrollMetadata struct {
	NodeGroup   *string
	ComputeZone *string
}

// Enroll runs the full enrollment workflow and performs a best-effort initial inventory sync.
func Enroll(ctx context.Context, baseEndpoint, sakToken string) error {
	return EnrollWithConfig(ctx, baseEndpoint, sakToken, nil)
}

// EnrollWithConfig runs the full enrollment workflow and uses cfg for best-effort inventory metadata.
func EnrollWithConfig(ctx context.Context, baseEndpoint, sakToken string, cfg *config.Config) error {
	return EnrollWithConfigAndMetadata(ctx, baseEndpoint, sakToken, cfg, nil)
}

// EnrollWithConfigAndMetadata runs the full enrollment workflow and persists optional metadata values.
func EnrollWithConfigAndMetadata(ctx context.Context, baseEndpoint, sakToken string, cfg *config.Config, metadata *EnrollMetadata) error {
	baseURL, err := normalizeBackendBaseURL(baseEndpoint)
	if err != nil {
		return fmt.Errorf("invalid enrollment endpoint: %w", err)
	}

	client, err := newBackendClient(baseURL.String())
	if err != nil {
		return fmt.Errorf("failed to create backend client: %w", err)
	}
	jwtToken, err := client.Enroll(ctx, sakToken)
	if err != nil {
		return err
	}
	if _, err := ensureNodeUUID(ctx, agentstate.NewSQLite()); err != nil {
		return fmt.Errorf("failed to initialize node UUID: %w", err)
	}
	enrolledAt := time.Now().UTC()
	if err := storeEnrollmentConfig(ctx, baseURL.String(), jwtToken, sakToken, enrolledAt, normalizedEnrollMetadata(metadata)); err != nil {
		return fmt.Errorf("failed to store configuration: %w", err)
	}
	syncCtx, cancel := context.WithTimeout(ctx, postEnrollInventorySyncTimeout)
	defer cancel()
	if err := runWithContext(syncCtx, func() error {
		return syncInventoryAfterEnroll(syncCtx, cfg)
	}); err != nil {
		log.Logger.Warnw("post-enroll inventory sync failed", "error", err)
	}
	return nil
}

func runWithContext(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() {
		done <- fn()
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func normalizeBackendBaseURL(raw string) (*url.URL, error) {
	baseURL, err := endpoint.ValidateBackendEndpoint(raw)
	if err != nil {
		return nil, err
	}
	if baseURL.Path == "" || baseURL.Path == "/" {
		return baseURL, nil
	}

	normalized, err := endpoint.DeriveBackendBaseURL(raw)
	if err != nil {
		return nil, err
	}
	return endpoint.ValidateBackendEndpoint(normalized)
}

func normalizedEnrollMetadata(metadata *EnrollMetadata) EnrollMetadata {
	if metadata == nil {
		return EnrollMetadata{}
	}
	return EnrollMetadata{
		NodeGroup:   trimmedOptionalString(metadata.NodeGroup),
		ComputeZone: trimmedOptionalString(metadata.ComputeZone),
	}
}

func trimmedOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func storeConfigInMetadata(ctx context.Context, baseURL, jwtToken, sakToken string, enrolledAt time.Time, metadata EnrollMetadata) error {
	stateFile, err := config.DefaultStateFile()
	if err != nil {
		return fmt.Errorf("failed to get state file path: %w", err)
	}

	dbRW, err := sqlite.Open(stateFile)
	if err != nil {
		return fmt.Errorf("failed to open state database: %w", err)
	}
	defer dbRW.Close()

	if err := config.SecureStateFilePermissions(stateFile); err != nil {
		return fmt.Errorf("failed to secure state database permissions: %w", err)
	}
	if err := pkgmetadata.CreateTableMetadata(ctx, dbRW); err != nil {
		return fmt.Errorf("failed to create metadata table: %w", err)
	}

	if err := pkgmetadata.SetMetadata(ctx, dbRW, agentstate.MetadataKeySAKToken, sakToken); err != nil {
		return fmt.Errorf("failed to set SAK token: %w", err)
	}
	if err := pkgmetadata.SetMetadata(ctx, dbRW, pkgmetadata.MetadataKeyToken, jwtToken); err != nil {
		return fmt.Errorf("failed to set JWT token: %w", err)
	}
	if err := pkgmetadata.SetMetadata(ctx, dbRW, agentstate.MetadataKeyBackendBaseURL, baseURL); err != nil {
		return fmt.Errorf("failed to set backend base URL: %w", err)
	}
	if err := pkgmetadata.SetMetadata(ctx, dbRW, agentstate.MetadataKeyEnrolledAt, enrolledAt.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("failed to set enrollment time: %w", err)
	}
	if err := agentstate.UpdateNodePlacementMetadata(ctx, dbRW, metadata.NodeGroup, metadata.ComputeZone); err != nil {
		return fmt.Errorf("failed to update node placement: %w", err)
	}
	return nil
}

type machineInfoCollectorFunc func(context.Context) (*machineinfo.MachineInfo, error)

func (f machineInfoCollectorFunc) Collect(ctx context.Context) (*machineinfo.MachineInfo, error) {
	return f(ctx)
}

func syncInventoryOnce(ctx context.Context, cfg *config.Config) error {
	state := agentstate.NewSQLite()
	sink := inventorysink.NewBackendSink(state)
	allComponents := registry.AllComponentNames()

	if cfg == nil {
		var err error
		cfg, err = config.Default(ctx)
		if err != nil {
			return fmt.Errorf("load default config for inventory sync: %w", err)
		}
	}
	retentionPeriodSeconds, enabledComponents, disabledComponents := cfg.InventoryAgentConfig(allComponents)
	inventoryEnabled, inventoryIntervalSeconds := cfg.InventoryLoopAgentConfig()
	attestationEnabled, attestationIntervalSeconds := cfg.AttestationLoopAgentConfig()

	nvmlInstance, err := nvidianvml.New()
	if err != nil {
		return fmt.Errorf("initialize nvml for inventory sync: %w", err)
	}
	defer func() { _ = nvmlInstance.Shutdown() }()

	src := inventorysource.NewMachineInfoSourceWithAgentConfig(
		machineInfoCollectorFunc(func(context.Context) (*machineinfo.MachineInfo, error) {
			return machineinfo.GetMachineInfo(nvmlInstance)
		}),
		&inventory.AgentConfig{
			TotalComponents:             int64(len(allComponents)),
			RetentionPeriodSeconds:      retentionPeriodSeconds,
			MetricScrapeIntervalSeconds: cfg.MetricScrapeIntervalSeconds(),
			EnabledComponents:           enabledComponents,
			DisabledComponents:          disabledComponents,
			InventoryEnabled:            inventoryEnabled,
			InventoryIntervalSeconds:    inventoryIntervalSeconds,
			AttestationEnabled:          attestationEnabled,
			AttestationIntervalSeconds:  attestationIntervalSeconds,
		},
	)
	manager := inventory.NewManager(src, sink, inventory.InventoryConfig{})
	_, err = manager.CollectOnce(ctx)
	return err
}
