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

package enrollment

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/fleet-intelligence-agent/internal/agentstate"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/backendclient"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/config"
	"github.com/NVIDIA/fleet-intelligence-agent/internal/nodeidentity"
)

type fakeBackendClient struct {
	enrollSAK string
	enrollJWT string
	enrollErr error
}

func strPtr(value string) *string {
	return &value
}

func (f *fakeBackendClient) Enroll(_ context.Context, sakToken string) (string, error) {
	f.enrollSAK = sakToken
	return f.enrollJWT, f.enrollErr
}

func (f *fakeBackendClient) UpsertNode(context.Context, string, *backendclient.NodeUpsertRequest, string) (*backendclient.NodeUpsertResponse, error) {
	return nil, nil
}

func (f *fakeBackendClient) GetNonce(context.Context, string, string) (*backendclient.NonceResponse, error) {
	return nil, nil
}

func (f *fakeBackendClient) SubmitAttestation(context.Context, string, *backendclient.AttestationRequest, string) error {
	return nil
}

func TestEnrollWorkflow(t *testing.T) {
	originalFactory := newBackendClient
	originalSync := syncInventoryAfterEnroll
	originalEnsure := ensureNodeUUID
	t.Cleanup(func() {
		newBackendClient = originalFactory
		syncInventoryAfterEnroll = originalSync
		ensureNodeUUID = originalEnsure
	})

	client := &fakeBackendClient{enrollJWT: "jwt-token"}
	newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
		require.Equal(t, "https://example.com", rawBaseURL)
		return client, nil
	}

	syncCalled := false
	ensureCalled := false
	ensureNodeUUID = func(ctx context.Context, state nodeidentity.State) (string, error) {
		require.NotNil(t, state)
		ensureCalled = true
		return "4c4c4544-0053-5210-8038-c8c04f583034", nil
	}
	syncInventoryAfterEnroll = func(ctx context.Context, cfg *config.Config) error {
		require.True(t, ensureCalled, "node UUID should be initialized before post-enroll inventory sync")
		syncCalled = true
		return nil
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	err := Enroll(context.Background(), "https://example.com", "sak-token")
	require.NoError(t, err)
	require.Equal(t, "sak-token", client.enrollSAK)
	require.True(t, ensureCalled)
	require.True(t, syncCalled)
}

func TestEnrollWorkflowPassesConfigToInventorySync(t *testing.T) {
	originalFactory := newBackendClient
	originalSync := syncInventoryAfterEnroll
	t.Cleanup(func() {
		newBackendClient = originalFactory
		syncInventoryAfterEnroll = originalSync
	})

	newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
		return &fakeBackendClient{enrollJWT: "jwt-token"}, nil
	}

	wantCfg := &config.Config{
		Inventory: &config.InventoryConfig{
			Enabled: true,
		},
	}
	syncInventoryAfterEnroll = func(ctx context.Context, gotCfg *config.Config) error {
		require.Same(t, wantCfg, gotCfg)
		return nil
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	err := EnrollWithConfig(context.Background(), "https://example.com", "sak-token", wantCfg)
	require.NoError(t, err)
}

func TestEnrollWorkflowStoresOptionalMetadata(t *testing.T) {
	originalFactory := newBackendClient
	originalSync := syncInventoryAfterEnroll
	t.Cleanup(func() {
		newBackendClient = originalFactory
		syncInventoryAfterEnroll = originalSync
	})

	newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
		return &fakeBackendClient{enrollJWT: "jwt-token"}, nil
	}
	syncInventoryAfterEnroll = func(context.Context, *config.Config) error { return nil }

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	err := EnrollWithConfigAndMetadata(
		context.Background(),
		"https://example.com",
		"sak-token",
		nil,
		&EnrollMetadata{
			NodeGroup:   strPtr("  nodegroup-a  "),
			ComputeZone: strPtr("  zone-a "),
		},
	)
	require.NoError(t, err)

	state := agentstate.NewSQLite()
	nodeGroup, ok, err := state.GetNodeGroup(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "nodegroup-a", nodeGroup)

	computeZone, ok, err := state.GetComputeZone(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "zone-a", computeZone)
}

func TestStoreConfigInMetadataPreservesOptionalMetadataWhenUnset(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test expects non-root default state path resolution")
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	ctx := context.Background()

	err := storeConfigInMetadata(
		ctx,
		"https://example.com",
		"jwt-token",
		"sak-token",
		time.Now().UTC(),
		EnrollMetadata{
			NodeGroup:   strPtr("group-a"),
			ComputeZone: strPtr("zone-a"),
		},
	)
	require.NoError(t, err)

	err = storeConfigInMetadata(
		ctx,
		"https://example.com",
		"jwt-token-2",
		"sak-token-2",
		time.Now().UTC(),
		EnrollMetadata{},
	)
	require.NoError(t, err)

	state := agentstate.NewSQLite()
	nodeGroup, ok, err := state.GetNodeGroup(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "group-a", nodeGroup)

	computeZone, ok, err := state.GetComputeZone(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "zone-a", computeZone)
}

