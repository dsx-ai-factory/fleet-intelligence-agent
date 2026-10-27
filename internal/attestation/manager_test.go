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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type testJWTProvider struct {
	jwt    string
	setJWT string
}

func (p *testJWTProvider) GetJWT(context.Context) (string, error) { return p.jwt, nil }
func (p *testJWTProvider) SetJWT(_ context.Context, value string) error {
	p.setJWT = value
	p.jwt = value
	return nil
}

type testNonceProvider struct {
	nonce        string
	refreshTS    time.Time
	refreshedJWT string
	err          error
}

func (p *testNonceProvider) GetNonce(context.Context, string, string) (string, time.Time, string, error) {
	return p.nonce, p.refreshTS, p.refreshedJWT, p.err
}

type testEvidenceCollector struct {
	resp *SDKResponse
	err  error
	n    int
}

func (c *testEvidenceCollector) Collect(context.Context, string) (*SDKResponse, error) {
	c.n++
	return c.resp, c.err
}

type blockingEvidenceCollector struct {
	started chan struct{}
	release chan struct{}
}

func (c *blockingEvidenceCollector) Collect(context.Context, string) (*SDKResponse, error) {
	close(c.started)
	<-c.release
	return &SDKResponse{ResultCode: 200}, nil
}

type submitted struct {
	result *Result
	jwt    string
}

type testSubmitter struct {
	submitted submitted
	err       error
	count     int
}

func (s *testSubmitter) Submit(_ context.Context, result *Result, jwt string) error {
	s.count++
	s.submitted = submitted{result: result, jwt: jwt}
	return s.err
}

func TestCollectOnceSuccess(t *testing.T) {
	refreshTS := time.Now().UTC()
	jwtProvider := &testJWTProvider{jwt: "old-jwt"}
	submitter := &testSubmitter{}
	manager := NewManager(
		func(context.Context) (string, error) { return "node-1", nil },
		jwtProvider,
		&testNonceProvider{nonce: "abc123", refreshTS: refreshTS, refreshedJWT: "new-jwt"},
		&testEvidenceCollector{resp: &SDKResponse{ResultCode: 200, ResultMessage: "ok"}},
		submitter,
		AttestationConfig{},
	)

	result, err := manager.CollectOnce(context.Background())
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, "node-1", result.AgentUUID)
	require.Equal(t, refreshTS, result.NonceRefreshTimestamp)
	require.Equal(t, "new-jwt", jwtProvider.setJWT)
	require.NotNil(t, submitter.submitted.result)
	require.Equal(t, "new-jwt", submitter.submitted.jwt)
	require.True(t, submitter.submitted.result.Success)
}

func TestCollectOnceCollectorFailureStillSubmitsFailureResult(t *testing.T) {
	submitter := &testSubmitter{}
	manager := NewManager(
		func(context.Context) (string, error) { return "node-1", nil },
		&testJWTProvider{jwt: "jwt-token"},
		&testNonceProvider{nonce: "abc123"},
		&testEvidenceCollector{err: errors.New("collect failed")},
		submitter,
		AttestationConfig{},
	)

	result, err := manager.CollectOnce(context.Background())
	require.ErrorContains(t, err, "collect failed")
	require.False(t, result.Success)
	require.Equal(t, "collect failed", result.ErrorMessage)
	require.NotNil(t, submitter.submitted.result)
	require.False(t, submitter.submitted.result.Success)
}

func TestCollectOnceMissingDependencies(t *testing.T) {
	_, err := NewManager(nil, nil, nil, nil, nil, AttestationConfig{}).CollectOnce(context.Background())
	require.Error(t, err)
}

func TestManagerRunAndCachedResult(t *testing.T) {
	submitter := &testSubmitter{}
	mgr := NewManager(
		func(context.Context) (string, error) { return "node-1", nil },
		&testJWTProvider{jwt: "jwt-token"},
		&testNonceProvider{nonce: "abc123"},
		&testEvidenceCollector{resp: &SDKResponse{ResultCode: 200}},
		submitter,
		AttestationConfig{Interval: 5 * time.Millisecond},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, mgr.Run(ctx), context.DeadlineExceeded)

	last := mgr.LastResult()
	require.NotNil(t, last)
	require.Equal(t, "node-1", last.AgentUUID)
	require.True(t, mgr.IsResultUpdated(time.Time{}))
}

func TestManagerRunUsesRetryIntervalOnFailure(t *testing.T) {
	submitter := &testSubmitter{}
	collector := &testEvidenceCollector{err: errors.New("collect failed")}
	mgr := NewManager(
		func(context.Context) (string, error) { return "node-1", nil },
		&testJWTProvider{jwt: "jwt-token"},
		&testNonceProvider{nonce: "abc123"},
		collector,
		submitter,
		AttestationConfig{Interval: time.Hour, RetryInterval: 5 * time.Millisecond},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, mgr.Run(ctx), context.DeadlineExceeded)

	last := mgr.LastResult()
	require.NotNil(t, last)
	require.False(t, last.Success)
	require.Equal(t, "collect failed", last.ErrorMessage)
	require.GreaterOrEqual(t, collector.n, 2)
	require.GreaterOrEqual(t, submitter.count, 2)
}

