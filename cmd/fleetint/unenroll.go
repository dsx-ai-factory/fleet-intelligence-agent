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
	"database/sql"
	"fmt"
	"strings"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	pkgmetadata "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metadata"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/sqlite"
	"github.com/urfave/cli"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/agentstate"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
)

func unenrollCommand(c *cli.Context) error {
	log.Logger.Infow("Starting un-enrollment process")

	// Get state file path
	stateFile, err := config.DefaultStateFile()
	if err != nil {
		return fmt.Errorf("failed to get state file path: %w", err)
	}

	// Open database connection
	dbRW, err := sqlite.Open(stateFile)
	if err != nil {
		return fmt.Errorf("failed to open state database: %w", err)
	}
	defer dbRW.Close()

	// Ensure metadata table exists so unenroll is idempotent even on fresh nodes.
	if err := pkgmetadata.CreateTableMetadata(context.Background(), dbRW); err != nil {
		return fmt.Errorf("failed to create metadata table: %w", err)
	}

	// Remove enrollment metadata entries
	if err := removeEnrollmentMetadata(context.Background(), dbRW); err != nil {
		return fmt.Errorf("failed to remove enrollment metadata: %w", err)
	}
	if err := config.SecureStateFilePermissions(stateFile); err != nil {
		return fmt.Errorf("failed to secure state database permissions: %w", err)
	}

	log.Logger.Infow("Successfully un-enrolled from Fleet Intelligence backend")
	return nil
}

// removeEnrollmentMetadata removes all enrollment-related metadata from the database
func removeEnrollmentMetadata(ctx context.Context, dbRW *sql.DB) error {
	// List of metadata keys to delete
	keysToDelete := []string{
		pkgmetadata.MetadataKeyToken,
		agentstate.MetadataKeySAKToken,
		agentstate.MetadataKeyBackendBaseURL,
		agentstate.MetadataKeyEnrolledAt,
		agentstate.MetadataKeyNodeGroup,
		agentstate.MetadataKeyComputeZone,
		"enroll_endpoint",
		"metrics_endpoint",
		"logs_endpoint",
		"nonce_endpoint",
	}

	// Build batch delete query
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(keysToDelete)), ", ")
	query := fmt.Sprintf("DELETE FROM gpud_metadata WHERE key IN (%s)", placeholders)

	// Convert string slice to []interface{} for ExecContext
	args := make([]interface{}, len(keysToDelete))
	for i, key := range keysToDelete {
		args[i] = key
	}

	// Execute batch delete
	result, err := dbRW.ExecContext(ctx, query, args...)
	if err != nil {
		log.Logger.Errorw("Failed to delete enrollment metadata", "error", err)
		return fmt.Errorf("failed to delete enrollment metadata: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	log.Logger.Infow("Deleted enrollment metadata", "rows_deleted", rowsAffected)

	return nil
}
