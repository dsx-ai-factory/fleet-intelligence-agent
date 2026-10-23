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
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
)

const (
	defaultStandardInterval = 30 * time.Second
	defaultExtendedInterval = time.Minute
	// maxFieldsPerGroup matches DCGM_MAX_FIELD_IDS_PER_FIELD_GROUP in go-dcgm.
	maxFieldsPerGroup = 128
)

// FieldProfile names a supported collection profile. The supported set is fixed
// in code so field assignments cannot create an unbounded number of field groups.
type FieldProfile string

const (
	StandardProfile FieldProfile = "standard"
	ExtendedProfile FieldProfile = "extended"
)

// CollectionIntervals configures the extended minimum and field assignments.
// Standard fields use the component interval supplied by the caller. Settings
// never enable fields. A zero extended interval uses the 60s default.
type CollectionIntervals struct {
	// Extended is the extended profile minimum (default 60s).
	Extended time.Duration
	// FieldProfiles changes only listed assignments. Omitted fields retain their
	// built-in profile. This map is read-only after collector construction.
	FieldProfiles map[dcgm.Short]FieldProfile
}

func parseFieldProfile(value string) (FieldProfile, error) {
	profile := FieldProfile(strings.TrimSpace(value))
	switch profile {
	case StandardProfile, ExtendedProfile:
		return profile, nil
	default:
		return "", fmt.Errorf("unknown DCGM interval profile %q; use standard or extended", value)
	}
}

// ParseFieldProfiles parses comma-separated ID=profile or FIRST-LAST=profile
// assignments. Omitted fields keep their built-in profile. Unknown profiles and
// overlapping entries are rejected. Assignments do not enable field collection.
func ParseFieldProfiles(value string) (map[dcgm.Short]FieldProfile, error) {
	result := make(map[dcgm.Short]FieldProfile)
	if strings.TrimSpace(value) == "" {
		return result, nil
	}
	for _, entry := range strings.Split(value, ",") {
		parts := strings.Split(entry, "=")
		if len(parts) != 2 {
			return nil, fmt.Errorf("expected field ID or range=profile")
		}
		profile, err := parseFieldProfile(parts[1])
		if err != nil {
			return nil, err
		}
		bounds := strings.Split(strings.TrimSpace(parts[0]), "-")
		if len(bounds) > 2 {
			return nil, fmt.Errorf("invalid field range")
		}
		first, err := strconv.ParseUint(strings.TrimSpace(bounds[0]), 10, 16)
		if err != nil || first == 0 {
			return nil, fmt.Errorf("invalid field ID")
		}
		last := first
		if len(bounds) == 2 {
			last, err = strconv.ParseUint(strings.TrimSpace(bounds[1]), 10, 16)
			if err != nil || last < first {
				return nil, fmt.Errorf("invalid field range")
			}
		}
		for id := first; id <= last; id++ {
			field := dcgm.Short(id)
			if _, exists := result[field]; exists {
				return nil, fmt.Errorf("overlapping field profile for %d", id)
			}
			result[field] = profile
		}
	}
	return result, nil
}

// FieldInterval returns the component interval for standard fields. Extended
// fields use max(component interval, extended minimum). Field assignments do not
// enable collection. Unknown SDK profile names leave the built-in assignment intact.
func FieldInterval(field dcgm.Short, requested time.Duration, intervals CollectionIntervals) time.Duration {
	if requested <= 0 {
		requested = defaultStandardInterval
	}
	profile := StandardProfile
	if (field >= 1200 && field <= 1219) || (field >= 1404 && field <= 1419) {
		profile = ExtendedProfile
	}
	if configured := intervals.FieldProfiles[field]; configured == StandardProfile || configured == ExtendedProfile {
		profile = configured
	}
	if profile == StandardProfile {
		return requested
	}
	extended := intervals.Extended
	if extended <= 0 {
		extended = defaultExtendedInterval
	}
	if extended > requested {
		return extended
	}
	return requested
}

// FieldSchedule owns interval-specific watches. Its owner must serialize calls,
// including Reset and Close, with reads and shutdown.
type FieldSchedule struct {
	// buckets partitions only enabled fields by effective interval, allowing
	// slower fields to use a longer DCGM watch without slowing faster fields.
	buckets []fieldBucket
	// group is the GPU or CPU entity group; unwatch must use the same group as setup.
	group dcgm.GroupHandle

	// Native operations default to go-dcgm. Per-instance functions let tests
	// inject partial setup and cleanup failures without a DCGM library or GPU.
	create  func(string, []dcgm.Short) (dcgm.FieldHandle, error)                  // Allocates a field group.
	watch   func(dcgm.FieldHandle, dcgm.GroupHandle, int64, float64, int32) error // Starts sampling.
	unwatch func(dcgm.FieldHandle, dcgm.GroupHandle) error                        // Stops a successfully started watch.
	destroy func(dcgm.FieldHandle) error                                          // Releases an allocated group, including setup failures.
}

