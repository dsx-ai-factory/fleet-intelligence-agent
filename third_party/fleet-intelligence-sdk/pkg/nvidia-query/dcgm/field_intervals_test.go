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

package dcgm

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
	"github.com/stretchr/testify/require"
)

func TestCollectionIntervalDefaultsAndOverrides(t *testing.T) {
	for _, id := range []dcgm.Short{1, 1110, 1199, 1220, 1403, 1420, 65535} {
		require.Equal(t, 30*time.Second, FieldInterval(id, 30*time.Second, CollectionIntervals{}))
	}
	for _, id := range []dcgm.Short{1200, 1219, 1404, 1419} {
		require.Equal(t, time.Minute, FieldInterval(id, 30*time.Second, CollectionIntervals{}))
	}
	for _, test := range []struct{ main, extended, standardWant, extendedWant time.Duration }{
		{30 * time.Second, 0, 30 * time.Second, time.Minute},
		{10 * time.Second, 0, 10 * time.Second, time.Minute},
		{90 * time.Second, 0, 90 * time.Second, 90 * time.Second},
		{5 * time.Second, 10 * time.Second, 5 * time.Second, 10 * time.Second},
		{10 * time.Second, 5 * time.Second, 10 * time.Second, 10 * time.Second},
		{30 * time.Second, 90 * time.Second, 30 * time.Second, 90 * time.Second},
		{0, 0, 30 * time.Second, time.Minute},
	} {
		intervals := CollectionIntervals{Extended: test.extended}
		require.Equal(t, test.standardWant, FieldInterval(100, test.main, intervals))
		require.Equal(t, test.extendedWant, FieldInterval(1200, test.main, intervals))
	}
}

func TestFieldProfileAssignments(t *testing.T) {
	mappings, err := ParseFieldProfiles(" 1110 = extended , 1200-1219 = standard , 65535=extended")
	require.NoError(t, err)
	profiles := CollectionIntervals{FieldProfiles: mappings}
	require.Equal(t, time.Minute, FieldInterval(1110, 30*time.Second, profiles))
	require.Equal(t, 30*time.Second, FieldInterval(1200, 30*time.Second, profiles))
	require.Equal(t, 30*time.Second, FieldInterval(1219, 30*time.Second, profiles))
	require.Equal(t, time.Minute, FieldInterval(1404, time.Second, profiles), "omitted fields retain their default assignment")
	require.Equal(t, time.Minute, FieldInterval(65535, time.Second, profiles))
	require.Equal(t, 2*time.Minute, FieldInterval(1110, 2*time.Minute, profiles))
	for _, value := range []string{"0=standard", "65536=standard", "2-1=standard", "1=custom", "1=30s", "1=standard,1=extended", "1-3=standard,3=extended", "1=standard,", "1", "1-2-3=standard", "1=standard=extended"} {
		result, err := ParseFieldProfiles(value)
		require.Error(t, err, value)
		require.Nil(t, result)
	}
	empty, err := ParseFieldProfiles("")
	require.NoError(t, err)
	require.Empty(t, empty)
	// SDK callers cannot create profiles by putting arbitrary names in the map.
	require.Equal(t, time.Minute, FieldInterval(1200, time.Second, CollectionIntervals{FieldProfiles: map[dcgm.Short]FieldProfile{1200: "custom"}}))
}

func TestFieldWatchesUsesConfiguredProfiles(t *testing.T) {
	profiles := CollectionIntervals{Extended: 10 * time.Second}
	w := NewFieldWatches([]dcgm.Short{100, 1200, 1404}, 5*time.Second, profiles)
	fakeWatches(w)
	var frequencies []int64
	watch := w.watch
	w.watch = func(h dcgm.FieldHandle, g dcgm.GroupHandle, frequency int64, age float64, samples int32) error {
		frequencies = append(frequencies, frequency)
		return watch(h, g, frequency, age, samples)
	}
	require.NoError(t, w.Setup("configured-profiles", dcgm.GroupHandle{}))
	require.Equal(t, []int64{5_000_000, 10_000_000}, frequencies)
	require.Equal(t, []dcgm.Short{100, 1200, 1404}, w.Fields(), "cached reads use the complete selected field list")
	require.NoError(t, w.Close())
}

