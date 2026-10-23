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

func TestFieldScheduleUsesConfiguredProfiles(t *testing.T) {
	profiles := CollectionIntervals{Extended: 10 * time.Second}
	s := NewFieldSchedule([]dcgm.Short{100, 1200, 1404}, 5*time.Second, profiles)
	require.Len(t, s.buckets, 2)
	require.Equal(t, 5*time.Second, s.buckets[0].interval)
	require.Equal(t, 10*time.Second, s.buckets[1].interval)
	fakeSchedule(s)
	require.NoError(t, s.Setup("configured-profiles", dcgm.GroupHandle{}))
	now := time.Unix(100, 0)
	require.Equal(t, []dcgm.Short{100, 1200, 1404}, s.Due(now))
	require.Empty(t, s.Due(now.Add(5*time.Second-time.Nanosecond)))
	require.Equal(t, []dcgm.Short{100}, s.Due(now.Add(5*time.Second)))
	require.Equal(t, []dcgm.Short{100, 1200, 1404}, s.Due(now.Add(10*time.Second)))
	s.Close()
}

func TestFieldScheduleIndependentCadences(t *testing.T) {
	s := NewFieldSchedule([]dcgm.Short{1404, 1200, 100, 100}, 30*time.Second, CollectionIntervals{})
	now := time.Unix(100, 0)
	require.Equal(t, []dcgm.Short{100, 1200, 1404}, s.Due(now))
	require.Empty(t, s.Due(now.Add(30*time.Second-time.Nanosecond)))
	require.Equal(t, []dcgm.Short{100}, s.Due(now.Add(30*time.Second)))
	s.Reset()
	require.Empty(t, s.Due(now.Add(31*time.Second)))
	require.Equal(t, []dcgm.Short{100, 1200, 1404}, s.Due(now.Add(time.Minute)))
	require.Equal(t, []dcgm.Short{100}, s.Due(now.Add(90*time.Second)))
	require.Equal(t, []dcgm.Short{100, 1200, 1404}, s.Due(now.Add(10*time.Minute)))
	require.Empty(t, s.Due(now.Add(10*time.Minute)))
}

func TestFieldScheduleGroupCountIsBounded(t *testing.T) {
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
	s := NewFieldSchedule(fields, 30*time.Second, profiles)
	require.Len(t, s.buckets, 2, "many field assignments must not create more intervals")
	created, _, _, _ := fakeSchedule(s)
	require.NoError(t, s.Setup("bounded-profiles", dcgm.GroupHandle{}))
	require.Len(t, *created, 2)
	s.Close()
	// A longer component interval merges distinct profile intervals.
	longer := NewFieldSchedule(fields, 2*time.Minute, profiles)
	require.Len(t, longer.buckets, 1)
	require.Equal(t, 2*time.Minute, longer.buckets[0].interval)
	// Equal profile intervals also share one group.
	profiles.Extended = 30 * time.Second
	require.Len(t, NewFieldSchedule(fields, 30*time.Second, profiles).buckets, 1)
}

func TestFieldProfileAssignmentsDoNotEnableFields(t *testing.T) {
	profiles := CollectionIntervals{FieldProfiles: map[dcgm.Short]FieldProfile{999: ExtendedProfile}}
	s := NewFieldSchedule([]dcgm.Short{100}, time.Second, profiles)
	require.Len(t, s.buckets, 1)
	require.Equal(t, []dcgm.Short{100}, s.buckets[0].fields)
	empty := NewFieldSchedule(nil, time.Second, profiles)
	created, _, _, _ := fakeSchedule(empty)
	require.NoError(t, empty.Setup("no-enabled-fields", dcgm.GroupHandle{}))
	require.Empty(t, *created)
}

func TestFieldScheduleRejectsOversizedGroupBeforeSetup(t *testing.T) {
	var fields []dcgm.Short
	for id := 1; id <= maxFieldsPerGroup+1; id++ {
		fields = append(fields, dcgm.Short(id))
	}
	s := NewFieldSchedule(fields, 30*time.Second, CollectionIntervals{})
	created, _, _, _ := fakeSchedule(s)
	require.ErrorContains(t, s.Setup("oversized", dcgm.GroupHandle{}), "maximum is 128")
	require.Empty(t, *created, "invalid field groups must not allocate resources")
}

