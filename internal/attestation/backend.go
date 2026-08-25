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
	"fmt"
	"time"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/agentstate"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/backendclient"
)

var newBackendClient = backendclient.New

func toAttestationRequest(r *Result) *backendclient.AttestationRequest {
	if r == nil {
		return nil
	}
	req := &backendclient.AttestationRequest{
		AttestationData: backendclient.AttestationData{
			NonceRefreshTimestamp: r.NonceRefreshTimestamp,
			Success:               r.Success,
			ErrorMessage:          r.ErrorMessage,
			SDKResponse: backendclient.AttestationSDKResponse{
				ResultCode:    r.SDKResponse.ResultCode,
				ResultMessage: r.SDKResponse.ResultMessage,
			},
		},
	}

	if len(r.SDKResponse.Evidences) > 0 {
		req.AttestationData.SDKResponse.Evidences = make([]backendclient.EvidenceItem, 0, len(r.SDKResponse.Evidences))
		for _, ev := range r.SDKResponse.Evidences {
			req.AttestationData.SDKResponse.Evidences = append(req.AttestationData.SDKResponse.Evidences, backendclient.EvidenceItem{
				Arch:          ev.Arch,
				Certificate:   ev.Certificate,
				DriverVersion: ev.DriverVersion,
				Evidence:      ev.Evidence,
				Nonce:         ev.Nonce,
				VBIOSVersion:  ev.VBIOSVersion,
				Version:       ev.Version,
			})
		}
	}

	return req
}

type stateBackendClientFactory struct {
	state agentstate.State
}

func (f *stateBackendClientFactory) client(ctx context.Context) (backendclient.Client, error) {
	if f.state == nil {
		return nil, fmt.Errorf("backend client factory requires agent state")
	}
	baseURL, ok, err := f.state.GetBackendBaseURL(ctx)
	if err != nil {
		return nil, err
	}
	if !ok || baseURL == "" {
		return nil, fmt.Errorf("%w: backend base URL not available in agent state", ErrNotEnrolled)
	}
	return newBackendClient(baseURL)
}

type stateNonceProvider struct {
	factory *stateBackendClientFactory
}

// NewStateNonceProvider creates a nonce provider that resolves backend state dynamically.
func NewStateNonceProvider(state agentstate.State) NonceProvider {
	return &stateNonceProvider{factory: &stateBackendClientFactory{state: state}}
}

func (p *stateNonceProvider) GetNonce(ctx context.Context, nodeUUID, jwt string) (string, time.Time, string, error) {
	client, err := p.factory.client(ctx)
	if err != nil {
		return "", time.Time{}, "", err
	}
	return NewBackendNonceProvider(client).GetNonce(ctx, nodeUUID, jwt)
}

type stateSubmitter struct {
	factory *stateBackendClientFactory
}

// NewStateBackendSubmitter creates a submitter that resolves backend state dynamically.
func NewStateBackendSubmitter(state agentstate.State) Submitter {
	return &stateSubmitter{factory: &stateBackendClientFactory{state: state}}
}

func (s *stateSubmitter) Submit(ctx context.Context, result *Result, jwt string) error {
	if result == nil {
		return fmt.Errorf("attestation submission requires result")
	}
	if result.NodeUUID == "" {
		if s.factory == nil || s.factory.state == nil {
			return fmt.Errorf("%w: node UUID not available in agent state", ErrNotEnrolled)
		}
		nodeUUID, ok, err := s.factory.state.GetNodeUUID(ctx)
		if err != nil {
			return err
		}
		if !ok || nodeUUID == "" {
			return fmt.Errorf("%w: node UUID not available in agent state", ErrNotEnrolled)
		}
		cloned := *result
		cloned.NodeUUID = nodeUUID
		result = &cloned
	}
	client, err := s.factory.client(ctx)
	if err != nil {
		return err
	}
	return NewBackendSubmitter(client).Submit(ctx, result, jwt)
}
