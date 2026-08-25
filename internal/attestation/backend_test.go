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

package attestation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/backendclient"
)

type stubState struct {
	baseURL  string
	baseOK   bool
	baseErr  error
	jwt      string
	jwtOK    bool
	jwtErr   error
	setJWT   string
	nodeUUID string
	nodeOK   bool
	nodeErr  error
}

func (s *stubState) GetBackendBaseURL(context.Context) (string, bool, error) {
	return s.baseURL, s.baseOK, s.baseErr
}
func (s *stubState) SetBackendBaseURL(context.Context, string) error { return nil }
func (s *stubState) GetJWT(context.Context) (string, bool, error)    { return s.jwt, s.jwtOK, s.jwtErr }
func (s *stubState) SetJWT(_ context.Context, v string) error        { s.setJWT = v; s.jwt = v; return nil }
func (s *stubState) GetSAK(context.Context) (string, bool, error)    { return "", false, nil }
func (s *stubState) SetSAK(context.Context, string) error            { return nil }
func (s *stubState) GetNodeUUID(context.Context) (string, bool, error) {
	return s.nodeUUID, s.nodeOK, s.nodeErr
}
func (s *stubState) SetNodeUUID(context.Context, string) error { return nil }
func (s *stubState) GetOrCreateNodeUUID(_ context.Context, create func() (string, error)) (string, bool, error) {
	if s.nodeErr != nil {
		return "", false, s.nodeErr
	}
	if s.nodeOK && s.nodeUUID != "" {
		return s.nodeUUID, false, nil
	}
	value, err := create()
	if err != nil {
		return "", false, err
	}
	s.nodeUUID = value
	s.nodeOK = true
	return value, true, nil
}
func (s *stubState) GetNodeGroup(context.Context) (string, bool, error) {
	return "", false, nil
}
func (s *stubState) SetNodeGroup(context.Context, string) error { return nil }
func (s *stubState) GetComputeZone(context.Context) (string, bool, error) {
	return "", false, nil
}
func (s *stubState) SetComputeZone(context.Context, string) error { return nil }
func (s *stubState) GetNodePlacement(context.Context) (string, bool, string, bool, error) {
	return "", false, "", false, nil
}
func (s *stubState) SetNodePlacement(context.Context, string, string) error { return nil }
func (s *stubState) GetEnrollmentTime(context.Context) (time.Time, bool, error) {
	return time.Time{}, false, nil
}
func (s *stubState) SetEnrollmentTime(context.Context, time.Time) error { return nil }

type recordingClient struct {
	lastNodeUUID string
	lastJWT      string
	lastReq      *backendclient.AttestationRequest
	nonceResp    *backendclient.NonceResponse
}

func (c *recordingClient) Enroll(context.Context, string) (string, error) { return "", nil }
func (c *recordingClient) UpsertNode(context.Context, string, *backendclient.NodeUpsertRequest, string) (*backendclient.NodeUpsertResponse, error) {
	return nil, nil
}
func (c *recordingClient) GetNonce(context.Context, string, string) (*backendclient.NonceResponse, error) {
	return c.nonceResp, nil
}
func (c *recordingClient) SubmitAttestation(_ context.Context, nodeUUID string, req *backendclient.AttestationRequest, jwt string) error {
	c.lastNodeUUID = nodeUUID
	c.lastJWT = jwt
	c.lastReq = req
	return nil
}

func TestToAttestationRequest(t *testing.T) {
	refreshTS := time.Now().UTC()
	req := toAttestationRequest(&Result{
		NonceRefreshTimestamp: refreshTS,
		Success:               true,
		ErrorMessage:          "",
		SDKResponse: SDKResponse{
			ResultCode:    7,
			ResultMessage: "ok",
			Evidences: []EvidenceItem{{
				Arch:          "BLACKWELL",
				Certificate:   "cert",
				DriverVersion: "575.1",
				Evidence:      "blob",
				Nonce:         "nonce",
				VBIOSVersion:  "vbios",
				Version:       "1.0",
			}},
		},
	})
	require.NotNil(t, req)
	require.Equal(t, refreshTS, req.AttestationData.NonceRefreshTimestamp)
	require.True(t, req.AttestationData.Success)
	require.Len(t, req.AttestationData.SDKResponse.Evidences, 1)
	require.Equal(t, "BLACKWELL", req.AttestationData.SDKResponse.Evidences[0].Arch)
	require.Nil(t, toAttestationRequest(nil))
}

