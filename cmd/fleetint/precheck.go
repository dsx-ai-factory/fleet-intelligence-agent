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
	"fmt"
	"io"
	"os"

	"github.com/urfave/cli"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/cmdutil"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/precheck"
)

var runPrecheck = precheck.Run

func precheckCommand(cliContext *cli.Context) error {
	result, err := runPrecheck()
	if err != nil {
		return fmt.Errorf("failed to run precheck: %w", err)
	}

	printPrecheckResult(writerFromContext(cliContext), result)
	if !result.Passed() {
		return fmt.Errorf("precheck failed")
	}

	return nil
}

func printPrecheckResult(w io.Writer, result precheck.Result) {
	for _, check := range result.Checks {
		symbol := cmdutil.WarningSign
		if check.Passed {
			symbol = cmdutil.CheckMark
		}
		fmt.Fprintf(w, "%s %s\n", symbol, check.Message)
	}
}

func writerFromContext(cliContext *cli.Context) io.Writer {
	if cliContext != nil && cliContext.App != nil && cliContext.App.Writer != nil {
		return cliContext.App.Writer
	}

	return os.Stdout
}
