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

package sink

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/backendclient"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/inventory"
)

type fakeState struct {
	baseURL           string
	jwt               string
	nodeUUID          string
	nodeGroup         string
	computeZone       string
	nodeGroupErr      error
	computeErr        error
	setNodeErr        error
	setComputeErr     error
	setNodeGroup      string
	setComputeZone    string
	setNodeCalls      int
	setComputeCalls   int
	setPlacementCalls int
	enrolled          time.Time
	enrollmentErr     error
	err               error
}

func (f *fakeState) GetBackendBaseURL(context.Context) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	return f.baseURL, f.baseURL != "", nil
}
func (f *fakeState) SetBackendBaseURL(context.Context, string) error { return nil }
func (f *fakeState) GetJWT(context.Context) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	return f.jwt, f.jwt != "", nil
}
func (f *fakeState) SetJWT(context.Context, string) error         { return nil }
func (f *fakeState) GetSAK(context.Context) (string, bool, error) { return "", false, nil }
func (f *fakeState) SetSAK(context.Context, string) error         { return nil }
func (f *fakeState) GetNodeUUID(context.Context) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	return f.nodeUUID, f.nodeUUID != "", nil
}
func (f *fakeState) SetNodeUUID(context.Context, string) error { return nil }
func (f *fakeState) GetOrCreateNodeUUID(_ context.Context, create func() (string, error)) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	if f.nodeUUID != "" {
		return f.nodeUUID, false, nil
	}
	value, err := create()
	if err != nil {
		return "", false, err
	}
	f.nodeUUID = value
	return value, true, nil
}
func (f *fakeState) GetNodeGroup(context.Context) (string, bool, error) {
	if f.nodeGroupErr != nil {
		return "", false, f.nodeGroupErr
	}
	if f.err != nil {
		return "", false, f.err
	}
	return f.nodeGroup, f.nodeGroup != "", nil
}
func (f *fakeState) SetNodeGroup(_ context.Context, value string) error {
	f.setNodeCalls++
	f.setNodeGroup = value
	return f.setNodeErr
}
func (f *fakeState) GetComputeZone(context.Context) (string, bool, error) {
	if f.computeErr != nil {
		return "", false, f.computeErr
	}
	if f.err != nil {
		return "", false, f.err
	}
	return f.computeZone, f.computeZone != "", nil
}
func (f *fakeState) SetComputeZone(_ context.Context, value string) error {
	f.setComputeCalls++
	f.setComputeZone = value
	return f.setComputeErr
}
func (f *fakeState) GetNodePlacement(context.Context) (string, bool, string, bool, error) {
	if f.nodeGroupErr != nil {
		return "", false, "", false, f.nodeGroupErr
	}
	if f.computeErr != nil {
		return "", false, "", false, f.computeErr
	}
	if f.err != nil {
		return "", false, "", false, f.err
	}
	return f.nodeGroup, f.nodeGroup != "", f.computeZone, f.computeZone != "", nil
}
func (f *fakeState) SetNodePlacement(_ context.Context, nodeGroup, computeZone string) error {
	f.setPlacementCalls++
	f.setNodeCalls++
	f.setComputeCalls++
	f.setNodeGroup = nodeGroup
	f.setComputeZone = computeZone
	return errors.Join(f.setNodeErr, f.setComputeErr)
}
func (f *fakeState) GetEnrollmentTime(context.Context) (time.Time, bool, error) {
	if f.enrollmentErr != nil {
		return time.Time{}, false, f.enrollmentErr
	}
	if f.err != nil {
		return time.Time{}, false, f.err
	}
	return f.enrolled, !f.enrolled.IsZero(), nil
}
func (f *fakeState) SetEnrollmentTime(context.Context, time.Time) error { return nil }

type fakeClient struct {
	nodeUUID string
	req      *backendclient.NodeUpsertRequest
	jwt      string
	resp     *backendclient.NodeUpsertResponse
	err      error
}

func (f *fakeClient) Enroll(context.Context, string) (string, error) { return "", nil }
func (f *fakeClient) GetNonce(context.Context, string, string) (*backendclient.NonceResponse, error) {
	return nil, nil
}
func (f *fakeClient) SubmitAttestation(context.Context, string, *backendclient.AttestationRequest, string) error {
	return nil
}
func (f *fakeClient) UpsertNode(_ context.Context, nodeUUID string, req *backendclient.NodeUpsertRequest, jwt string) (*backendclient.NodeUpsertResponse, error) {
	f.nodeUUID = nodeUUID
	f.req = req
	f.jwt = jwt
	if f.resp == nil && f.err == nil {
		f.resp = &backendclient.NodeUpsertResponse{NodeUUID: nodeUUID}
	}
	return f.resp, f.err
}

func TestBackendSinkExportNotReady(t *testing.T) {
	s := &backendSink{
		state:         &fakeState{},
		clientFactory: backendclient.New,
	}

	err := s.Export(context.Background(), &inventory.Snapshot{})
	require.ErrorIs(t, err, inventory.ErrNotReady)
}

