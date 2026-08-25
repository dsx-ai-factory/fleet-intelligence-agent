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
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/enrollment"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/precheck"
)

var loopEnvKeys = []string{
	"FLEETINT_INVENTORY_ENABLED",
	"FLEETINT_INVENTORY_INTERVAL",
	"FLEETINT_ATTESTATION_ENABLED",
	"FLEETINT_ATTESTATION_INTERVAL",
}

func isolateLoopEnv(t *testing.T) {
	t.Helper()
	originals := make(map[string]string, len(loopEnvKeys))
	exists := make(map[string]bool, len(loopEnvKeys))
	for _, key := range loopEnvKeys {
		value, ok := os.LookupEnv(key)
		if ok {
			originals[key] = value
			exists[key] = true
			require.NoError(t, os.Unsetenv(key))
		}
	}
	t.Cleanup(func() {
		for _, key := range loopEnvKeys {
			if exists[key] {
				_ = os.Setenv(key, originals[key])
			} else {
				_ = os.Unsetenv(key)
			}
		}
	})
}

func useMissingFleetintEnvFile(t *testing.T) {
	t.Helper()
	isolateLoopEnv(t)
	originalPath := fleetintEnvFilePath
	fleetintEnvFilePath = filepath.Join(t.TempDir(), "missing-fleetint-env")
	t.Cleanup(func() {
		fleetintEnvFilePath = originalPath
	})
}