// fakeSchedule records actual watch requests and cleanup without DCGM hardware.
func fakeSchedule(s *FieldSchedule) (created, watched, unwatched, destroyed *[]uintptr) {
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
		if frequency != int64(s.buckets[(len(*watched)-1)%len(s.buckets)].interval/time.Microsecond) || samples != 3 || age != float64(frequency)*2/1e6 {
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

func TestFieldScheduleRollbackRetryAndCleanup(t *testing.T) {
	for _, failure := range []string{"create", "watch"} {
		t.Run(failure, func(t *testing.T) {
			s := NewFieldSchedule([]dcgm.Short{100, 1200}, 30*time.Second, CollectionIntervals{})
			created, _, unwatched, destroyed := fakeSchedule(s)
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
			for _, b := range s.buckets {
				require.Zero(t, b.handle.GetHandle())
			}
			fakeSchedule(s)
			require.NoError(t, s.Setup("test", dcgm.GroupHandle{}))
			require.NoError(t, s.Setup("test", dcgm.GroupHandle{}))
			s.Close()
			s.Close()
			for _, b := range s.buckets {
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

func TestFieldCacheIntervalsFailureRecoveryReconnectAndStop(t *testing.T) {
	// Exercise real Poll and Stop with the standard library's virtual clock.
	synctest.Test(t, func(t *testing.T) {
		var reads [][]dcgm.Short
		fail := false
		inst := &intervalCacheInstance{mockDCGMInstance: mockDCGMInstance{dcgmExists: true, devices: []DeviceInfo{{ID: 1, UUID: "gpu"}}}, fields: []dcgm.Short{100, 1200}}
		inst.read = func(_ uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
			reads = append(reads, append([]dcgm.Short(nil), fields...))
			if fail {
				return nil, fmt.Errorf("read failed")
			}
			var result []dcgm.FieldValue_v1
			for _, id := range fields {
				result = append(result, dcgm.FieldValue_v1{FieldID: id})
			}
			return result, nil
		}
		fc := NewFieldValueCache(context.Background(), inst, 30*time.Second)
		fc.schedule = NewFieldSchedule(inst.fields, 30*time.Second, CollectionIntervals{})
		_, _, _, destroyed := fakeSchedule(fc.schedule)
		require.NoError(t, fc.Poll())
		require.Equal(t, [][]dcgm.Short{{100, 1200}}, reads)
		require.NoError(t, fc.Poll())
		require.Len(t, reads, 1)
		require.Zero(t, inst.fieldQueries, "the schedule already owns the selected fields")
		time.Sleep(30 * time.Second)
		require.NoError(t, fc.Poll())
		require.Equal(t, []dcgm.Short{100}, reads[1])
		result, err := fc.GetResult([]dcgm.Short{1200})
		require.NoError(t, err)
		require.Len(t, result, 1)
		time.Sleep(30 * time.Second)
		fail = true
		require.Error(t, fc.Poll())
		require.NoError(t, fc.Poll())
		require.Len(t, reads, 3)
		_, err = fc.GetResult([]dcgm.Short{1200})
		require.Error(t, err)
		time.Sleep(30 * time.Second)
		fail = false
		require.NoError(t, fc.Poll())
		_, err = fc.GetResult([]dcgm.Short{100})
		require.NoError(t, err)
		_, err = fc.GetResult([]dcgm.Short{1200})
		require.Error(t, err, "slow-field error must survive fast-field recovery")
		fc.resetAfterReconnect()
		require.NoError(t, fc.Poll())
		require.Len(t, reads, 4, "reconnect must not bypass deadlines")
		require.Zero(t, inst.fieldQueries, "reconnect reuses the selected fields")
		time.Sleep(30 * time.Second)
		require.NoError(t, fc.Poll())
		result, err = fc.GetResult([]dcgm.Short{1200})
		require.NoError(t, err)
		require.Len(t, result, 1)
		// Stop waits for any in-flight poll, is idempotent, and prevents new reads.
		time.Sleep(time.Minute)
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

func TestFieldCachePublishesCompletePollAndRetainsSlowFields(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inst := &intervalCacheInstance{
			mockDCGMInstance: mockDCGMInstance{dcgmExists: true, devices: []DeviceInfo{{ID: 1}, {ID: 2}}},
			fields:           []dcgm.Short{100, 1200},
		}
		valuesAt := func(fields []dcgm.Short, timestamp int64) []dcgm.FieldValue_v1 {
			values := make([]dcgm.FieldValue_v1, 0, len(fields))
			for _, field := range fields {
				values = append(values, dcgm.FieldValue_v1{FieldID: field, TS: timestamp})
			}
			return values
		}
		inst.read = func(_ uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
			return valuesAt(fields, 1), nil
		}
		fc := NewFieldValueCache(context.Background(), inst, 30*time.Second)
		fc.schedule = NewFieldSchedule(inst.fields, 30*time.Second, CollectionIntervals{})
		fakeSchedule(fc.schedule)
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

func TestFieldScheduleRetriesFailedCleanupBeforeAllocation(t *testing.T) {
	for _, operation := range []string{"unwatch", "destroy"} {
		t.Run(operation, func(t *testing.T) {
			s := NewFieldSchedule([]dcgm.Short{100, 1200}, 30*time.Second, CollectionIntervals{})
			created, _, _, destroyed := fakeSchedule(s)
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
			require.NotZero(t, s.buckets[0].handle.GetHandle())
			require.Equal(t, operation == "unwatch", s.buckets[0].watching)
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

func TestFieldScheduleRetainsRollbackCleanupFailure(t *testing.T) {
	s := NewFieldSchedule([]dcgm.Short{100, 1200}, 30*time.Second, CollectionIntervals{})
	created, _, _, destroyed := fakeSchedule(s)
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

func TestFieldScheduleCompletesOnlySelectedIntervals(t *testing.T) {
	s := NewFieldSchedule([]dcgm.Short{100, 1200}, 30*time.Second, CollectionIntervals{})
	now := time.Unix(100, 0)
	fields := s.Due(now)
	s.Complete(fields, now.Add(10*time.Second))
	require.Empty(t, s.Due(now.Add(30*time.Second)))
	fields = s.Due(now.Add(40 * time.Second))
	require.Equal(t, []dcgm.Short{100}, fields)
	s.Complete(fields, now.Add(45*time.Second))
	require.Equal(t, []dcgm.Short{1200}, s.Due(now.Add(70*time.Second)), "a fast batch must not delay the slow group")
}

func TestFieldCacheReadSpacingAfterSlowBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inst := &intervalCacheInstance{
			mockDCGMInstance: mockDCGMInstance{dcgmExists: true, devices: []DeviceInfo{{ID: 1}, {ID: 2}}},
			fields:           []dcgm.Short{100, 1200},
		}
		var reads [][]dcgm.Short
		inst.read = func(_ uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
			reads = append(reads, append([]dcgm.Short(nil), fields...))
			if len(reads) == 1 {
				time.Sleep(10 * time.Second)
			}
			return nil, nil
		}
		fc := NewFieldValueCache(context.Background(), inst, 30*time.Second)
		fc.schedule = NewFieldSchedule(inst.fields, 30*time.Second, CollectionIntervals{})
		fakeSchedule(fc.schedule)
		defer fc.Stop()
		require.NoError(t, fc.Poll())
		time.Sleep(20 * time.Second)
		require.NoError(t, fc.Poll())
		require.Len(t, reads, 2, "the second device was read only 20 seconds ago")
		time.Sleep(10 * time.Second)
		require.NoError(t, fc.Poll())
		require.Equal(t, [][]dcgm.Short{{100}, {100}}, reads[2:])
		time.Sleep(30 * time.Second)
		require.NoError(t, fc.Poll())
		require.Equal(t, [][]dcgm.Short{{100, 1200}, {100, 1200}}, reads[4:])
	})
}

func TestFieldCacheTransientReadDoesNotClearFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		inst := &intervalCacheInstance{mockDCGMInstance: mockDCGMInstance{dcgmExists: true, devices: []DeviceInfo{{ID: 1}}}, fields: []dcgm.Short{100}}
		readErr := fmt.Errorf("read failed")
		inst.read = func(_ uint, _ []dcgm.Short) ([]dcgm.FieldValue_v1, error) { return nil, readErr }
		fc := NewFieldValueCache(context.Background(), inst, 30*time.Second)
		fc.schedule = NewFieldSchedule(inst.fields, 30*time.Second, CollectionIntervals{})
		fakeSchedule(fc.schedule)
		defer fc.Stop()
		require.Error(t, fc.Poll())
		time.Sleep(30 * time.Second)
		readErr = fmt.Errorf("dcgm request failed with error code %d", dcgm.DCGM_ST_NO_DATA)
		require.NoError(t, fc.Poll(), "transient errors do not create a new poll error")
		_, err := fc.GetResult(inst.fields)
		require.ErrorContains(t, err, "read failed", "an unverified recovery must preserve the earlier failure")
		time.Sleep(30 * time.Second)
		readErr = nil
		require.NoError(t, fc.Poll())
		_, err = fc.GetResult(inst.fields)
		require.NoError(t, err)
	})
}
