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
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/urfave/cli"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/enrollment"
)

var (
	performEnrollWorkflow = enrollment.EnrollWithConfigAndMetadata
	fleetintEnvFilePath   = config.DefaultEnvFilePath
)

const defaultEnrollTimeout = time.Minute
const reservedUnassignedName = "Unassigned"

var enrollMetadataNamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9 ._-]*$`)

// resolveToken returns the SAK token from --token, --token-file, or stdin.
func resolveToken(cliContext *cli.Context) (string, error) {
	token := strings.TrimSpace(cliContext.String("token"))
	tokenFile := cliContext.String("token-file")

	if token != "" && tokenFile != "" {
		return "", fmt.Errorf("--token and --token-file are mutually exclusive")
	}

	if tokenFile != "" {
		const maxTokenSize = 1 << 20
		var raw []byte
		var err error
		if tokenFile == "-" {
			raw, err = io.ReadAll(io.LimitReader(os.Stdin, maxTokenSize))
		} else {
			var file *os.File
			file, err = os.Open(tokenFile)
			if err == nil {
				defer file.Close()
				var info os.FileInfo
				info, err = file.Stat()
				if err == nil && info.Size() >= maxTokenSize {
					return "", fmt.Errorf("token file %q exceeds maximum size of %d bytes", tokenFile, maxTokenSize)
				}
				if err == nil {
					raw, err = io.ReadAll(io.LimitReader(file, maxTokenSize))
				}
			}
		}
		if err != nil {
			return "", fmt.Errorf("failed to read token from %q: %w", tokenFile, err)
		}
		if len(raw) >= maxTokenSize {
			return "", fmt.Errorf("token file %q exceeds maximum size of %d bytes", tokenFile, maxTokenSize)
		}
		token = strings.TrimSpace(string(raw))
	}

	if token == "" {
		return "", fmt.Errorf("a token is required: use --token <value> or --token-file <path>")
	}
	return token, nil
}

func enrollCommand(cliContext *cli.Context) error {
	baseEndpoint := cliContext.String("endpoint")
	force := cliContext.Bool("force")
	nodeGroup, err := validatedOptionalMetadataFlagValue(cliContext, "node-group", "Node group")
	if err != nil {
		return err
	}
	computeZone, err := validatedOptionalMetadataFlagValue(cliContext, "compute-zone", "Compute zone")
	if err != nil {
		return err
	}
	metadata := &enrollment.EnrollMetadata{
		NodeGroup:   nodeGroup,
		ComputeZone: computeZone,
	}

	sakToken, err := resolveToken(cliContext)
	if err != nil {
		return err
	}

	result, err := runPrecheck()
	if err != nil {
		return fmt.Errorf("failed to run precheck: %w", err)
	}
	printPrecheckResult(writerFromContext(cliContext), result)
	if !result.Passed() {
		if !force {
			fmt.Fprintln(writerFromContext(cliContext), "Enrollment skipped: precheck failed")
			return fmt.Errorf("precheck failed")
		}
		fmt.Fprintln(writerFromContext(cliContext), "Proceeding with enrollment because --force was provided")
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultEnrollTimeout)
	defer cancel()

	cfg, err := config.Default(ctx)
	if err != nil {
		return fmt.Errorf("failed to load default config: %w", err)
	}
	if err := config.LoadEnvFileDefaults(fleetintEnvFilePath); err != nil {
		return err
	}
	if err := configureLoopConfigFromEnv(cfg); err != nil {
		return fmt.Errorf("failed to configure loop settings from environment variables: %w", err)
	}

	return performEnrollWorkflow(ctx, baseEndpoint, sakToken, cfg, metadata)
}

func validatedOptionalMetadataFlagValue(cliContext *cli.Context, name, fieldName string) (*string, error) {
	if !cliContext.IsSet(name) {
		return nil, nil
	}
	value := strings.TrimSpace(cliContext.String(name))
	// Explicit empty means clear/unassign for reserved assignment fields.
	if value == "" {
		return &value, nil
	}
	if strings.EqualFold(value, reservedUnassignedName) {
		return nil, fmt.Errorf("%s name %q is reserved; use empty value to clear assignment", fieldName, reservedUnassignedName)
	}
	if len(value) > 255 {
		return nil, fmt.Errorf("%s name must be 255 characters or fewer", fieldName)
	}
	if !enrollMetadataNamePattern.MatchString(value) {
		return nil, fmt.Errorf("%s name must start with a letter and contain only letters, numbers, spaces, hyphens, underscores, or periods", fieldName)
	}
	return &value, nil
}
