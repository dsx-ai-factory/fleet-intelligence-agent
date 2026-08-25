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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/precheck"
)

func TestAppIncludesPrecheckCommand(t *testing.T) {
	t.Parallel()

	app := App()

	commandNames := make([]string, 0, len(app.Commands))
	for _, command := range app.Commands {
		commandNames = append(commandNames, command.Name)
	}

	assert.Contains(t, commandNames, "precheck")
}

func TestPrecheckCommand(t *testing.T) {
	originalRunPrecheck := runPrecheck
	t.Cleanup(func() {
		runPrecheck = originalRunPrecheck
	})

	tests := []struct {
		name      string
		result    precheck.Result
		wantError string
	}{
		{
			name: "returns success when checks pass",
			result: precheck.Result{
				Checks: []precheck.Check{
					{Name: "gpu-present", Passed: true, Message: "NVIDIA GPU detected"},
				},
			},
		},
		{
			name: "returns error when checks fail",
			result: precheck.Result{
				Checks: []precheck.Check{
					{Name: "gpu-present", Message: "no NVIDIA GPU detected"},
				},
			},
			wantError: "precheck failed",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			runPrecheck = func() (precheck.Result, error) {
				return tt.result, nil
			}

			app := App()
			app.Writer = &bytes.Buffer{}

			err := app.Run([]string{"fleetint", "precheck"})
			if tt.wantError == "" {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}
