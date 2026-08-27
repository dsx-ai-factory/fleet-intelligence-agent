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