func TestStoreConfigInMetadataClearsOptionalMetadataWhenSetToEmpty(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test expects non-root default state path resolution")
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	ctx := context.Background()

	err := storeConfigInMetadata(
		ctx,
		"https://example.com",
		"jwt-token",
		"sak-token",
		time.Now().UTC(),
		EnrollMetadata{
			NodeGroup:   strPtr("group-a"),
			ComputeZone: strPtr("zone-a"),
		},
	)
	require.NoError(t, err)

	err = storeConfigInMetadata(
		ctx,
		"https://example.com",
		"jwt-token-2",
		"sak-token-2",
		time.Now().UTC(),
		EnrollMetadata{
			NodeGroup:   strPtr(""),
			ComputeZone: strPtr(""),
		},
	)
	require.NoError(t, err)

	state := agentstate.NewSQLite()
	nodeGroup, ok, err := state.GetNodeGroup(ctx)
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, nodeGroup)

	computeZone, ok, err := state.GetComputeZone(ctx)
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, computeZone)
}

func TestEnrollWorkflowDefaultWrapperUsesNilConfig(t *testing.T) {
	originalFactory := newBackendClient
	originalSync := syncInventoryAfterEnroll
	t.Cleanup(func() {
		newBackendClient = originalFactory
		syncInventoryAfterEnroll = originalSync
	})

	newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
		return &fakeBackendClient{enrollJWT: "jwt-token"}, nil
	}

	syncInventoryAfterEnroll = func(ctx context.Context, gotCfg *config.Config) error {
		require.Nil(t, gotCfg)
		return nil
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	err := Enroll(context.Background(), "https://example.com", "sak-token")
	require.NoError(t, err)
}

func TestEnrollWorkflowNormalizesLegacyEndpointToBaseURL(t *testing.T) {
	originalFactory := newBackendClient
	originalSync := syncInventoryAfterEnroll
	t.Cleanup(func() {
		newBackendClient = originalFactory
		syncInventoryAfterEnroll = originalSync
	})

	client := &fakeBackendClient{enrollJWT: "jwt-token"}
	newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
		require.Equal(t, "https://example.com", rawBaseURL)
		return client, nil
	}
	syncInventoryAfterEnroll = func(context.Context, *config.Config) error { return nil }

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	err := Enroll(context.Background(), "https://example.com/api/v1/health/metrics", "sak-token")
	require.NoError(t, err)
	require.Equal(t, "sak-token", client.enrollSAK)
}

func TestEnrollWorkflowErrors(t *testing.T) {
	t.Run("invalid endpoint", func(t *testing.T) {
		err := Enroll(context.Background(), "http://example.com", "sak-token")
		require.Error(t, err)
	})

	t.Run("localhost http endpoint allowed", func(t *testing.T) {
		originalFactory := newBackendClient
		t.Cleanup(func() { newBackendClient = originalFactory })

		called := false
		newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
			called = true
			require.Equal(t, "http://localhost:8080", rawBaseURL)
			return &fakeBackendClient{enrollJWT: "jwt-token"}, nil
		}

		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		err := Enroll(context.Background(), "http://localhost:8080", "sak-token")
		require.NoError(t, err)
		require.True(t, called)
	})

	t.Run("backend client creation", func(t *testing.T) {
		originalFactory := newBackendClient
		t.Cleanup(func() { newBackendClient = originalFactory })
		newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
			return nil, errors.New("factory boom")
		}

		err := Enroll(context.Background(), "https://example.com", "sak-token")
		require.ErrorContains(t, err, "failed to create backend client")
	})

	t.Run("enroll error", func(t *testing.T) {
		originalFactory := newBackendClient
		t.Cleanup(func() { newBackendClient = originalFactory })
		newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
			return &fakeBackendClient{enrollErr: errors.New("enroll boom")}, nil
		}

		err := Enroll(context.Background(), "https://example.com", "sak-token")
		require.ErrorContains(t, err, "enroll boom")
	})

	t.Run("node UUID initialization", func(t *testing.T) {
		originalFactory := newBackendClient
		originalEnsure := ensureNodeUUID
		originalSync := syncInventoryAfterEnroll
		originalStore := storeEnrollmentConfig
		t.Cleanup(func() {
			newBackendClient = originalFactory
			ensureNodeUUID = originalEnsure
			syncInventoryAfterEnroll = originalSync
			storeEnrollmentConfig = originalStore
		})
		newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
			return &fakeBackendClient{enrollJWT: "jwt-token"}, nil
		}
		ensureNodeUUID = func(context.Context, nodeidentity.State) (string, error) {
			return "", errors.New("identity store failed")
		}
		syncInventoryAfterEnroll = func(context.Context, *config.Config) error {
			t.Fatal("inventory sync should not run when node UUID initialization fails")
			return nil
		}
		storeEnrollmentConfig = func(context.Context, string, string, string, time.Time, EnrollMetadata) error {
			t.Fatal("enrollment metadata should not be stored when node UUID initialization fails")
			return nil
		}

		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		err := Enroll(context.Background(), "https://example.com", "sak-token")
		require.ErrorContains(t, err, "failed to initialize node UUID")
	})

	t.Run("localhost legacy endpoint allowed", func(t *testing.T) {
		originalFactory := newBackendClient
		originalSync := syncInventoryAfterEnroll
		t.Cleanup(func() {
			newBackendClient = originalFactory
			syncInventoryAfterEnroll = originalSync
		})

		called := false
		newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
			called = true
			require.Equal(t, "http://localhost:8080", rawBaseURL)
			return &fakeBackendClient{enrollJWT: "jwt-token"}, nil
		}
		syncInventoryAfterEnroll = func(context.Context, *config.Config) error { return nil }

		tmpHome := t.TempDir()
		t.Setenv("HOME", tmpHome)

		err := Enroll(context.Background(), "http://localhost:8080/api/v1/health/enroll", "sak-token")
		require.NoError(t, err)
		require.True(t, called)
	})
}

