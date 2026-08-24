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
	"path/filepath"
	"testing"
	"time"

	pkgmetadata "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metadata"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/sqlite"
	"github.com/stretchr/testify/require"
)

func newTestSQLiteState(t *testing.T) *sqliteState {
	t.Helper()
	stateFile := filepath.Join(t.TempDir(), "agent.state")
	return &sqliteState{
		stateFileFn: func() (string, error) {
			return stateFile, nil
		},
	}
}

func TestSQLiteStateRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := newTestSQLiteState(t)

	err := state.SetBackendBaseURL(ctx, "https://backend.example.com")
	require.NoError(t, err)
	err = state.SetJWT(ctx, "jwt-token")
	require.NoError(t, err)
	err = state.SetSAK(ctx, "sak-token")
	require.NoError(t, err)
	err = state.SetNodeUUID(ctx, "node-1")
	require.NoError(t, err)
	err = state.SetNodeGroup(ctx, "group-a")
	require.NoError(t, err)
	err = state.SetComputeZone(ctx, "us-west-2a")
	require.NoError(t, err)
	enrollmentTime := time.Date(2026, 5, 6, 15, 0, 0, 123456789, time.UTC)
	err = state.SetEnrollmentTime(ctx, enrollmentTime)
	require.NoError(t, err)

	value, ok, err := state.GetBackendBaseURL(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "https://backend.example.com", value)

	value, ok, err = state.GetJWT(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "jwt-token", value)

	value, ok, err = state.GetSAK(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "sak-token", value)

	value, ok, err = state.GetNodeUUID(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "node-1", value)

	value, ok, err = state.GetNodeGroup(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "group-a", value)

	stateFile, err := state.stateFileFn()
	require.NoError(t, err)
	db, err := sqlite.Open(stateFile, sqlite.WithReadOnly(true))
	require.NoError(t, err)
	defer db.Close()
	value, err = pkgmetadata.ReadMetadata(ctx, db, "node_group")
	require.NoError(t, err)
	require.Equal(t, "group-a", value)

	value, ok, err = state.GetComputeZone(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "us-west-2a", value)

	gotEnrollmentTime, ok, err := state.GetEnrollmentTime(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, enrollmentTime, gotEnrollmentTime)
}

func TestSQLiteStateMissingValue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := newTestSQLiteState(t)

	err := state.SetJWT(ctx, "jwt-token")
	require.NoError(t, err)

	value, ok, err := state.GetBackendBaseURL(ctx)
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, value)
}

func TestSQLiteStateNodePlacementRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := newTestSQLiteState(t)

	err := state.SetNodePlacement(ctx, "group-a", "zone-a")
	require.NoError(t, err)

	nodeGroup, nodeGroupOK, computeZone, computeZoneOK, err := state.GetNodePlacement(ctx)
	require.NoError(t, err)
	require.True(t, nodeGroupOK)
	require.Equal(t, "group-a", nodeGroup)
	require.True(t, computeZoneOK)
	require.Equal(t, "zone-a", computeZone)

	err = state.SetNodePlacement(ctx, "", "zone-b")
	require.NoError(t, err)
	nodeGroup, nodeGroupOK, computeZone, computeZoneOK, err = state.GetNodePlacement(ctx)
	require.NoError(t, err)
	require.False(t, nodeGroupOK)
	require.Empty(t, nodeGroup)
	require.True(t, computeZoneOK)
	require.Equal(t, "zone-b", computeZone)
}

func TestSQLiteStateNodePlacementUpdateRollsBackBothValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := newTestSQLiteState(t)
	require.NoError(t, state.SetNodePlacement(ctx, "group-a", "zone-a"))

	stateFile, err := state.stateFileFn()
	require.NoError(t, err)
	db, err := sqlite.Open(stateFile)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
CREATE TRIGGER reject_compute_zone_update
BEFORE UPDATE ON gpud_metadata
WHEN NEW.key = 'compute_zone' AND NEW.value = 'rejected-zone'
BEGIN
  SELECT RAISE(ABORT, 'reject compute zone update');
