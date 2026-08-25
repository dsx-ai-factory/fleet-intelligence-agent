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

package agentstate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	pkgmetadata "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metadata"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/sqlite"
	sqlite3 "github.com/mattn/go-sqlite3"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/endpoint"
)

type sqliteState struct {
	stateFileFn func() (string, error)
}

// NewSQLite returns a State backed by the agent sqlite metadata database.
func NewSQLite() State {
	return &sqliteState{stateFileFn: config.DefaultStateFile}
}

func (s *sqliteState) GetBackendBaseURL(ctx context.Context) (string, bool, error) {
	db, err := s.openReadOnly()
	if err != nil {
		return "", false, err
	}
	defer db.Close()

	value, err := pkgmetadata.ReadMetadata(ctx, db, MetadataKeyBackendBaseURL)
	switch {
	case err == nil && value != "":
		baseURL, err := endpoint.DeriveBackendBaseURL(value)
		if err == nil {
			return baseURL, true, nil
		}
	case err == nil || isMetadataAbsentErr(err):
		// fall through to legacy endpoint keys
	default:
		return "", false, fmt.Errorf("read metadata %q: %w", MetadataKeyBackendBaseURL, err)
	}

	for _, key := range []string{"enroll_endpoint", "metrics_endpoint", "logs_endpoint", "nonce_endpoint"} {
		value, err := pkgmetadata.ReadMetadata(ctx, db, key)
		switch {
		case err == nil && value == "":
			continue
		case err == nil:
			baseURL, err := endpoint.DeriveBackendBaseURL(value)
			if err != nil {
				continue
			}
			return baseURL, true, nil
		case isMetadataAbsentErr(err):
			continue
		default:
			return "", false, fmt.Errorf("read metadata %q: %w", key, err)
		}
	}

	return "", false, nil
}

func (s *sqliteState) SetBackendBaseURL(ctx context.Context, value string) error {
	if _, err := endpoint.ValidateBackendEndpoint(value); err != nil {
		return fmt.Errorf("validate backend base URL: %w", err)
	}
	return s.setMetadata(ctx, MetadataKeyBackendBaseURL, value)
}

func (s *sqliteState) GetJWT(ctx context.Context) (string, bool, error) {
	return s.getMetadata(ctx, pkgmetadata.MetadataKeyToken)
}

func (s *sqliteState) SetJWT(ctx context.Context, value string) error {
	return s.setMetadata(ctx, pkgmetadata.MetadataKeyToken, value)
}

func (s *sqliteState) GetSAK(ctx context.Context) (string, bool, error) {
	return s.getMetadata(ctx, MetadataKeySAKToken)
}

func (s *sqliteState) SetSAK(ctx context.Context, value string) error {
	return s.setMetadata(ctx, MetadataKeySAKToken, value)
}

func (s *sqliteState) GetNodeUUID(ctx context.Context) (string, bool, error) {
	return s.getMetadata(ctx, pkgmetadata.MetadataKeyMachineID)
}

func (s *sqliteState) SetNodeUUID(ctx context.Context, value string) error {
	return s.setMetadata(ctx, pkgmetadata.MetadataKeyMachineID, value)
}

func (s *sqliteState) GetOrCreateNodeUUID(ctx context.Context, create func() (string, error)) (string, bool, error) {
	db, err := s.openReadWrite()
	if err != nil {
		return "", false, err
	}
	defer db.Close()

	if stateFile, err := s.stateFileFn(); err == nil {
		if err := config.SecureStateFilePermissions(stateFile); err != nil {
			return "", false, fmt.Errorf("secure state file permissions: %w", err)
		}
	}
	if err := pkgmetadata.CreateTableMetadata(ctx, db); err != nil {
		return "", false, fmt.Errorf("create metadata table: %w", err)
	}

	return GetOrCreateNodeUUIDMetadata(ctx, db, create)
}

