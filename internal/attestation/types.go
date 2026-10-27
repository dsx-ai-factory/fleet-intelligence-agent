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

// Package attestation owns the backend attestation workflow.
package attestation

import (
	"context"
	"errors"
	"time"
)

// ErrNotEnrolled indicates attestation cannot run yet because the agent is not enrolled.
var ErrNotEnrolled = errors.New("agent not enrolled")

// Result is the agent-owned attestation state model for the new backend sync loop.
type Result struct {
	CollectedAt           time.Time
	AgentUUID             string
	NonceRefreshTimestamp time.Time
	Success               bool
	ErrorMessage          string
	SDKResponse           SDKResponse
}

type SDKResponse struct {
	Evidences     []EvidenceItem `json:"evidences"`
	ResultCode    int            `json:"result_code"`
	ResultMessage string         `json:"result_message"`
}

type EvidenceItem struct {
	Arch          string `json:"arch"`
	Certificate   string `json:"certificate"`
	DriverVersion string `json:"driver_version"`
	Evidence      string `json:"evidence"`
	Nonce         string `json:"nonce"`
	VBIOSVersion  string `json:"vbios_version"`
	Version       string `json:"version"`
}

// NonceProvider retrieves a backend nonce for the reporting agent.
type NonceProvider interface {
	GetNonce(ctx context.Context, agentUUID, jwt string) (nonce string, refreshTS time.Time, refreshedJWT string, err error)
}

// EvidenceCollector collects attestation evidence from local tooling.
type EvidenceCollector interface {
	Collect(ctx context.Context, nonce string) (*SDKResponse, error)
}

// Submitter submits attestation results to the backend.
type Submitter interface {
	Submit(ctx context.Context, result *Result, jwt string) error
}

// AttestationConfig controls periodic attestation workflow scheduling.
type AttestationConfig struct {
	Interval      time.Duration
	RetryInterval time.Duration
	Timeout       time.Duration
	StartupJitter time.Duration
}