END`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	err = state.SetNodePlacement(ctx, "group-b", "rejected-zone")
	require.ErrorContains(t, err, "reject compute zone update")

	nodeGroup, nodeGroupOK, computeZone, computeZoneOK, err := state.GetNodePlacement(ctx)
	require.NoError(t, err)
	require.True(t, nodeGroupOK)
	require.Equal(t, "group-a", nodeGroup)
	require.True(t, computeZoneOK)
	require.Equal(t, "zone-a", computeZone)
}

func TestSQLiteStateGetOrCreateNodeUUID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := newTestSQLiteState(t)

	value, created, err := state.GetOrCreateNodeUUID(ctx, func() (string, error) {
		return "node-created", nil
	})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "node-created", value)

	value, created, err = state.GetOrCreateNodeUUID(ctx, func() (string, error) {
		t.Fatal("create should not run when node UUID is already persisted")
		return "", nil
	})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "node-created", value)

	emptyRowState := newTestSQLiteState(t)
	err = emptyRowState.SetNodeUUID(ctx, "")
	require.NoError(t, err)

	value, created, err = emptyRowState.GetOrCreateNodeUUID(ctx, func() (string, error) {
		return "node-repaired", nil
	})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "node-repaired", value)

	value, ok, err := emptyRowState.GetNodeUUID(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "node-repaired", value)
}

func TestSQLiteStateMissingMetadataTableIsTreatedAsAbsent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := newTestSQLiteState(t)

	stateFile, err := state.stateFileFn()
	require.NoError(t, err)

	db, err := sqlite.Open(stateFile)
	require.NoError(t, err)
	_, err = db.Exec("PRAGMA user_version = 1")
	require.NoError(t, err)
	require.NoError(t, db.Close())

	for _, get := range []func(context.Context) (string, bool, error){
		state.GetJWT,
		state.GetSAK,
		state.GetNodeUUID,
		state.GetNodeGroup,
		state.GetComputeZone,
	} {
		value, ok, err := get(ctx)
		require.NoError(t, err)
		require.False(t, ok)
		require.Empty(t, value)
	}

	enrollmentTime, ok, err := state.GetEnrollmentTime(ctx)
	require.NoError(t, err)
	require.False(t, ok)
	require.True(t, enrollmentTime.IsZero())
}

func TestSQLiteStateSetBackendBaseURLValidatesInput(t *testing.T) {
	t.Parallel()

	state := newTestSQLiteState(t)

	err := state.SetBackendBaseURL(context.Background(), "http://example.com")
	require.Error(t, err)

	err = state.SetBackendBaseURL(context.Background(), "not-a-url")
	require.Error(t, err)

	err = state.SetEnrollmentTime(context.Background(), time.Time{})
	require.Error(t, err)
}

func TestSQLiteStateGetBackendBaseURLFallsBackToLegacyEndpoints(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := newTestSQLiteState(t)

	err := state.setMetadata(ctx, "metrics_endpoint", "https://backend.example.com/api/v1/health/metrics")
	require.NoError(t, err)

	value, ok, err := state.GetBackendBaseURL(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "https://backend.example.com", value)
}

func TestSQLiteStateGetBackendBaseURLSkipsMalformedValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := newTestSQLiteState(t)

	err := state.setMetadata(ctx, MetadataKeyBackendBaseURL, "not-a-url")
	require.NoError(t, err)
	err = state.setMetadata(ctx, "enroll_endpoint", "also-not-a-url")
	require.NoError(t, err)
	err = state.setMetadata(ctx, "metrics_endpoint", "https://backend.example.com/api/v1/health/metrics")
	require.NoError(t, err)

	value, ok, err := state.GetBackendBaseURL(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "https://backend.example.com", value)
}

func TestSQLiteStateStateFileErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	state := &sqliteState{
		stateFileFn: func() (string, error) {
			return "", boom
		},
	}

	_, _, err := state.GetJWT(context.Background())
	require.ErrorIs(t, err, boom)

	err = state.SetJWT(context.Background(), "jwt-token")
	require.ErrorIs(t, err, boom)
}

func TestNewSQLite(t *testing.T) {
	t.Parallel()
	require.NotNil(t, NewSQLite())
}

func TestSQLiteStateGetBackendBaseURLPropagatesReadErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	state := newTestSQLiteState(t)

	stateFile, err := state.stateFileFn()
	require.NoError(t, err)

	db, err := sqlite.Open(stateFile)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	_, _, err = state.GetBackendBaseURL(ctx)
	require.Error(t, err)
	require.NotErrorIs(t, err, sql.ErrNoRows)
}