func TestFieldWatchesRetainsFieldsAcrossReconnect(t *testing.T) {
	w := NewFieldWatches([]dcgm.Short{1404, 1200, 100, 100}, 30*time.Second, CollectionIntervals{})
	created, watched, _, destroyed := fakeWatches(w)
	require.NoError(t, w.Setup("reconnect", dcgm.GroupHandle{}))
	require.NoError(t, w.Setup("reconnect", dcgm.GroupHandle{}))
	require.Len(t, *watched, 2, "ready watches must not be restarted on each cached read")
	require.Equal(t, []dcgm.Short{100, 1200, 1404}, w.Fields())
	w.Reset()
	require.Equal(t, []dcgm.Short{100, 1200, 1404}, w.Fields())
	require.NoError(t, w.Setup("reconnect", dcgm.GroupHandle{}))
	require.Len(t, *created, 4)
	require.NoError(t, w.Close())
	require.ElementsMatch(t, []uintptr{3, 4}, *destroyed, "only handles from the current session are released")
}

func TestFieldWatchesGroupCountIsBounded(t *testing.T) {
	profiles := CollectionIntervals{FieldProfiles: make(map[dcgm.Short]FieldProfile)}
	var fields []dcgm.Short
	for id := dcgm.Short(1); id <= 100; id++ {
		fields = append(fields, id)
		if id%2 == 0 {
			profiles.FieldProfiles[id] = ExtendedProfile
		} else {
			profiles.FieldProfiles[id] = StandardProfile
		}
	}
	s := NewFieldWatches(fields, 30*time.Second, profiles)
	require.Len(t, s.groups, 2, "many field assignments must not create more intervals")
	created, _, _, _ := fakeWatches(s)
	require.NoError(t, s.Setup("bounded-profiles", dcgm.GroupHandle{}))
	require.Len(t, *created, 2)
	s.Close()
	// A longer component interval merges distinct profile intervals.
	longer := NewFieldWatches(fields, 2*time.Minute, profiles)
	require.Len(t, longer.groups, 1)
	require.Equal(t, 2*time.Minute, longer.groups[0].interval)
	// Equal profile intervals also share one group.
	profiles.Extended = 30 * time.Second
	require.Len(t, NewFieldWatches(fields, 30*time.Second, profiles).groups, 1)
}

func TestFieldProfileAssignmentsDoNotEnableFields(t *testing.T) {
	profiles := CollectionIntervals{FieldProfiles: map[dcgm.Short]FieldProfile{999: ExtendedProfile}}
	s := NewFieldWatches([]dcgm.Short{100}, time.Second, profiles)
	require.Len(t, s.groups, 1)
	require.Equal(t, []dcgm.Short{100}, s.groups[0].fields)
	empty := NewFieldWatches(nil, time.Second, profiles)
	created, _, _, _ := fakeWatches(empty)
	require.NoError(t, empty.Setup("no-enabled-fields", dcgm.GroupHandle{}))
	require.Empty(t, *created)
}

func TestFieldWatchesRejectsOversizedGroupBeforeSetup(t *testing.T) {
	var fields []dcgm.Short
	for id := 1; id <= maxFieldsPerGroup+1; id++ {
		fields = append(fields, dcgm.Short(id))
	}
	s := NewFieldWatches(fields, 30*time.Second, CollectionIntervals{})
	created, _, _, _ := fakeWatches(s)
	require.ErrorContains(t, s.Setup("oversized", dcgm.GroupHandle{}), "maximum is 128")
	require.Empty(t, *created, "invalid field groups must not allocate resources")
}

// fakeWatches records actual watch requests and cleanup without DCGM hardware.
func fakeWatches(s *FieldWatches) (created, watched, unwatched, destroyed *[]uintptr) {
	created = &[]uintptr{}
	watched = &[]uintptr{}
	unwatched = &[]uintptr{}
	destroyed = &[]uintptr{}
	s.create = func(_ string, _ []dcgm.Short) (dcgm.FieldHandle, error) {
		var h dcgm.FieldHandle
		h.SetHandle(uintptr(len(*created) + 1))
		*created = append(*created, h.GetHandle())
		return h, nil
	}
	s.watch = func(h dcgm.FieldHandle, _ dcgm.GroupHandle, frequency int64, age float64, samples int32) error {
		*watched = append(*watched, h.GetHandle())
		if frequency != int64((s.groups[(len(*watched)-1)%len(s.groups)].interval+time.Microsecond-1)/time.Microsecond) || samples != 3 || age != s.groups[(len(*watched)-1)%len(s.groups)].interval.Seconds()*2 {
			return fmt.Errorf("invalid watch parameters")
		}
		return nil
	}
	s.unwatch = func(h dcgm.FieldHandle, _ dcgm.GroupHandle) error {
		*unwatched = append(*unwatched, h.GetHandle())
		return nil
	}
	s.destroy = func(h dcgm.FieldHandle) error { *destroyed = append(*destroyed, h.GetHandle()); return nil }
	return
}