func TestManagerRunTimeoutDoesNotOverlapStuckWorkflow(t *testing.T) {
	collector := &blockingEvidenceCollector{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	mgr := NewManager(
		func(context.Context) (string, error) { return "node-1", nil },
		&testJWTProvider{jwt: "jwt-token"},
		&testNonceProvider{nonce: "abc123"},
		collector,
		&testSubmitter{},
		AttestationConfig{Timeout: 10 * time.Millisecond},
	).(*manager)

	start := time.Now()
	_, err := mgr.runAttempt(context.Background())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), time.Second)

	select {
	case <-collector.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for attestation workflow to start")
	}

	_, err = mgr.runAttempt(context.Background())
	require.ErrorContains(t, err, "previous attestation workflow is still running")

	close(collector.release)
}

func TestManagerHelpersAndSubmitterErrors(t *testing.T) {
	mgr := NewManager(
		func(context.Context) (string, error) { return "node-1", nil },
		&testJWTProvider{jwt: "jwt-token"},
		&testNonceProvider{nonce: "abc123"},
		&testEvidenceCollector{resp: &SDKResponse{}},
		&testSubmitter{},
		AttestationConfig{},
	)
	require.Nil(t, mgr.LastResult())
	require.False(t, mgr.IsResultUpdated(time.Now().UTC()))

	err := NewBackendSubmitter(nil).Submit(context.Background(), &Result{}, "jwt")
	require.ErrorContains(t, err, "backend client")
	err = NewBackendSubmitter(&recordingClient{}).Submit(context.Background(), nil, "jwt")
	require.ErrorContains(t, err, "requires result")
}

func TestBackendSubmitterValidationDoesNotBlockSubmit(t *testing.T) {
	client := &recordingClient{}
	result := &Result{
		AgentUUID: "node-1",
		Success:   true,
		// Missing nonce refresh timestamp and evidences intentionally to trigger validation issues.
		SDKResponse: SDKResponse{
			ResultCode: 200,
		},
	}

	err := NewBackendSubmitter(client).Submit(context.Background(), result, "jwt-token")
	require.NoError(t, err)
	require.Equal(t, "node-1", client.lastAgentUUID)
	require.NotNil(t, client.lastReq)
	require.True(t, client.lastReq.AttestationData.Success)
}

func TestStateJWTProviderAndAgentUUIDProviderErrors(t *testing.T) {
	_, err := NewStateJWTProvider(nil).GetJWT(context.Background())
	require.ErrorContains(t, err, "requires agent state")
	err = NewStateJWTProvider(nil).SetJWT(context.Background(), "x")
	require.ErrorContains(t, err, "requires agent state")

	_, err = NewStateJWTProvider(&stubState{}).GetJWT(context.Background())
	require.ErrorContains(t, err, "jwt not available")

	_, err = NewStateAgentUUIDProvider(nil)(context.Background())
	require.ErrorContains(t, err, "requires agent state")
	_, err = NewStateAgentUUIDProvider(&stubState{})(context.Background())
	require.ErrorContains(t, err, "agent UUID not available")
}

func TestSleepWithContext(t *testing.T) {
	require.NoError(t, sleepWithContext(context.Background(), 0))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, sleepWithContext(ctx, 0), context.Canceled)

	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, sleepWithContext(ctx, time.Hour), context.Canceled)
}

func TestAttestationStartupJitterHelper(t *testing.T) {
	require.Equal(t, time.Duration(0), calculateJitter(0))
	jitter := calculateJitter(50 * time.Millisecond)
	require.GreaterOrEqual(t, jitter, time.Duration(0))
	require.Less(t, jitter, 50*time.Millisecond)
}

func TestManagerRunUsesRetryIntervalWhenNotEnrolled(t *testing.T) {
	var attempts atomic.Int32
	mgr := NewManager(
		func(context.Context) (string, error) {
			attempts.Add(1)
			return "", ErrNotEnrolled
		},
		&testJWTProvider{jwt: "jwt-token"},
		&testNonceProvider{nonce: "abc123"},
		&testEvidenceCollector{resp: &SDKResponse{ResultCode: 200}},
		&testSubmitter{},
		AttestationConfig{
			Interval:      time.Hour,
			RetryInterval: 5 * time.Millisecond,
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := mgr.Run(ctx)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.GreaterOrEqual(t, attempts.Load(), int32(2))
	require.GreaterOrEqual(t, elapsed, 15*time.Millisecond)
	require.Less(t, elapsed, 100*time.Millisecond)
}
