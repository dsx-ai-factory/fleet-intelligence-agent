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
	flagpkg "flag"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli"
)

func TestLogLevelDefaultsToWarn(t *testing.T) {
	var commandsWithLogLevel []string

	for _, command := range App().Commands {
		for _, flag := range command.Flags {
			logLevelFlag, ok := flag.(*cli.StringFlag)
			if !ok || logLevelFlag.Name != "log-level,l" {
				continue
			}

			commandsWithLogLevel = append(commandsWithLogLevel, command.Name)
			require.Equal(t, "warn", logLevelFlag.Value, "command %q", command.Name)
		}
	}

	require.ElementsMatch(t, []string{"scan", "run", "status", "machine-info", "metadata", "compact"}, commandsWithLogLevel)
}

func TestHPCJobMappingDirConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		fleetintEnv string
		dcgmEnv     string
		arguments   []string
		want        string
	}{
		{
			name: "disabled by default",
			want: "",
		},
		{
			name:    "DCGM environment variable is ignored",
			dcgmEnv: "/dcgm/job-mapping",
			want:    "",
		},
		{
			name:        "FleetInt environment variable enables mapping",
			fleetintEnv: "/fleetint/job-mapping",
			dcgmEnv:     "/dcgm/job-mapping",
			want:        "/fleetint/job-mapping",
		},
		{
			name:        "command line flag takes precedence",
			fleetintEnv: "/fleetint/job-mapping",
			dcgmEnv:     "/dcgm/job-mapping",
			arguments:   []string{"--hpc-job-mapping-dir=/flag/job-mapping"},
			want:        "/flag/job-mapping",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setOptionalEnv(t, "FLEETINT_HPC_JOB_MAPPING_DIR", test.fleetintEnv)
			setOptionalEnv(t, "DCGM_HPC_JOB_MAPPING_DIR", test.dcgmEnv)

			mappingFlag := hpcJobMappingDirFlag(t)
			flagSet := flagpkg.NewFlagSet("test", flagpkg.ContinueOnError)
			require.NoError(t, mappingFlag.ApplyWithError(flagSet))
			require.NoError(t, flagSet.Parse(test.arguments))
			require.Equal(t, test.want, flagSet.Lookup("hpc-job-mapping-dir").Value.String())
		})
	}
}

func TestWorkloadAttributionSourceConfiguration(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		setOptionalEnv(t, "FLEETINT_WORKLOAD_ATTRIBUTION_SOURCE", "")
		flag := workloadAttributionSourceFlag(t)
		flagSet := flagpkg.NewFlagSet("test", flagpkg.ContinueOnError)
		require.NoError(t, flag.ApplyWithError(flagSet))
		require.NoError(t, flagSet.Parse(nil))
		require.Empty(t, flagSet.Lookup("workload-attribution-source").Value.String())
	})

	t.Run("FleetInt environment variable selects HPC", func(t *testing.T) {
		setOptionalEnv(t, "FLEETINT_WORKLOAD_ATTRIBUTION_SOURCE", "hpc")
		flag := workloadAttributionSourceFlag(t)
		flagSet := flagpkg.NewFlagSet("test", flagpkg.ContinueOnError)
		require.NoError(t, flag.ApplyWithError(flagSet))
		require.NoError(t, flagSet.Parse(nil))
		require.Equal(t, "hpc", flagSet.Lookup("workload-attribution-source").Value.String())
	})
}

func TestHPCGPUIdentifierConfiguration(t *testing.T) {
	t.Run("defaults to DCGM index", func(t *testing.T) {
		setOptionalEnv(t, "FLEETINT_HPC_GPU_IDENTIFIER", "")
		flag := stringFlag(t, "hpc-gpu-identifier")
		flagSet := flagpkg.NewFlagSet("test", flagpkg.ContinueOnError)
		require.NoError(t, flag.ApplyWithError(flagSet))
		require.NoError(t, flagSet.Parse(nil))
		require.Equal(t, "dcgm_index", flagSet.Lookup("hpc-gpu-identifier").Value.String())
	})

	t.Run("FleetInt environment variable selects UUID", func(t *testing.T) {
		setOptionalEnv(t, "FLEETINT_HPC_GPU_IDENTIFIER", "uuid")
		flag := stringFlag(t, "hpc-gpu-identifier")
		flagSet := flagpkg.NewFlagSet("test", flagpkg.ContinueOnError)
		require.NoError(t, flag.ApplyWithError(flagSet))
		require.NoError(t, flagSet.Parse(nil))
		require.Equal(t, "uuid", flagSet.Lookup("hpc-gpu-identifier").Value.String())
	})
}

func hpcJobMappingDirFlag(t *testing.T) *cli.StringFlag {
	t.Helper()

	for _, command := range App().Commands {
		if command.Name != "run" {
			continue
		}
		for _, commandFlag := range command.Flags {
			stringFlag, ok := commandFlag.(*cli.StringFlag)
			if ok && stringFlag.Name == "hpc-job-mapping-dir" {
				return stringFlag
			}
		}
	}

	t.Fatal("run command does not define --hpc-job-mapping-dir")
	return nil
}

func workloadAttributionSourceFlag(t *testing.T) *cli.StringFlag {
	return stringFlag(t, "workload-attribution-source")
}

func stringFlag(t *testing.T, name string) *cli.StringFlag {
	t.Helper()

	for _, command := range App().Commands {
		if command.Name != "run" {
			continue
		}
		for _, commandFlag := range command.Flags {
			stringFlag, ok := commandFlag.(*cli.StringFlag)
			if ok && stringFlag.Name == name {
				return stringFlag
			}
		}
	}

	t.Fatalf("run command does not define --%s", name)
	return nil
}

func setOptionalEnv(t *testing.T, name, value string) {
	t.Helper()

	previous, wasSet := os.LookupEnv(name)
	require.NoError(t, os.Unsetenv(name))
	t.Cleanup(func() {
		if wasSet {
			require.NoError(t, os.Setenv(name, previous))
			return
		}
		require.NoError(t, os.Unsetenv(name))
	})

	if value != "" {
		require.NoError(t, os.Setenv(name, value))
	}
}