func TestFieldWatchesRollbackRetryAndCleanup(t *testing.T) {
	for _, failure := range []string{"create", "watch"} {
		t.Run(failure, func(t *testing.T) {
			s := NewFieldWatches([]dcgm.Short{100, 1200}, 30*time.Second, CollectionIntervals{})
			created, _, unwatched, destroyed := fakeWatches(s)
			create, watch := s.create, s.watch
			s.create = func(name string, fields []dcgm.Short) (dcgm.FieldHandle, error) {
				if failure == "create" && len(*created) == 1 {
					return dcgm.FieldHandle{}, fmt.Errorf("create failed")
				}
				return create(name, fields)
			}
			s.watch = func(h dcgm.FieldHandle, g dcgm.GroupHandle, f int64, a float64, n int32) error {
				if failure == "watch" && h.GetHandle() == 2 {
					return fmt.Errorf("watch failed")
				}
				return watch(h, g, f, a, n)
			}
			require.Error(t, s.Setup("test", dcgm.GroupHandle{}))
			require.Len(t, *unwatched, 1)
			require.Equal(t, len(*created), len(*destroyed))
			for _, b := range s.groups {
				require.Zero(t, b.handle.GetHandle())
			}
			fakeWatches(s)
			require.NoError(t, s.Setup("test", dcgm.GroupHandle{}))
			require.NoError(t, s.Setup("test", dcgm.GroupHandle{}))
			s.Close()
			s.Close()
			for _, b := range s.groups {
				require.Zero(t, b.handle.GetHandle())
			}
		})
	}
}

type intervalCacheInstance struct {
	mockDCGMInstance
	fields       []dcgm.Short
	fieldQueries int
	read         func(uint, []dcgm.Short) ([]dcgm.FieldValue_v1, error)
}

func (m *intervalCacheInstance) GetWatchedFields() []dcgm.Short {
	m.fieldQueries++
	return m.fields
}
func (m *intervalCacheInstance) GetLatestValuesForFields(id uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
	return m.read(id, fields)
}

func TestFieldCachePollsAllFieldsAndRecovers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var reads [][]dcgm.Short
		fail := false
		standardTS, extendedTS := int64(1), int64(1)
		inst := &intervalCacheInstance{mockDCGMInstance: mockDCGMInstance{dcgmExists: true, devices: []DeviceInfo{{ID: 1, UUID: "gpu"}}}, fields: []dcgm.Short{100, 1200}}
		inst.read = func(_ uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
			reads = append(reads, append([]dcgm.Short(nil), fields...))
			if fail {
				return nil, fmt.Errorf("read failed")
			}
			return []dcgm.FieldValue_v1{{FieldID: 100, TS: standardTS}, {FieldID: 1200, TS: extendedTS}}, nil
		}
		fc := NewFieldValueCache(context.Background(), inst, 30*time.Second)
		fc.fieldWatches = NewFieldWatches(inst.fields, 30*time.Second, CollectionIntervals{})
		_, watched, _, destroyed := fakeWatches(fc.fieldWatches)
		assertSamples := func(standard, extended int64) {
			t.Helper()
			result, err := fc.GetResult(inst.fields)
			require.NoError(t, err)
			require.Len(t, result, 1)
			require.Len(t, result[0].Values, 2)
			require.Equal(t, standard, result[0].Values[0].TS)
			require.Equal(t, extended, result[0].Values[1].TS)
		}
		require.NoError(t, fc.Poll())
		assertSamples(1, 1)
		time.Sleep(30 * time.Second)
		standardTS = 2
		require.NoError(t, fc.Poll())
		assertSamples(2, 1)
		require.Equal(t, [][]dcgm.Short{{100, 1200}, {100, 1200}}, reads)
		require.Len(t, *watched, 2, "cache reads must not restart the native sampling watches")
		time.Sleep(30 * time.Second)
		fail = true
		require.Error(t, fc.Poll())
		for _, field := range inst.fields {
			_, err := fc.GetResult([]dcgm.Short{field})
			require.ErrorContains(t, err, "read failed")
		}
		time.Sleep(30 * time.Second)
		fail = false
		standardTS, extendedTS = 3, 2
		require.NoError(t, fc.Poll())
		assertSamples(3, 2)
		fc.resetAfterReconnect()
		result, err := fc.GetResult(inst.fields)
		require.NoError(t, err)
		require.Empty(t, result, "a reconnect clears samples from the previous session")
		require.NoError(t, fc.Poll())
		assertSamples(3, 2)
		require.Zero(t, inst.fieldQueries, "the watch owner retains the selected fields")
		for _, fields := range reads {
			require.Equal(t, inst.fields, fields)
		}

		inst.devices = append(inst.devices, DeviceInfo{ID: 2, UUID: "gpu-2"})
		entered, release := make(chan struct{}), make(chan struct{})
		inst.read = func(_ uint, _ []dcgm.Short) ([]dcgm.FieldValue_v1, error) { close(entered); <-release; return nil, nil }
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = fc.Poll() }()
		<-entered
		go func() { defer wg.Done(); fc.Stop() }()
		<-fc.ctx.Done()
		close(release)
		wg.Wait()
		fc.Stop()
		require.ErrorIs(t, fc.Poll(), context.Canceled)
		require.Len(t, *destroyed, 2)
	})
}

func TestFieldCachePublishesCompleteCachedBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inst := &intervalCacheInstance{
			mockDCGMInstance: mockDCGMInstance{dcgmExists: true, devices: []DeviceInfo{{ID: 1}, {ID: 2}}},
			fields:           []dcgm.Short{100, 1200},
		}
		valuesAt := func(fields []dcgm.Short, timestamp int64) []dcgm.FieldValue_v1 {
			values := make([]dcgm.FieldValue_v1, 0, len(fields))
			for _, field := range fields {
				sampleTS := timestamp
				if field == 1200 {
					sampleTS = 1 // DCGM can return the same extended sample on consecutive polls.
				}
				values = append(values, dcgm.FieldValue_v1{FieldID: field, TS: sampleTS})
			}
			return values
		}
		inst.read = func(_ uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
			return valuesAt(fields, 1), nil
		}
		fc := NewFieldValueCache(context.Background(), inst, 30*time.Second)
		fc.fieldWatches = NewFieldWatches(inst.fields, 30*time.Second, CollectionIntervals{})
		fakeWatches(fc.fieldWatches)
		defer fc.Stop()
		require.NoError(t, fc.Poll())

		assertSnapshot := func(fast, slow int64) {
			t.Helper()
			result, err := fc.GetResult(inst.fields)
			require.NoError(t, err)
			require.Len(t, result, 2)
			for _, device := range result {
				require.Len(t, device.Values, 2)
				require.Equal(t, fast, device.Values[0].TS)
				require.Equal(t, slow, device.Values[1].TS)
			}
		}
		assertSnapshot(1, 1)
		time.Sleep(30 * time.Second)
		entered, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer unblock()
		inst.read = func(id uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
			if id == 2 {
				close(entered)
				<-release
			}
			return valuesAt(fields, 2), nil
		}
		polled := make(chan error, 1)
		go func() { polled <- fc.Poll() }()
		<-entered
		// The first device has returned new samples, but readers must still
		// receive the complete previous snapshot until the poll finishes.
		assertSnapshot(1, 1)
		unblock()
		require.NoError(t, <-polled)
		assertSnapshot(2, 1)
	})
}

func TestFieldWatchesRetriesFailedCleanupBeforeAllocation(t *testing.T) {
	for _, operation := range []string{"unwatch", "destroy"} {
		t.Run(operation, func(t *testing.T) {
			s := NewFieldWatches([]dcgm.Short{100, 1200}, 30*time.Second, CollectionIntervals{})
			created, _, _, destroyed := fakeWatches(s)
			require.NoError(t, s.Setup("cleanup", dcgm.GroupHandle{}))
			remaining := 2
			unwatch, destroy := s.unwatch, s.destroy
			s.unwatch = func(h dcgm.FieldHandle, g dcgm.GroupHandle) error {
				if operation == "unwatch" && h.GetHandle() == 1 && remaining > 0 {
					remaining--
					return fmt.Errorf("unwatch failed")
				}
				return unwatch(h, g)
			}
			s.destroy = func(h dcgm.FieldHandle) error {
				if operation == "destroy" && h.GetHandle() == 1 && remaining > 0 {
					remaining--
					return fmt.Errorf("destroy failed")
				}
				return destroy(h)
			}
			require.Error(t, s.Close())
			require.NotZero(t, s.groups[0].handle.GetHandle())
			require.Equal(t, operation == "unwatch", s.groups[0].watching)
			require.Error(t, s.Setup("cleanup", dcgm.GroupHandle{}))
			require.Len(t, *created, 2, "pending cleanup must prevent new allocation")
			require.NoError(t, s.Setup("cleanup", dcgm.GroupHandle{}))
			require.Len(t, *created, 4)
			require.NoError(t, s.Close())
			require.NoError(t, s.Close())
			require.ElementsMatch(t, *created, *destroyed)
		})
	}
}