func TestBackendSinkExportErrors(t *testing.T) {
	err := (&backendSink{}).Export(context.Background(), &inventory.Snapshot{})
	require.ErrorContains(t, err, "agent state")

	err = (&backendSink{state: &fakeState{baseURL: "https://example.com", jwt: "jwt"}}).Export(context.Background(), &inventory.Snapshot{})
	require.ErrorContains(t, err, "client factory")

	err = (&backendSink{
		state:         &fakeState{err: errors.New("state error")},
		clientFactory: backendclient.New,
	}).Export(context.Background(), &inventory.Snapshot{})
	require.ErrorContains(t, err, "state error")

	err = (&backendSink{
		state:         &fakeState{baseURL: "https://example.com", jwt: "jwt"},
		clientFactory: backendclient.New,
	}).Export(context.Background(), nil)
	require.ErrorContains(t, err, "inventory snapshot")

	err = (&backendSink{
		state: &fakeState{baseURL: "https://example.com", jwt: "jwt", nodeUUID: "node-1"},
		clientFactory: func(string) (backendclient.Client, error) {
			return nil, errors.New("client factory error")
		},
	}).Export(context.Background(), &inventory.Snapshot{})
	require.ErrorContains(t, err, "create backend client")
}

func TestBackendSinkExportUsesState(t *testing.T) {
	enrollmentTime := time.Date(2026, 5, 6, 15, 0, 0, 0, time.UTC)
	client := &fakeClient{resp: &backendclient.NodeUpsertResponse{
		NodeUUID:    "node-1",
		NodeGroup:   "resolved-group",
		ComputeZone: "resolved-zone",
	}}
	state := &fakeState{
		baseURL:     "https://example.com",
		jwt:         "jwt-token",
		nodeUUID:    "node-1",
		nodeGroup:   "group-a",
		computeZone: "zone-a",
		enrolled:    enrollmentTime,
	}
	s := &backendSink{
		state: state,
		clientFactory: func(string) (backendclient.Client, error) {
			return client, nil
		},
	}

	err := s.Export(context.Background(), &inventory.Snapshot{
		Hostname:  "host-a",
		MachineID: "machine-id",
	})
	require.NoError(t, err)
	require.Equal(t, "node-1", client.nodeUUID)
	require.Equal(t, "jwt-token", client.jwt)
	require.NotNil(t, client.req)
	require.Equal(t, "host-a", client.req.Hostname)
	require.Equal(t, "group-a", client.req.NodeGroup)
	require.Equal(t, "zone-a", client.req.ComputeZone)
	require.NotNil(t, client.req.EnrolledAt)
	require.Equal(t, enrollmentTime, *client.req.EnrolledAt)
	require.Equal(t, "resolved-group", state.setNodeGroup)
	require.Equal(t, "resolved-zone", state.setComputeZone)
	require.Equal(t, 1, state.setNodeCalls)
	require.Equal(t, 1, state.setComputeCalls)
	require.Equal(t, 1, state.setPlacementCalls)
}

func TestBackendSinkExportAtomicallyPersistsChangedResolvedMembership(t *testing.T) {
	tests := []struct {
		name                string
		currentGroup        string
		currentZone         string
		resolvedGroup       string
		resolvedZone        string
		wantPlacementWrites int
	}{
		{
			name:                "absent metadata and empty response unchanged",
			wantPlacementWrites: 0,
		},
		{
			name:                "both unchanged",
			currentGroup:        "group-a",
			currentZone:         "zone-a",
			resolvedGroup:       "group-a",
			resolvedZone:        "zone-a",
			wantPlacementWrites: 0,
		},
		{
			name:                "only node group changed",
			currentGroup:        "group-a",
			currentZone:         "zone-a",
			resolvedGroup:       "group-b",
			resolvedZone:        "zone-a",
			wantPlacementWrites: 1,
		},
		{
			name:                "only compute zone changed",
			currentGroup:        "group-a",
			currentZone:         "zone-a",
			resolvedGroup:       "group-a",
			resolvedZone:        "zone-b",
			wantPlacementWrites: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := &fakeState{
				baseURL:     "https://example.com",
				jwt:         "jwt-token",
				nodeUUID:    "node-1",
				nodeGroup:   tc.currentGroup,
				computeZone: tc.currentZone,
			}
			client := &fakeClient{resp: &backendclient.NodeUpsertResponse{
				NodeUUID:    "node-1",
				NodeGroup:   tc.resolvedGroup,
				ComputeZone: tc.resolvedZone,
			}}
			s := &backendSink{
				state: state,
				clientFactory: func(string) (backendclient.Client, error) {
					return client, nil
				},
			}

			err := s.Export(context.Background(), &inventory.Snapshot{})
			require.NoError(t, err)
			require.Equal(t, tc.wantPlacementWrites, state.setPlacementCalls)
			if tc.wantPlacementWrites > 0 {
				require.Equal(t, tc.resolvedGroup, state.setNodeGroup)
				require.Equal(t, tc.resolvedZone, state.setComputeZone)
			}
		})
	}
}