// GetOrCreateNodeUUIDMetadata atomically returns the committed node UUID
// metadata value, creating it when missing or repairing an empty value.
func GetOrCreateNodeUUIDMetadata(ctx context.Context, db *sql.DB, create func() (string, error)) (string, bool, error) {
	if create == nil {
		return "", false, fmt.Errorf("node UUID create function is required")
	}

	existing, ok, err := readNodeUUIDDB(ctx, db)
	if err != nil {
		return "", false, err
	} else if ok {
		return existing, false, nil
	}

	candidate, err := create()
	if err != nil {
		return "", false, err
	}
	if candidate == "" {
		return "", false, fmt.Errorf("created node UUID is empty")
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO gpud_metadata (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value
WHERE gpud_metadata.value = ''`,
		pkgmetadata.MetadataKeyMachineID,
		candidate,
	); err != nil {
		return "", false, fmt.Errorf("insert node UUID metadata: %w", err)
	}

	committed, ok, err := readNodeUUIDDB(ctx, db)
	if err != nil {
		return "", false, err
	} else if !ok {
		return "", false, fmt.Errorf("node UUID metadata was not committed")
	} else if committed == "" {
		return "", false, fmt.Errorf("node UUID metadata is empty")
	}
	return committed, committed == candidate, nil
}

func readNodeUUIDDB(ctx context.Context, db *sql.DB) (string, bool, error) {
	var value string
	err := db.QueryRowContext(ctx, `
SELECT value FROM gpud_metadata WHERE key = ?`,
		pkgmetadata.MetadataKeyMachineID,
	).Scan(&value)
	switch {
	case err == nil && value != "":
		return value, true, nil
	case err == nil:
		return "", false, nil
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	default:
		return "", false, fmt.Errorf("read node UUID metadata: %w", err)
	}
}

func (s *sqliteState) GetNodeGroup(ctx context.Context) (string, bool, error) {
	return s.getMetadata(ctx, MetadataKeyNodeGroup)
}

func (s *sqliteState) SetNodeGroup(ctx context.Context, value string) error {
	return s.setMetadata(ctx, MetadataKeyNodeGroup, value)
}

func (s *sqliteState) GetComputeZone(ctx context.Context) (string, bool, error) {
	return s.getMetadata(ctx, MetadataKeyComputeZone)
}

func (s *sqliteState) SetComputeZone(ctx context.Context, value string) error {
	return s.setMetadata(ctx, MetadataKeyComputeZone, value)
}

func (s *sqliteState) GetNodePlacement(ctx context.Context) (string, bool, string, bool, error) {
	db, err := s.openReadOnly()
	if err != nil {
		return "", false, "", false, err
	}
	defer db.Close()

	return ReadNodePlacementMetadata(ctx, db)
}

// ReadNodePlacementMetadata reads node group and compute zone in one SQLite
// statement so callers observe both values from the same database snapshot.
func ReadNodePlacementMetadata(ctx context.Context, db *sql.DB) (string, bool, string, bool, error) {
	if db == nil {
		return "", false, "", false, fmt.Errorf("read node placement metadata: database is required")
	}

	var nodeGroup, computeZone string
	err := db.QueryRowContext(ctx, `
SELECT
  COALESCE(MAX(CASE WHEN key = ? THEN value END), ''),
  COALESCE(MAX(CASE WHEN key = ? THEN value END), '')
FROM gpud_metadata
WHERE key IN (?, ?)`,
		MetadataKeyNodeGroup,
		MetadataKeyComputeZone,
		MetadataKeyNodeGroup,
		MetadataKeyComputeZone,
	).Scan(&nodeGroup, &computeZone)
	if err != nil {
		if isMetadataAbsentErr(err) {
			return "", false, "", false, nil
		}
		return "", false, "", false, fmt.Errorf("read node placement metadata: %w", err)
	}
	return nodeGroup, nodeGroup != "", computeZone, computeZone != "", nil
}

func (s *sqliteState) SetNodePlacement(ctx context.Context, nodeGroup, computeZone string) error {
	db, err := s.openReadWrite()
	if err != nil {
		return err
	}
	defer db.Close()

	if stateFile, err := s.stateFileFn(); err == nil {
		if err := config.SecureStateFilePermissions(stateFile); err != nil {
			return fmt.Errorf("secure state file permissions: %w", err)
		}
	}
	if err := pkgmetadata.CreateTableMetadata(ctx, db); err != nil {
		return fmt.Errorf("create metadata table: %w", err)
	}
	return UpdateNodePlacementMetadata(ctx, db, &nodeGroup, &computeZone)
}

// UpdateNodePlacementMetadata atomically updates the supplied placement
// fields and preserves either existing value whose argument is nil.
func UpdateNodePlacementMetadata(ctx context.Context, db *sql.DB, nodeGroup, computeZone *string) error {
	if db == nil {
		return fmt.Errorf("update node placement metadata: database is required")
	}
	if nodeGroup == nil && computeZone == nil {
		return nil
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO gpud_metadata (key, value) VALUES
  (?, COALESCE(?, (SELECT value FROM gpud_metadata WHERE key = ?), '')),
  (?, COALESCE(?, (SELECT value FROM gpud_metadata WHERE key = ?), ''))
ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		MetadataKeyNodeGroup,
		optionalStringValue(nodeGroup),
		MetadataKeyNodeGroup,
		MetadataKeyComputeZone,
		optionalStringValue(computeZone),
		MetadataKeyComputeZone,
	); err != nil {
		return fmt.Errorf("update node placement metadata: %w", err)
	}
	return nil
}

func optionalStringValue(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func (s *sqliteState) GetEnrollmentTime(ctx context.Context) (time.Time, bool, error) {
	value, ok, err := s.getMetadata(ctx, MetadataKeyEnrolledAt)
	if err != nil || !ok {
		return time.Time{}, ok, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parse metadata %q: %w", MetadataKeyEnrolledAt, err)
	}
	return parsed.UTC(), true, nil
}

func (s *sqliteState) SetEnrollmentTime(ctx context.Context, value time.Time) error {
	if value.IsZero() {
		return fmt.Errorf("enrollment time cannot be zero")
	}
	return s.setMetadata(ctx, MetadataKeyEnrolledAt, value.UTC().Format(time.RFC3339Nano))
}

func (s *sqliteState) getMetadata(ctx context.Context, key string) (string, bool, error) {
	db, err := s.openReadOnly()
	if err != nil {
		return "", false, err
	}
	defer db.Close()

	value, err := pkgmetadata.ReadMetadata(ctx, db, key)
	if err != nil {
		if isMetadataAbsentErr(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read metadata %q: %w", key, err)
	}
	if value == "" {
		return "", false, nil
	}
	return value, true, nil
}

func (s *sqliteState) setMetadata(ctx context.Context, key, value string) error {
	db, err := s.openReadWrite()
	if err != nil {
		return err
	}
	defer db.Close()

	if stateFile, err := s.stateFileFn(); err == nil {
		if err := config.SecureStateFilePermissions(stateFile); err != nil {
			return fmt.Errorf("secure state file permissions: %w", err)
		}
	}
	if err := pkgmetadata.CreateTableMetadata(ctx, db); err != nil {
		return fmt.Errorf("create metadata table: %w", err)
	}
	if err := pkgmetadata.SetMetadata(ctx, db, key, value); err != nil {
		return fmt.Errorf("set metadata %q: %w", key, err)
	}
	return nil
}

func (s *sqliteState) openReadOnly() (*sql.DB, error) {
	stateFile, err := s.stateFileFn()
	if err != nil {
		return nil, fmt.Errorf("get state file path: %w", err)
	}
	db, err := sqlite.Open(stateFile, sqlite.WithReadOnly(true))
	if err != nil {
		return nil, fmt.Errorf("open state database read-only: %w", err)
	}
	return db, nil
}

func isMetadataAbsentErr(err error) bool {
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}

	var sqliteErr sqlite3.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code == sqlite3.ErrError && strings.Contains(strings.ToLower(sqliteErr.Error()), "no such table")
}

func (s *sqliteState) openReadWrite() (*sql.DB, error) {
	stateFile, err := s.stateFileFn()
	if err != nil {
		return nil, fmt.Errorf("get state file path: %w", err)
	}
	db, err := sqlite.Open(stateFile)
	if err != nil {
		return nil, fmt.Errorf("open state database: %w", err)
	}
	return db, nil
}