func TestFieldWatchesRetainsRollbackCleanupFailure(t *testing.T) {
	s := NewFieldWatches([]dcgm.Short{100, 1200}, 30*time.Second, CollectionIntervals{})
	created, _, _, destroyed := fakeWatches(s)
	s.watch = func(h dcgm.FieldHandle, _ dcgm.GroupHandle, _ int64, _ float64, _ int32) error {
		if h.GetHandle() == 2 {
			return fmt.Errorf("watch failed")
		}
		return nil
	}
	remaining := 2
	destroy := s.destroy
	s.destroy = func(h dcgm.FieldHandle) error {
		if h.GetHandle() == 1 && remaining > 0 {
			remaining--
			return fmt.Errorf("destroy failed")
		}
		return destroy(h)
	}
	err := s.Setup("rollback", dcgm.GroupHandle{})
	require.ErrorContains(t, err, "watch failed")
	require.ErrorContains(t, err, "destroy failed")
	require.Error(t, s.Setup("rollback", dcgm.GroupHandle{}))
	require.Len(t, *created, 2)
	require.NoError(t, s.Setup("rollback", dcgm.GroupHandle{}))
	require.NoError(t, s.Close())
	require.ElementsMatch(t, *created, *destroyed)
}

func TestFieldCacheTransientAndEmptyReadsClearFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inst := &intervalCacheInstance{mockDCGMInstance: mockDCGMInstance{dcgmExists: true, devices: []DeviceInfo{{ID: 1}}}, fields: []dcgm.Short{100}}
		readErr := fmt.Errorf("read failed")
		inst.read = func(_ uint, _ []dcgm.Short) ([]dcgm.FieldValue_v1, error) { return nil, readErr }
		fc := NewFieldValueCache(context.Background(), inst, 30*time.Second)
		fc.fieldWatches = NewFieldWatches(inst.fields, 30*time.Second, CollectionIntervals{})
		fakeWatches(fc.fieldWatches)
		defer fc.Stop()
		require.Error(t, fc.Poll())
		time.Sleep(30 * time.Second)
		readErr = fmt.Errorf("dcgm request failed with error code %d", dcgm.DCGM_ST_NO_DATA)
		require.NoError(t, fc.Poll(), "transient errors do not create a new poll error")
		values, err := fc.GetResult(inst.fields)
		require.NoError(t, err, "transient errors do not retain the previous poll error")
		require.Empty(t, values, "failed device reads do not retain stale samples")
		time.Sleep(30 * time.Second)
		readErr = fmt.Errorf("read failed")
		require.Error(t, fc.Poll())
		readErr = nil
		require.NoError(t, fc.Poll())
		_, err = fc.GetResult(inst.fields)
		require.NoError(t, err, "an empty value list clears the previous poll error")
		readErr = fmt.Errorf("read failed")
		require.Error(t, fc.Poll())
		inst.devices = nil
		require.NoError(t, fc.Poll())
		_, err = fc.GetResult(inst.fields)
		require.NoError(t, err, "a poll without devices clears the previous poll error")
	})
}

func TestFieldWatchesRoundsSamplingIntervalUp(t *testing.T) {
	minimum := 30*time.Second + time.Nanosecond
	w := NewFieldWatches([]dcgm.Short{100}, minimum, CollectionIntervals{})
	fakeWatches(w)
	watch := w.watch
	w.watch = func(h dcgm.FieldHandle, g dcgm.GroupHandle, frequency int64, age float64, samples int32) error {
		require.Equal(t, int64(30_000_001), frequency)
		require.GreaterOrEqual(t, time.Duration(frequency)*time.Microsecond, minimum)
		return watch(h, g, frequency, age, samples)
	}
	require.NoError(t, w.Setup("rounding", dcgm.GroupHandle{}))
	require.NoError(t, w.Close())
}