func TestStateBackendClientFactory(t *testing.T) {
	orig := newBackendClient
	t.Cleanup(func() { newBackendClient = orig })

	client := &recordingClient{}
	newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
		require.Equal(t, "https://backend.example.com", rawBaseURL)
		return client, nil
	}

	factory := &stateBackendClientFactory{state: &stubState{baseURL: "https://backend.example.com", baseOK: true}}
	got, err := factory.client(context.Background())
	require.NoError(t, err)
	require.Equal(t, client, got)

	_, err = (&stateBackendClientFactory{}).client(context.Background())
	require.ErrorContains(t, err, "requires agent state")

	_, err = (&stateBackendClientFactory{state: &stubState{baseErr: errors.New("boom")}}).client(context.Background())
	require.ErrorContains(t, err, "boom")

	_, err = (&stateBackendClientFactory{state: &stubState{baseOK: false}}).client(context.Background())
	require.ErrorContains(t, err, "backend base URL not available")
}

func TestStateProvidersAndSubmitter(t *testing.T) {
	orig := newBackendClient
	t.Cleanup(func() { newBackendClient = orig })

	recording := &recordingClient{
		nonceResp: &backendclient.NonceResponse{
			Nonce:                 "abc123",
			NonceRefreshTimestamp: time.Unix(10, 0).UTC(),
			JWTAssertion:          "new-jwt",
		},
	}
	newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
		require.Equal(t, "https://backend.example.com", rawBaseURL)
		return recording, nil
	}

	state := &stubState{
		baseURL: "https://backend.example.com", baseOK: true,
		jwt: "jwt-token", jwtOK: true,
		nodeUUID: "node-1", nodeOK: true,
	}

	jwtProvider := NewStateJWTProvider(state)
	jwt, err := jwtProvider.GetJWT(context.Background())
	require.NoError(t, err)
	require.Equal(t, "jwt-token", jwt)
	require.NoError(t, jwtProvider.SetJWT(context.Background(), "updated"))
	require.Equal(t, "updated", state.setJWT)

	nodeUUID, err := NewStateNodeUUIDProvider(state)(context.Background())
	require.NoError(t, err)
	require.Equal(t, "node-1", nodeUUID)

	nonce, ts, refreshedJWT, err := NewStateNonceProvider(state).GetNonce(context.Background(), "node-1", "jwt-token")
	require.NoError(t, err)
	require.Equal(t, "abc123", nonce)
	require.Equal(t, time.Unix(10, 0).UTC(), ts)
	require.Equal(t, "new-jwt", refreshedJWT)

	result := &Result{
		NodeUUID: "node-1",
		SDKResponse: SDKResponse{
			ResultCode: 1,
			Evidences:  []EvidenceItem{{Arch: "BLACKWELL"}},
		},
	}
	err = NewStateBackendSubmitter(state).Submit(context.Background(), result, "jwt-token")
	require.NoError(t, err)
	require.Equal(t, "node-1", recording.lastNodeUUID)
	require.Equal(t, "jwt-token", recording.lastJWT)
	require.NotNil(t, recording.lastReq)
	require.Equal(t, "BLACKWELL", recording.lastReq.AttestationData.SDKResponse.Evidences[0].Arch)

	recording.lastNodeUUID = ""
	err = NewStateBackendSubmitter(state).Submit(context.Background(), &Result{}, "jwt-token")
	require.NoError(t, err)
	require.Equal(t, "node-1", recording.lastNodeUUID)
}

func TestStateBackendSubmitterMissingStateDoesNotPanic(t *testing.T) {
	err := NewStateBackendSubmitter(nil).Submit(context.Background(), &Result{}, "jwt-token")
	require.ErrorIs(t, err, ErrNotEnrolled)
	require.ErrorContains(t, err, "node UUID not available")
}

func TestStateProvidersPropagateBackendClientConstructionErrors(t *testing.T) {
	orig := newBackendClient
	t.Cleanup(func() { newBackendClient = orig })

	newBackendClient = func(string) (backendclient.Client, error) {
		return nil, errors.New("construct failed")
	}
	state := &stubState{baseURL: "https://backend.example.com", baseOK: true}

	_, _, _, err := NewStateNonceProvider(state).GetNonce(context.Background(), "node-1", "jwt-token")
	require.ErrorContains(t, err, "construct failed")

	err = NewStateBackendSubmitter(state).Submit(context.Background(), &Result{NodeUUID: "node-1"}, "jwt-token")
	require.ErrorContains(t, err, "construct failed")
}