func TestEnrollCommandPrecheckError(t *testing.T) {
	originalRunPrecheck := runPrecheck
	t.Cleanup(func() {
		runPrecheck = originalRunPrecheck
	})

	runPrecheck = func() (precheck.Result, error) {
		return precheck.Result{}, fmt.Errorf("nvml init failed")
	}

	app := App()
	app.Writer = &bytes.Buffer{}

	err := app.Run([]string{"fleetint", "enroll", "--endpoint", "https://example.com", "--token", "token"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to run precheck")
	assert.Contains(t, err.Error(), "nvml init failed")
}

func TestEnrollCommandBlocksOnFailedPrecheck(t *testing.T) {
	originalRunPrecheck := runPrecheck
	originalEnrollWorkflow := performEnrollWorkflow
	t.Cleanup(func() {
		runPrecheck = originalRunPrecheck
		performEnrollWorkflow = originalEnrollWorkflow
	})

	enrollmentCalled := false
	runPrecheck = func() (precheck.Result, error) {
		return precheck.Result{
			Checks: []precheck.Check{
				{Name: "gpu-present", Message: "no NVIDIA GPU detected"},
			},
		}, nil
	}
	performEnrollWorkflow = func(ctx context.Context, baseEndpoint, sakToken string, cfg *config.Config, metadata *enrollment.EnrollMetadata) error {
		enrollmentCalled = true
		require.NotNil(t, metadata)
		require.Nil(t, metadata.NodeGroup)
		require.Nil(t, metadata.ComputeZone)
		return nil
	}

	out := &bytes.Buffer{}
	app := App()
	app.Writer = out

	err := app.Run([]string{"fleetint", "enroll", "--endpoint", "https://example.com", "--token", "token"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "precheck failed")
	assert.Contains(t, out.String(), "Enrollment skipped: precheck failed")
	assert.False(t, enrollmentCalled)
}

func TestEnrollCommandForceBypassesFailedPrecheck(t *testing.T) {
	useMissingFleetintEnvFile(t)

	originalRunPrecheck := runPrecheck
	originalEnrollWorkflow := performEnrollWorkflow
	t.Cleanup(func() {
		runPrecheck = originalRunPrecheck
		performEnrollWorkflow = originalEnrollWorkflow
	})

	enrollmentCalled := false
	runPrecheck = func() (precheck.Result, error) {
		return precheck.Result{
			Checks: []precheck.Check{
				{Name: "gpu-present", Message: "no NVIDIA GPU detected"},
			},
		}, nil
	}
	performEnrollWorkflow = func(ctx context.Context, baseEndpoint, sakToken string, cfg *config.Config, metadata *enrollment.EnrollMetadata) error {
		enrollmentCalled = true
		require.NotNil(t, metadata)
		require.Nil(t, metadata.NodeGroup)
		require.Nil(t, metadata.ComputeZone)
		return nil
	}

	app := App()
	app.Writer = &bytes.Buffer{}

	err := app.Run([]string{"fleetint", "enroll", "--endpoint", "https://example.com", "--token", "token", "--force"})

	require.NoError(t, err)
	assert.True(t, enrollmentCalled)
}

func TestEnrollCommandPassesTimeoutContext(t *testing.T) {
	useMissingFleetintEnvFile(t)

	originalRunPrecheck := runPrecheck
	originalEnrollWorkflow := performEnrollWorkflow
	t.Cleanup(func() {
		runPrecheck = originalRunPrecheck
		performEnrollWorkflow = originalEnrollWorkflow
	})

	runPrecheck = func() (precheck.Result, error) {
		return precheck.Result{
			Checks: []precheck.Check{
				{Name: "gpu-present", Message: "ok", Passed: true},
			},
		}, nil
	}

	performEnrollWorkflow = func(ctx context.Context, baseEndpoint, sakToken string, cfg *config.Config, metadata *enrollment.EnrollMetadata) error {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), defaultEnrollTimeout)
		require.Greater(t, time.Until(deadline), 55*time.Second)
		require.NotNil(t, metadata)
		require.Nil(t, metadata.NodeGroup)
		require.Nil(t, metadata.ComputeZone)
		return nil
	}

	app := App()
	app.Writer = &bytes.Buffer{}

	err := app.Run([]string{"fleetint", "enroll", "--endpoint", "https://example.com", "--token", "token"})
	require.NoError(t, err)
}

func TestEnrollCommandPassesEffectiveLoopConfig(t *testing.T) {
	isolateLoopEnv(t)

	originalRunPrecheck := runPrecheck
	originalEnrollWorkflow := performEnrollWorkflow
	originalEnvFilePath := fleetintEnvFilePath
	t.Cleanup(func() {
		runPrecheck = originalRunPrecheck
		performEnrollWorkflow = originalEnrollWorkflow
		fleetintEnvFilePath = originalEnvFilePath
	})

	runPrecheck = func() (precheck.Result, error) {
		return precheck.Result{
			Checks: []precheck.Check{
				{Name: "gpu-present", Message: "ok", Passed: true},
			},
		}, nil
	}
	envFilePath := filepath.Join(t.TempDir(), "fleetint.env")
	require.NoError(t, os.WriteFile(envFilePath, []byte(`
FLEETINT_INVENTORY_ENABLED="false"
FLEETINT_INVENTORY_INTERVAL="15m"
FLEETINT_ATTESTATION_ENABLED="true"
FLEETINT_ATTESTATION_INTERVAL="6h"
`), 0o600))
	fleetintEnvFilePath = envFilePath

	performEnrollWorkflow = func(ctx context.Context, baseEndpoint, sakToken string, cfg *config.Config, metadata *enrollment.EnrollMetadata) error {
		require.NotNil(t, cfg)
		require.NotNil(t, cfg.Inventory)
		require.False(t, cfg.Inventory.Enabled)
		require.Equal(t, 15*time.Minute, cfg.Inventory.Interval.Duration)
		require.NotNil(t, cfg.Attestation)
		require.True(t, cfg.Attestation.Enabled)
		require.Equal(t, 6*time.Hour, cfg.Attestation.Interval.Duration)
		require.NotNil(t, metadata)
		require.Nil(t, metadata.NodeGroup)
		require.Nil(t, metadata.ComputeZone)
		return nil
	}

	app := App()
	app.Writer = &bytes.Buffer{}

	err := app.Run([]string{"fleetint", "enroll", "--endpoint", "https://example.com", "--token", "token"})
	require.NoError(t, err)
}

func TestEnrollCommandPassesOptionalMetadata(t *testing.T) {
	useMissingFleetintEnvFile(t)

	originalRunPrecheck := runPrecheck
	originalEnrollWorkflow := performEnrollWorkflow
	t.Cleanup(func() {
		runPrecheck = originalRunPrecheck
		performEnrollWorkflow = originalEnrollWorkflow
	})

	runPrecheck = func() (precheck.Result, error) {
		return precheck.Result{
			Checks: []precheck.Check{
				{Name: "gpu-present", Message: "ok", Passed: true},
			},
		}, nil
	}

	performEnrollWorkflow = func(ctx context.Context, baseEndpoint, sakToken string, cfg *config.Config, metadata *enrollment.EnrollMetadata) error {
		require.NotNil(t, metadata)
		require.NotNil(t, metadata.NodeGroup)
		require.Equal(t, "prod-group", *metadata.NodeGroup)
		require.NotNil(t, metadata.ComputeZone)
		require.Equal(t, "us-east-1c", *metadata.ComputeZone)
		return nil
	}

	app := App()
	app.Writer = &bytes.Buffer{}

	err := app.Run([]string{
		"fleetint", "enroll",
		"--endpoint", "https://example.com",
		"--token", "token",
		"--node-group", "prod-group",
		"--compute-zone", "us-east-1c",
	})
	require.NoError(t, err)
}

func TestValidatedOptionalMetadataFlagValueAllowsExplicitEmpty(t *testing.T) {
	flagSet := flag.NewFlagSet("enroll", flag.ContinueOnError)
	flagSet.String("node-group", "", "")
	require.NoError(t, flagSet.Set("node-group", ""))
	cliContext := cli.NewContext(cli.NewApp(), flagSet, nil)

	value, err := validatedOptionalMetadataFlagValue(cliContext, "node-group", "Node group")
	require.NoError(t, err)
	require.NotNil(t, value)
	require.Empty(t, *value)
}

func TestValidatedOptionalMetadataFlagValueRejectsReservedUnassignedName(t *testing.T) {
	flagSet := flag.NewFlagSet("enroll", flag.ContinueOnError)
	flagSet.String("node-group", "", "")
	require.NoError(t, flagSet.Set("node-group", "unassigned"))
	cliContext := cli.NewContext(cli.NewApp(), flagSet, nil)

	_, err := validatedOptionalMetadataFlagValue(cliContext, "node-group", "Node group")
	require.ErrorContains(t, err, `Node group name "Unassigned" is reserved; use empty value to clear assignment`)
}

func TestEnrollCommandRejectsInvalidMetadataCharacters(t *testing.T) {
	app := App()
	app.Writer = &bytes.Buffer{}

	err := app.Run([]string{
		"fleetint", "enroll",
		"--endpoint", "https://example.com",
		"--token", "token",
		"--compute-zone", "@bad-zone",
	})
	require.ErrorContains(t, err, "Compute zone name must start with a letter")
}

func TestEnrollCommandRejectsOverlongMetadataNames(t *testing.T) {
	app := App()
	app.Writer = &bytes.Buffer{}
	longName := strings.Repeat("a", 256)

	err := app.Run([]string{
		"fleetint", "enroll",
		"--endpoint", "https://example.com",
		"--token", "token",
		"--node-group", longName,
	})
	require.ErrorContains(t, err, "Node group name must be 255 characters or fewer")
}

func TestValidatedOptionalMetadataFlagValueSupportsIndependentReservedUpdates(t *testing.T) {
	strPtr := func(v string) *string { return &v }
	tests := []struct {
		name              string
		nodeGroupArg      *string
		computeZoneArg    *string
		expectNodeGroup   *string
		expectComputeZone *string
	}{
		{
			name:              "clear compute-zone and set node-group",
			nodeGroupArg:      strPtr("xx"),
			computeZoneArg:    strPtr(""),
			expectNodeGroup:   strPtr("xx"),
			expectComputeZone: strPtr(""),
		},
		{
			name:              "set compute-zone and clear node-group",
			nodeGroupArg:      strPtr(""),
			computeZoneArg:    strPtr("xx"),
			expectNodeGroup:   strPtr(""),
			expectComputeZone: strPtr("xx"),
		},
		{
			name:              "set compute-zone and omit node-group",
			nodeGroupArg:      nil,
			computeZoneArg:    strPtr("xx"),
			expectNodeGroup:   nil,
			expectComputeZone: strPtr("xx"),
		},
		{
			name:              "set node-group and omit compute-zone",
			nodeGroupArg:      strPtr("xx"),
			computeZoneArg:    nil,
			expectNodeGroup:   strPtr("xx"),
			expectComputeZone: nil,
		},
		{
			name:              "clear compute-zone and omit node-group",
			nodeGroupArg:      nil,
			computeZoneArg:    strPtr(""),
			expectNodeGroup:   nil,
			expectComputeZone: strPtr(""),
		},
		{
			name:              "clear node-group and omit compute-zone",
			nodeGroupArg:      strPtr(""),
			computeZoneArg:    nil,
			expectNodeGroup:   strPtr(""),
			expectComputeZone: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flagSet := flag.NewFlagSet("enroll", flag.ContinueOnError)
			flagSet.String("node-group", "", "")
			flagSet.String("compute-zone", "", "")
			if tc.nodeGroupArg != nil {
				require.NoError(t, flagSet.Set("node-group", *tc.nodeGroupArg))
			}
			if tc.computeZoneArg != nil {
				require.NoError(t, flagSet.Set("compute-zone", *tc.computeZoneArg))
			}
			cliContext := cli.NewContext(cli.NewApp(), flagSet, nil)

			nodeGroup, err := validatedOptionalMetadataFlagValue(cliContext, "node-group", "Node group")
			require.NoError(t, err)
			computeZone, err := validatedOptionalMetadataFlagValue(cliContext, "compute-zone", "Compute zone")
			require.NoError(t, err)

			assert.Equal(t, tc.expectNodeGroup, nodeGroup)
			assert.Equal(t, tc.expectComputeZone, computeZone)
		})
	}
}

func TestEnrollCommandRejectsReservedUnassignedName(t *testing.T) {
	app := App()
	app.Writer = &bytes.Buffer{}

	err := app.Run([]string{
		"fleetint", "enroll",
		"--endpoint", "https://example.com",
		"--token", "token",
		"--node-group", "Unassigned",
		"--compute-zone", "cz-a",
	})
	require.ErrorContains(t, err, `Node group name "Unassigned" is reserved; use empty value to clear assignment`)
}
