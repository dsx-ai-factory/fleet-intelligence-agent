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
	"fmt"
	"os"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	pkgmetadata "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metadata"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/netutil"
	nvidianvml "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/nvml"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/sqlite"
	"github.com/urfave/cli"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/cmdutil"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/machineinfo"
)

func machineInfoCommand(cliContext *cli.Context) error {
	logLevel := cliContext.String("log-level")
	zapLvl, err := log.ParseLogLevel(logLevel)
	if err != nil {
		return err
	}
	log.Logger = log.CreateLogger(zapLvl, "")

	log.Logger.Debugw("starting machine-info command")

	stateFile, err := config.DefaultStateFile()
	if err != nil {
		return fmt.Errorf("failed to get state file: %w", err)
	}

	// only read the state file if it exists (existing fleetint login)
	if _, err := os.Stat(stateFile); err == nil {
		// Check if we have read permission to the state file
		if _, err := os.Open(stateFile); err != nil {
			if os.IsPermission(err) {
				return fmt.Errorf("insufficient permissions to read state file %s. Please run with sudo", stateFile)
			}
			// If it's not a permission error, continue - the sqlite.Open call below will handle other issues.
		}

		dbRO, err := sqlite.Open(stateFile, sqlite.WithReadOnly(true))
		if err != nil {
			return fmt.Errorf("failed to open state file: %w", err)
		}
		defer dbRO.Close()

		rootCtx, rootCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer rootCancel()
		machineID, err := pkgmetadata.ReadMachineID(rootCtx, dbRO)
		if err != nil {
			return err
		}

		fmt.Printf("Fleetint machine ID: %q\n\n", machineID)
	}

	nvmlInstance, err := nvidianvml.New()
	if err != nil {
		return err
	}

	machineInfo, err := machineinfo.GetMachineInfo(nvmlInstance)
	if err != nil {
		return err
	}
	machineInfo.RenderTable(os.Stdout)

	pubIP, _ := netutil.PublicIP()
	providerInfo := machineinfo.GetProvider(pubIP)
	if providerInfo == nil {
		fmt.Printf("%s failed to find provider (%v)\n", cmdutil.WarningSign, err)
	} else {
		machineinfo.PopulatePrivateIPFromMachineInfo(providerInfo, machineInfo)
		fmt.Printf("%s successfully found provider %s\n", cmdutil.CheckMark, providerInfo.Provider)
		providerInfo.RenderTable(os.Stdout)
	}

	return nil
}