func TestEnrollWorkflowInventorySyncFailureIsNonFatal(t *testing.T) {
	originalFactory := newBackendClient
	originalSync := syncInventoryAfterEnroll
	t.Cleanup(func() {
		newBackendClient = originalFactory
		syncInventoryAfterEnroll = originalSync
	})

	newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
		return &fakeBackendClient{enrollJWT: "jwt-token"}, nil
	}
	syncInventoryAfterEnroll = func(ctx context.Context, cfg *config.Config) error {
		return errors.New("inventory failed")
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	err := Enroll(context.Background(), "https://example.com", "sak-token")
	require.NoError(t, err)
}

func TestEnrollWorkflowInventorySyncTimeoutIsNonFatal(t *testing.T) {
	originalFactory := newBackendClient
	originalSync := syncInventoryAfterEnroll
	originalTimeout := postEnrollInventorySyncTimeout
	t.Cleanup(func() {
		newBackendClient = originalFactory
		syncInventoryAfterEnroll = originalSync
		postEnrollInventorySyncTimeout = originalTimeout
	})

	newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
		return &fakeBackendClient{enrollJWT: "jwt-token"}, nil
	}
	postEnrollInventorySyncTimeout = 10 * time.Millisecond

	syncStarted := make(chan struct{})
	releaseSync := make(chan struct{})
	syncInventoryAfterEnroll = func(context.Context, *config.Config) error {
		close(syncStarted)
		<-releaseSync
		return nil
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	start := time.Now()
	err := Enroll(context.Background(), "https://example.com", "sak-token")
	require.NoError(t, err)
	require.Less(t, time.Since(start), time.Second)

	select {
	case <-syncStarted:
	case <-time.After(time.Second):
		t.Fatal("inventory sync did not start")
	}
	close(releaseSync)
}

func TestEnrollWorkflowInventorySyncUsesRemainingEnrollTimeout(t *testing.T) {
	originalFactory := newBackendClient
	originalSync := syncInventoryAfterEnroll
	originalTimeout := postEnrollInventorySyncTimeout
	t.Cleanup(func() {
		newBackendClient = originalFactory
		syncInventoryAfterEnroll = originalSync
		postEnrollInventorySyncTimeout = originalTimeout
	})

	newBackendClient = func(rawBaseURL string) (backendclient.Client, error) {
		return &fakeBackendClient{enrollJWT: "jwt-token"}, nil
	}
	postEnrollInventorySyncTimeout = time.Minute

	syncInventoryAfterEnroll = func(ctx context.Context, cfg *config.Config) error {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.Less(t, time.Until(deadline), 5*time.Second)
		return nil
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := Enroll(ctx, "https://example.com", "sak-token")
	require.NoError(t, err)
}

func TestStoreConfigInMetadataSecuresFreshStateFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test expects non-root default state path resolution")
	}

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	err := storeConfigInMetadata(
		context.Background(),
		"https://example.com",
		"jwt-token",
		"sak-token",
		time.Now().UTC(),
		EnrollMetadata{},
	)
	require.NoError(t, err)

	stateFile, err := config.DefaultStateFile()
	require.NoError(t, err)
	for _, candidate := range []string{stateFile, stateFile + "-wal", stateFile + "-shm"} {
		info, err := os.Stat(candidate)
		if os.IsNotExist(err) {
			if candidate == stateFile {
				require.NoError(t, err)
			}
			continue
		}
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}
