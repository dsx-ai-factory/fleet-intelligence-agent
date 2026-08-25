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

package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/urfave/cli"
	"go.uber.org/zap"

	nvidiainfiniband "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/infiniband"
	infinibandtypes "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/infiniband/types"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/scan"
)

func scanCreateCommand() func(*cli.Context) error {
	return func(cliContext *cli.Context) error {
		return cmdScan(
			cliContext.String("log-level"),
			cliContext.String("infiniband-expected-port-states"),
			cliContext.String("infiniband-class-root-dir"),
		)
	}
}

func cmdScan(logLevel string, infinibandExpectedPortStates string, ibClassRootDir string) error {
	zapLvl, err := log.ParseLogLevel(logLevel)
	if err != nil {
		return err
	}
	log.Logger = log.CreateLogger(zapLvl, "")

	log.Logger.Debugw("starting scan command")

	if len(infinibandExpectedPortStates) > 0 {
		var expectedPortStates infinibandtypes.ExpectedPortStates
		if err := json.Unmarshal([]byte(infinibandExpectedPortStates), &expectedPortStates); err != nil {
			return err
		}
		nvidiainfiniband.SetDefaultExpectedPortStates(expectedPortStates)

		log.Logger.Infow("set infiniband expected port states", "infinibandExpectedPortStates", infinibandExpectedPortStates)
	}

	opts := []scan.Option{
		scan.WithInfinibandClassRootDir(ibClassRootDir),
	}
	if zapLvl.Level() <= zap.DebugLevel { // e.g., info, warn, error
		opts = append(opts, scan.WithDebug(true))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err = scan.Scan(ctx, opts...); err != nil {
		return err
	}

	return nil
}
