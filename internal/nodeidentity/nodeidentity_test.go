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

package nodeidentity

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeState struct {
	nodeUUID string
	ok       bool
	err      error
	calls    int
	commitAs string
}

func (s *fakeState) GetOrCreateNodeUUID(_ context.Context, create func() (string, error)) (string, bool, error) {
	s.calls++
	if s.err != nil {
		return "", false, s.err
	}
	if s.ok && s.nodeUUID != "" {
		return s.nodeUUID, false, nil
	}
	value, err := create()
	if err != nil {
		return "", false, err
	}
	created := s.commitAs == ""
	if s.commitAs != "" {
		value = s.commitAs
	}
	s.nodeUUID = value
	s.ok = true
	return value, created, nil
}

func overrideUUIDSources(t *testing.T, read func() (string, error), generate func() string) {
	t.Helper()

	previousRead := readHardwareUUID
	previousGenerate := newNodeUUID
	readHardwareUUID = read
	newNodeUUID = generate
	t.Cleanup(func() {
		readHardwareUUID = previousRead
		newNodeUUID = previousGenerate
	})
}

func TestNodeUUIDFromSystemUUID(t *testing.T) {
	const validUUID = "4C4C4544-0053-5210-8038-C8C04F583034"

	t.Run("uses system uuid as reported", func(t *testing.T) {
		id, err := nodeUUIDFromSystemUUID(validUUID)
		require.NoError(t, err)
		assert.Equal(t, validUUID, id)
	})

	for _, tc := range []struct {
		name       string
		systemUUID string
		wantErr    string
	}{
		{name: "missing", systemUUID: "", wantErr: "system UUID not found"},
		{name: "invalid", systemUUID: "Not Settable", wantErr: "invalid system UUID"},
		{name: "all-zero placeholder", systemUUID: "00000000-0000-0000-0000-000000000000", wantErr: "is a placeholder"},
		{name: "all-f placeholder", systemUUID: "ffffffff-ffff-ffff-ffff-ffffffffffff", wantErr: "is a placeholder"},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			id, err := nodeUUIDFromSystemUUID(tc.systemUUID)
			require.Error(t, err)
			assert.Empty(t, id)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestEnsureNodeUUID(t *testing.T) {
	const (
		dmiUUID     = "4c4c4544-0053-5210-8038-c8c04f583034"
		generatedID = "6f459d44-ee11-44c1-b633-e810390ab3f7"
	)

	t.Run("uses persisted UUID", func(t *testing.T) {
		state := &fakeState{nodeUUID: dmiUUID, ok: true}
		overrideUUIDSources(t, func() (string, error) {
			t.Fatal("hardware UUID should not be read when UUID is already persisted")
			return "", nil
		}, func() string {
			t.Fatal("random UUID should not be generated when UUID is already persisted")
			return ""
		})

		id, err := EnsureNodeUUID(context.Background(), state)
		require.NoError(t, err)
		assert.Equal(t, dmiUUID, id)
		assert.Equal(t, 1, state.calls)
	})

	t.Run("persists DMI product UUID", func(t *testing.T) {
		state := &fakeState{}
		overrideUUIDSources(t, func() (string, error) {
			return dmiUUID, nil
		}, func() string {
			return generatedID
		})

		id, err := EnsureNodeUUID(context.Background(), state)
		require.NoError(t, err)
		assert.Equal(t, dmiUUID, id)
		assert.Equal(t, dmiUUID, state.nodeUUID)
		assert.Equal(t, 1, state.calls)
	})

	t.Run("persists random UUID when DMI fails", func(t *testing.T) {
		state := &fakeState{}
		overrideUUIDSources(t, func() (string, error) {
			return "", errors.New("dmi unavailable")
		}, func() string {
			return generatedID
		})

		id, err := EnsureNodeUUID(context.Background(), state)
		require.NoError(t, err)
		assert.Equal(t, generatedID, id)
		assert.Equal(t, generatedID, state.nodeUUID)
		assert.Equal(t, 1, state.calls)
	})

	t.Run("returns committed UUID from state", func(t *testing.T) {
		const committedID = "3758d746-9bad-4f47-b240-ab73d6f68c9a"
		state := &fakeState{commitAs: committedID}
		overrideUUIDSources(t, func() (string, error) {
			return dmiUUID, nil
		}, func() string {
			return generatedID
		})

		id, err := EnsureNodeUUID(context.Background(), state)
		require.NoError(t, err)
		assert.Equal(t, committedID, id)
		assert.Equal(t, committedID, state.nodeUUID)
		assert.Equal(t, 1, state.calls)
	})

	t.Run("propagates state error", func(t *testing.T) {
		state := &fakeState{err: errors.New("write failed")}
		overrideUUIDSources(t, func() (string, error) {
			return dmiUUID, nil
		}, func() string {
			return generatedID
		})

		id, err := EnsureNodeUUID(context.Background(), state)
		require.Error(t, err)
		assert.Empty(t, id)
		assert.Contains(t, err.Error(), "failed to ensure node UUID")
	})
}