func TestBackendSinkExportClearsStateWhenResolvedMembershipIsEmpty(t *testing.T) {
	client := &fakeClient{}
	state := &fakeState{
		baseURL:     "https://example.com",
		jwt:         "jwt-token",
		nodeUUID:    "node-1",
		nodeGroup:   "stale-group",
		computeZone: "stale-zone",
	}
	s := &backendSink{
		state: state,
		clientFactory: func(string) (backendclient.Client, error) {
			return client, nil
		},
	}

	err := s.Export(context.Background(), &inventory.Snapshot{
		Hostname:  "host-a",
		MachineID: "machine-id",
	})
	require.NoError(t, err)
	require.NotNil(t, client.req)
	require.Equal(t, "stale-group", client.req.NodeGroup)
	require.Equal(t, "stale-zone", client.req.ComputeZone)
	require.Empty(t, state.setNodeGroup)
	require.Empty(t, state.setComputeZone)
	require.Equal(t, 1, state.setNodeCalls)
	require.Equal(t, 1, state.setComputeCalls)
	require.Equal(t, 1, state.setPlacementCalls)
}

func TestBackendSinkExportReturnsResolvedMembershipPersistenceErrors(t *testing.T) {
	state := &fakeState{
		baseURL:       "https://example.com",
		jwt:           "jwt-token",
		nodeUUID:      "node-1",
		setNodeErr:    errors.New("node group write failed"),
		setComputeErr: errors.New("compute zone write failed"),
	}
	client := &fakeClient{resp: &backendclient.NodeUpsertResponse{
		NodeUUID:    "node-1",
		NodeGroup:   "resolved-group",
		ComputeZone: "resolved-zone",
	}}
	s := &backendSink{
		state: state,
		clientFactory: func(string) (backendclient.Client, error) {
			return client, nil
		},
	}

	err := s.Export(context.Background(), &inventory.Snapshot{})
	require.ErrorContains(t, err, "persist backend-resolved node placement")
	require.ErrorContains(t, err, "node group write failed")
	require.ErrorContains(t, err, "compute zone write failed")
	require.Equal(t, "resolved-group", state.setNodeGroup)
	require.Equal(t, "resolved-zone", state.setComputeZone)
	require.Equal(t, 1, state.setNodeCalls)
	require.Equal(t, 1, state.setComputeCalls)
	require.Equal(t, 1, state.setPlacementCalls)
}

func TestBackendSinkExportEnrollmentTimeErrorIsNonFatal(t *testing.T) {
	client := &fakeClient{}
	s := &backendSink{
		state: &fakeState{
			baseURL:       "https://example.com",
			jwt:           "jwt-token",
			nodeUUID:      "node-1",
			enrollmentErr: errors.New("malformed enrollment timestamp"),
		},
		clientFactory: func(string) (backendclient.Client, error) {
			return client, nil
		},
	}

	err := s.Export(context.Background(), &inventory.Snapshot{
		Hostname:  "host-a",
		MachineID: "machine-id",
	})
	require.NoError(t, err)
	require.NotNil(t, client.req)
	require.Nil(t, client.req.EnrolledAt)
}

func TestBackendSinkExportOptionalMetadataErrorsAreNonFatal(t *testing.T) {
	client := &fakeClient{}
	state := &fakeState{
		baseURL:      "https://example.com",
		jwt:          "jwt-token",
		nodeUUID:     "node-1",
		nodeGroupErr: errors.New("failed to read nodegroup"),
		computeErr:   errors.New("failed to read compute zone"),
	}
	s := &backendSink{
		state: state,
		clientFactory: func(string) (backendclient.Client, error) {
			return client, nil
		},
	}

	err := s.Export(context.Background(), &inventory.Snapshot{
		Hostname:  "host-a",
		MachineID: "machine-id",
	})
	require.NoError(t, err)
	require.NotNil(t, client.req)
	require.Empty(t, client.req.NodeGroup)
	require.Empty(t, client.req.ComputeZone)
	require.Zero(t, state.setNodeCalls)
	require.Zero(t, state.setComputeCalls)
	require.Zero(t, state.setPlacementCalls)
}

func TestBackendSinkValidationDoesNotBlockExport(t *testing.T) {
	client := &fakeClient{}
	s := &backendSink{
		state: &fakeState{
			baseURL:  "https://example.com",
			jwt:      "jwt-token",
			nodeUUID: "node-1",
		},
		clientFactory: func(string) (backendclient.Client, error) {
			return client, nil
		},
	}

	err := s.Export(context.Background(), &inventory.Snapshot{
		Hostname:  "host-a",
		MachineID: "machine-id",
		Resources: inventory.Resources{
			DiskInfo: inventory.DiskInfo{
				BlockDevices: []inventory.BlockDevice{{
					Name: "nfs-1",
					Size: -1,
				}},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, client.req)
	require.Equal(t, int64(-1), client.req.Resources.DiskInfo.BlockDevices[0].Size)
}