// fieldBucket owns the watch and read deadline for fields with one cadence.
// Handles are session-specific; deadlines survive reconnect to prevent early retries.
type fieldBucket struct {
	// interval is the standard component interval, or a longer extended minimum.
	interval time.Duration
	// fields contains the sorted, deduplicated IDs assigned to this interval.
	fields []dcgm.Short
	// handle replaces the former single field-group handle with one per interval.
	// Zero means no group is allocated in the current DCGM session.
	handle dcgm.FieldHandle
	// next is the earliest next read attempt. It advances even when a read fails.
	// Zero permits an initial read; keeping it on reconnect prevents an early retry.
	next time.Time
	// watching distinguishes successful watch registration from group allocation
	// so rollback unwatches only watches that actually started.
	watching bool
}

// NewFieldSchedule groups selected fields by effective interval. The two supported
// profiles bound each schedule to at most two field groups. Equal effective intervals
// share one group. Profile settings never add fields to collection.
func NewFieldSchedule(fields []dcgm.Short, requested time.Duration, intervals CollectionIntervals) *FieldSchedule {
	grouped := make(map[time.Duration][]dcgm.Short)
	seen := make(map[dcgm.Short]bool)
	for _, field := range fields {
		if seen[field] {
			continue
		}
		seen[field] = true
		interval := FieldInterval(field, requested, intervals)
		grouped[interval] = append(grouped[interval], field)
	}
	s := &FieldSchedule{create: dcgm.FieldGroupCreate, watch: dcgm.WatchFieldsWithGroupEx, unwatch: dcgm.UnwatchFields, destroy: dcgm.FieldGroupDestroy}
	for interval, fields := range grouped {
		sort.Slice(fields, func(i, j int) bool { return fields[i] < fields[j] })
		s.buckets = append(s.buckets, fieldBucket{interval: interval, fields: fields})
	}
	sort.Slice(s.buckets, func(i, j int) bool { return s.buckets[i].interval < s.buckets[j].interval })
	return s
}

// Setup rolls back every created group and watch if any setup operation fails.
func (s *FieldSchedule) Setup(name string, group dcgm.GroupHandle) error {
	if len(s.buckets) == 0 {
		return nil
	}
	ready := true
	// Reject an oversized group before creating any native resources. Field lists
	// come from components, not configuration; future growth must respect this limit.
	for _, bucket := range s.buckets {
		if bucket.handle.GetHandle() == 0 || !bucket.watching {
			ready = false
		}
		if len(bucket.fields) > maxFieldsPerGroup {
			return fmt.Errorf("DCGM field group has %d fields; maximum is %d", len(bucket.fields), maxFieldsPerGroup)
		}
	}
	if ready && s.group.GetHandle() == group.GetHandle() {
		return nil
	}
	if err := s.Close(); err != nil {
		return fmt.Errorf("clean up previous field groups: %w", err)
	}
	s.group = group
	for i := range s.buckets {
		b := &s.buckets[i]
		handle, err := s.create(fmt.Sprintf("%s-%d", name, i), b.fields)
		if err != nil {
			return errors.Join(fmt.Errorf("create field group: %w", err), s.Close())
		}
		b.handle = handle
		if err := s.watch(handle, group, int64((b.interval+time.Microsecond-1)/time.Microsecond), b.interval.Seconds()*2, 3); err != nil {
			return errors.Join(fmt.Errorf("watch fields: %w", err), s.Close())
		}
		b.watching = true
	}
	return nil
}

// Due advances deadlines before attempting reads, so failures cannot cause a
// retry faster than the configured minimum. Late polls never catch up in bursts.
func (s *FieldSchedule) Due(now time.Time) []dcgm.Short {
	var fields []dcgm.Short
	for i := range s.buckets {
		b := &s.buckets[i]
		if now.Before(b.next) {
			continue
		}
		fields = append(fields, b.fields...)
		b.next = now.Add(b.interval)
	}
	return fields
}

// Complete moves the selected buckets' deadlines to the end of a read batch.
// Pass the unchanged fields returned by Due. This keeps sequential device reads
// at least one interval apart even when the batch duration varies.
func (s *FieldSchedule) Complete(fields []dcgm.Short, now time.Time) {
	for i := range s.buckets {
		b := &s.buckets[i]
		if slices.Contains(fields, b.fields[0]) {
			b.next = now.Add(b.interval)
		}
	}
}

// Reset forgets handles from a lost session but preserves deadlines.
func (s *FieldSchedule) Reset() {
	for i := range s.buckets {
		s.buckets[i].handle = dcgm.FieldHandle{}
		s.buckets[i].watching = false
	}
}

// Close unwatches and destroys allocated groups. Failed operations retain their
// handles so a later Close or Setup can retry. Read deadlines are retained.
func (s *FieldSchedule) Close() error {
	var errs []error
	for i := range s.buckets {
		b := &s.buckets[i]
		if b.handle.GetHandle() == 0 {
			continue
		}
		if b.watching {
			if err := s.unwatch(b.handle, s.group); err != nil {
				log.Logger.Warnw("failed to unwatch DCGM fields", "error", err)
				errs = append(errs, fmt.Errorf("unwatch fields: %w", err))
				continue
			}
			b.watching = false
		}
		if err := s.destroy(b.handle); err != nil {
			log.Logger.Warnw("failed to destroy DCGM field group", "error", err)
			errs = append(errs, fmt.Errorf("destroy field group: %w", err))
			continue
		}
		b.handle = dcgm.FieldHandle{}
	}
	return errors.Join(errs...)
}
