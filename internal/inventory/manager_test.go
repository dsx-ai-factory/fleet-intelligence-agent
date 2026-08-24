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

package inventory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeSource struct {
	mu        sync.Mutex
	snapshots []*Snapshot
	index     int
}

func (f *fakeSource) Collect(context.Context) (*Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.snapshots) == 0 {
		return nil, nil
	}
	if f.index >= len(f.snapshots) {
		last := *f.snapshots[len(f.snapshots)-1]
		return &last, nil
	}
	snap := *f.snapshots[f.index]
	f.index++
	return &snap, nil
}

type fakeSink struct {
	mu       sync.Mutex
	exported []*Snapshot
	ready    chan struct{}
	err      error
}

func (f *fakeSink) Export(_ context.Context, snap *Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cloned := *snap
	f.exported = append(f.exported, &cloned)
	if f.ready != nil {
		select {
		case f.ready <- struct{}{}:
		default:
		}
	}
	if f.err != nil {
		return f.err
	}
	return nil
}

func TestManagerCollectOnceExportsEverySnapshot(t *testing.T) {
	src := &fakeSource{
		snapshots: []*Snapshot{
			{
				CollectedAt: time.Unix(100, 0).UTC(),
				Hostname:    "host-a",
				MachineID:   "machine-id",
				Resources: Resources{
					CPUInfo: CPUInfo{Type: "Xeon", LogicalCores: 64},
				},
			},
			{
				CollectedAt: time.Unix(200, 0).UTC(),
				Hostname:    "host-a",
				MachineID:   "machine-id",
				Resources: Resources{
					CPUInfo: CPUInfo{Type: "Xeon", LogicalCores: 64},
				},
			},
			{
				CollectedAt: time.Unix(300, 0).UTC(),
				Hostname:    "host-b",
				MachineID:   "machine-id",
				Resources: Resources{
					CPUInfo: CPUInfo{Type: "Xeon", LogicalCores: 64},
				},
			},
		},
	}
	sink := &fakeSink{}
	mgr := NewManager(src, sink, InventoryConfig{})

	snap1, err := mgr.CollectOnce(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, snap1.InventoryHash)
	require.Len(t, sink.exported, 1)

	snap2, err := mgr.CollectOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, snap1.InventoryHash, snap2.InventoryHash)
	require.Len(t, sink.exported, 2)

	snap3, err := mgr.CollectOnce(context.Background())
	require.NoError(t, err)
	require.NotEqual(t, snap1.InventoryHash, snap3.InventoryHash)
	require.Len(t, sink.exported, 3)
}

func TestManagerCollectOnceConcurrentExportsEverySnapshot(t *testing.T) {
	src := &fakeSource{
		snapshots: []*Snapshot{{
			CollectedAt: time.Unix(100, 0).UTC(),
			Hostname:    "host-a",
			MachineID:   "machine-id",
			Resources: Resources{
				CPUInfo: CPUInfo{Type: "Xeon", LogicalCores: 64},
			},
		}},
	}
	sink := &fakeSink{}
	mgr := NewManager(src, sink, InventoryConfig{})

	var (
		wg   sync.WaitGroup
		errs = make(chan error, 8)
	)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := mgr.CollectOnce(context.Background())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	require.Len(t, sink.exported, 8)
}

func TestManagerCollectOnceReturnsNotReadyForRetryScheduling(t *testing.T) {
	src := &fakeSource{
		snapshots: []*Snapshot{{MachineID: "machine-1", Hostname: "host-a"}},
	}
	sink := &fakeSink{err: ErrNotReady}
	mgr := NewManager(src, sink, InventoryConfig{})

	snap, err := mgr.CollectOnce(context.Background())
	require.ErrorIs(t, err, ErrNotReady)
	require.NotNil(t, snap)
	require.Len(t, sink.exported, 1)
}
