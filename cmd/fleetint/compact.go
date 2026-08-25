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
	"net"
	"os"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/netutil"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/sqlite"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/systemd"
	"github.com/dustin/go-humanize"
	"github.com/urfave/cli"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/cmdutil"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
)

func compactCommand(cliContext *cli.Context) error {
	logLevel := cliContext.String("log-level")
	zapLvl, err := log.ParseLogLevel(logLevel)
	if err != nil {
		return err
	}
	log.Logger = log.CreateLogger(zapLvl, "")

	log.Logger.Debugw("starting compact command")

	if systemd.SystemctlExists() {
		active, err := systemd.IsActive("fleetintd.service")
		if err != nil {
			return err
		}
		if active {
			return fmt.Errorf("fleetintd service is running (must be stopped before running compact)")
		}
	}

	// Check both transports: the daemon may have been started with the default
	// unix socket or with an explicit --listen-address on TCP. Either would
	// make it unsafe to compact the database.
	if conn, err := net.DialTimeout("unix", config.DefaultUnixSocketPath, 2*time.Second); err == nil {
		conn.Close()
		return fmt.Errorf("fleetint is running on socket %s (must be stopped before running compact)", config.DefaultUnixSocketPath)
	}
	if netutil.IsPortOpen(config.DefaultHealthPort) {
		return fmt.Errorf("fleetint is running on port %d (must be stopped before running compact)", config.DefaultHealthPort)
	}

	log.Logger.Infow("successfully checked fleetintd is not running")

	rootCtx, rootCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer rootCancel()

	stateFile, err := config.DefaultStateFile()
	if err != nil {
		return fmt.Errorf("failed to get state file: %w", err)
	}

	// Check if we have write permission to the state file
	if _, err := os.OpenFile(stateFile, os.O_WRONLY, 0); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("insufficient permissions to write to state file %s. Please run with sudo", stateFile)
		}
		// If it's not a permission error, continue - the file might not exist yet or have other issues
		// that will be handled by the sqlite.Open call below
	}

	dbRW, err := sqlite.Open(stateFile)
	if err != nil {
		return fmt.Errorf("failed to open state file: %w", err)
	}
	defer dbRW.Close()

	dbRO, err := sqlite.Open(stateFile, sqlite.WithReadOnly(true))
	if err != nil {
		return fmt.Errorf("failed to open state file: %w", err)
	}
	defer dbRO.Close()

	dbSize, err := sqlite.ReadDBSize(rootCtx, dbRO)
	if err != nil {
		return fmt.Errorf("failed to read state file size: %w", err)
	}
	log.Logger.Infow("state file size before compact", "size", humanize.Bytes(dbSize))

	if err := sqlite.Compact(rootCtx, dbRW); err != nil {
		return fmt.Errorf("failed to compact state file: %w", err)
	}
	if err := config.SecureStateFilePermissions(stateFile); err != nil {
		return fmt.Errorf("failed to secure state file permissions: %w", err)
	}

	dbSize, err = sqlite.ReadDBSize(rootCtx, dbRO)
	if err != nil {
		return fmt.Errorf("failed to read state file size: %w", err)
	}
	log.Logger.Infow("state file size after compact", "size", humanize.Bytes(dbSize))

	fmt.Printf("%s successfully compacted state file\n", cmdutil.CheckMark)
	return nil
}
