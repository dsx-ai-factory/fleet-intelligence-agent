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
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/sqlite"
	"github.com/urfave/cli"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/cmdutil"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/config"
)

func metadataCommand(cliContext *cli.Context) error {
	logLevel := cliContext.String("log-level")
	zapLvl, err := log.ParseLogLevel(logLevel)
	if err != nil {
		return err
	}
	log.Logger = log.CreateLogger(zapLvl, "")

	log.Logger.Debugw("starting metadata command")

	rootCtx, rootCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer rootCancel()

	log.Logger.Debugw("getting state file")
	stateFile, err := config.DefaultStateFile()
	if err != nil {
		return fmt.Errorf("failed to get state file: %w", err)
	}
	log.Logger.Debugw("successfully got state file")

	// Check if we have read permission to the state file
	if _, err := os.Open(stateFile); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("insufficient permissions to read state file %s. Please run with sudo", stateFile)
		}
		// If it's not a permission error, continue - the sqlite.Open call below will handle other issues
	}

	log.Logger.Debugw("opening state file for reading")
	dbRO, err := sqlite.Open(stateFile, sqlite.WithReadOnly(true))
	if err != nil {
		return fmt.Errorf("failed to open state file: %w", err)
	}
	defer dbRO.Close()
	log.Logger.Debugw("successfully opened state file for reading")

	metadata, err := pkgmetadata.ReadAllMetadata(rootCtx, dbRO)
	if err != nil {
		return fmt.Errorf("failed to read metadata: %w", err)
	}
	log.Logger.Debugw("successfully read metadata")

	for k, v := range metadata {
		fmt.Printf("%s: %s\n", k, maskMetadataValue(k, v))
	}

	setKey := cliContext.String("set-key")
	setValue := cliContext.String("set-value")
	if setKey == "" || setValue == "" { // no update/insert needed
		return nil
	}

	// Check if we have write permission to the state file when setting metadata
	if _, err := os.OpenFile(stateFile, os.O_WRONLY, 0); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("insufficient permissions to write to state file %s. Please run with sudo", stateFile)
		}
		// If it's not a permission error, continue - the sqlite.Open call below will handle other issues
	}

	log.Logger.Debugw("opening state file for writing")
	dbRW, err := sqlite.Open(stateFile)
	if err != nil {
		return fmt.Errorf("failed to open state file: %w", err)
	}
	defer dbRW.Close()
	log.Logger.Debugw("successfully opened state file for writing")

	log.Logger.Debugw("deleting metadata data")
	if err := pkgmetadata.SetMetadata(rootCtx, dbRW, setKey, setValue); err != nil {
		return fmt.Errorf("failed to update metadata: %w", err)
	}
	if err := config.SecureStateFilePermissions(stateFile); err != nil {
		return fmt.Errorf("failed to secure state file permissions: %w", err)
	}
	log.Logger.Debugw("successfully updated metadata")

	fmt.Printf("%s successfully updated metadata\n", cmdutil.CheckMark)
	return nil
}

func maskMetadataValue(key, value string) string {
	if key == pkgmetadata.MetadataKeyToken || key == "sak_token" {
		return pkgmetadata.MaskToken(value)
	}
	return value
}
