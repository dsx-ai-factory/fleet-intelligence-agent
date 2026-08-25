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
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/agentstate"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/backendclient"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/validation/outbound"
)

// JWTProvider retrieves the current backend JWT.
type JWTProvider interface {
	GetJWT(ctx context.Context) (string, error)
	SetJWT(ctx context.Context, value string) error
}

// Manager coordinates periodic attestation collection into a store.
type Manager interface {
	Run(ctx context.Context) error
	CollectOnce(ctx context.Context) (*Result, error)
	LastResult() *Result
	IsResultUpdated(since time.Time) bool
}

type manager struct {
	mu               sync.RWMutex
	runMu            sync.Mutex
	nodeUUIDProvider func(context.Context) (string, error)
	jwtProvider      JWTProvider
	nonceProvider    NonceProvider
	collector        EvidenceCollector
	submitter        Submitter
	config           AttestationConfig

	lastResult  *Result
	lastUpdated time.Time
}

// NewManager creates an attestation loop manager skeleton.
func NewManager(
	nodeUUIDProvider func(context.Context) (string, error),
	jwtProvider JWTProvider,
	nonceProvider NonceProvider,
	collector EvidenceCollector,
	submitter Submitter,
	cfg AttestationConfig,
) Manager {
	return &manager{
		nodeUUIDProvider: nodeUUIDProvider,
		jwtProvider:      jwtProvider,
		nonceProvider:    nonceProvider,
		collector:        collector,
		submitter:        submitter,
		config:           cfg,
	}
}

func (m *manager) Run(ctx context.Context) error {
	if m.nodeUUIDProvider == nil || m.jwtProvider == nil || m.nonceProvider == nil || m.collector == nil || m.submitter == nil {
		return fmt.Errorf("attestation loop dependencies are incomplete")
	}
	if m.config.Interval <= 0 {
		return nil
	}
	if m.config.StartupJitter > 0 {
		jitter := calculateJitter(m.config.StartupJitter)
		log.Logger.Infow("adding initial attestation startup jitter", "jitter_duration", jitter)
		if err := sleepWithContext(ctx, jitter); err != nil {
			return err
		}
	}

	for {
		_, err := m.runAttempt(ctx)
		nextInterval := m.nextInterval(err)
		m.logNextRun(err, nextInterval)
		if err := sleepWithContext(ctx, nextInterval); err != nil {
			return err
		}
	}
}

func (m *manager) nextInterval(err error) time.Duration {
	if err != nil && m.config.RetryInterval > 0 {
		return m.config.RetryInterval
	}
	return m.config.Interval
}

func (m *manager) logNextRun(err error, nextInterval time.Duration) {
	if err == nil {
		log.Logger.Infow("attestation attempt succeeded", "next_run_in", nextInterval)
		return
	}
	if errors.Is(err, ErrNotEnrolled) {
		log.Logger.Infow("attestation attempt not ready", "next_run_in", nextInterval, "error", err)
		return
	}
	log.Logger.Warnw("attestation attempt failed", "next_run_in", nextInterval, "error", err)
}

func (m *manager) runAttempt(ctx context.Context) (*Result, error) {
	if m.config.Timeout <= 0 {
		return m.CollectOnce(ctx)
	}
	if !m.runMu.TryLock() {
		return nil, fmt.Errorf("previous attestation workflow is still running")
	}

	runCtx, cancel := context.WithTimeout(ctx, m.config.Timeout)
	defer cancel()
	done := make(chan struct {
		result *Result
		err    error
	}, 1)

	go func() {
		defer m.runMu.Unlock()
		result, err := m.CollectOnce(runCtx)
		done <- struct {
			result *Result
			err    error
		}{result: result, err: err}
	}()

	select {
	case result := <-done:
		return result.result, result.err
	case <-runCtx.Done():
		return nil, runCtx.Err()
	}
}

func (m *manager) CollectOnce(ctx context.Context) (*Result, error) {
	if m.nodeUUIDProvider == nil || m.jwtProvider == nil || m.nonceProvider == nil || m.collector == nil || m.submitter == nil {
		return nil, fmt.Errorf("attestation loop dependencies are incomplete")
	}

	nodeUUID, err := m.nodeUUIDProvider(ctx)
	if err != nil {
		return nil, err
	}
	jwt, err := m.jwtProvider.GetJWT(ctx)
	if err != nil {
		return nil, err
	}
	nonce, refreshTS, refreshedJWT, err := m.nonceProvider.GetNonce(ctx, nodeUUID, jwt)
	if err != nil {
		return nil, err
	}
	if refreshedJWT != "" && refreshedJWT != jwt {
		if err := m.jwtProvider.SetJWT(ctx, refreshedJWT); err != nil {
			return nil, err
		}
		jwt = refreshedJWT
	}
	sdkResp, err := m.collector.Collect(ctx, nonce)
	collectErr := err
	result := &Result{
		CollectedAt:           time.Now().UTC(),
		NodeUUID:              nodeUUID,
		NonceRefreshTimestamp: refreshTS,
	}
	if err != nil {
		result.Success = false
		result.ErrorMessage = err.Error()
	} else {
		result.Success = true
	}
	if sdkResp != nil {
		result.SDKResponse = *sdkResp
	}
	m.mu.Lock()
	cloned := *result
	m.lastResult = &cloned
	m.lastUpdated = time.Now().UTC()
	m.mu.Unlock()
	if err := m.submitter.Submit(ctx, result, jwt); err != nil {
		return nil, err
	}
	if collectErr != nil {
		return result, collectErr
	}
	return result, nil
}

func (m *manager) LastResult() *Result {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.lastResult == nil {
		return nil
	}
	cloned := *m.lastResult
	return &cloned
}

func (m *manager) IsResultUpdated(since time.Time) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastUpdated.After(since)
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func calculateJitter(maxJitter time.Duration) time.Duration {
	if maxJitter <= 0 {
		return 0
	}
	maxMs := int64(maxJitter / time.Millisecond)
	if maxMs <= 0 {
		return 0
	}
	randomMs, err := rand.Int(rand.Reader, big.NewInt(maxMs))
	if err != nil {
		log.Logger.Warnw("failed to generate secure attestation jitter, using fallback", "error", err)
		return time.Duration(time.Now().UnixNano()%maxMs) * time.Millisecond
	}
	return time.Duration(randomMs.Int64()) * time.Millisecond
}

type backendSubmitter struct {
	client BackendClient
}

// BackendClient is the backend client view required by the attestation workflow.
type BackendClient interface {
	SubmitAttestation(ctx context.Context, nodeUUID string, req *backendclient.AttestationRequest, jwt string) error
}

// NewBackendSubmitter creates a backend submitter backed by the agent backend client.
func NewBackendSubmitter(client BackendClient) Submitter {
	return &backendSubmitter{client: client}
}

func (s *backendSubmitter) Submit(ctx context.Context, result *Result, jwt string) error {
	if s.client == nil {
		return fmt.Errorf("attestation submission requires backend client")
	}
	if result == nil {
		return fmt.Errorf("attestation submission requires result")
	}
	if jwt == "" {
		return fmt.Errorf("attestation submission requires jwt")
	}
	req := toAttestationRequest(result)
	outbound.LogIssues("attestation-backend-submitter", "AttestationRequest", outbound.ValidateAttestationRequest(req), "node_uuid", result.NodeUUID)
	return s.client.SubmitAttestation(ctx, result.NodeUUID, req, jwt)
}

type stateJWTProvider struct {
	state agentstate.State
}

// NewStateJWTProvider returns a JWT provider backed by persisted agent state.
func NewStateJWTProvider(state agentstate.State) JWTProvider {
	return &stateJWTProvider{state: state}
}

func (p *stateJWTProvider) GetJWT(ctx context.Context) (string, error) {
	if p.state == nil {
		return "", fmt.Errorf("jwt provider requires agent state")
	}
	value, ok, err := p.state.GetJWT(ctx)
	if err != nil {
		return "", err
	}
	if !ok || value == "" {
		return "", fmt.Errorf("%w: jwt not available in agent state", ErrNotEnrolled)
	}
	return value, nil
}

func (p *stateJWTProvider) SetJWT(ctx context.Context, value string) error {
	if p.state == nil {
		return fmt.Errorf("jwt provider requires agent state")
	}
	return p.state.SetJWT(ctx, value)
}

// NewStateNodeUUIDProvider returns a node UUID provider backed by persisted agent state.
func NewStateNodeUUIDProvider(state agentstate.State) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if state == nil {
			return "", fmt.Errorf("node UUID provider requires agent state")
		}
		value, ok, err := state.GetNodeUUID(ctx)
		if err != nil {
			return "", err
		}
		if !ok || value == "" {
			return "", fmt.Errorf("%w: node UUID not available in agent state", ErrNotEnrolled)
		}
		return value, nil
	}
}
