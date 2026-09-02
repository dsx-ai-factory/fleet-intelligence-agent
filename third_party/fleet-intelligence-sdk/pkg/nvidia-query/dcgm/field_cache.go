// SPDX-FileCopyrightText: Copyright (c) 2024, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
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
	"os"
	"sync"
	"time"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
)

// FieldValueCache manages centralized DCGM field watching and caching.
// Polls field values in the background and provides cached results to components.
type FieldValueCache struct {
	ctx      context.Context
	cancel   context.CancelFunc
	instance Instance

	mu         sync.RWMutex
	values     map[uint]map[dcgm.Short]dcgm.FieldValue_v1 // deviceID -> fieldID -> value
	lastUpdate time.Time
	lastError  error

	fieldGroupID   dcgm.FieldHandle
	fieldGroupName string
	pollInterval   time.Duration
	started        bool
	startOnce      sync.Once
	registrationMu sync.Mutex
}

// NewFieldValueCache creates a placeholder cache. Call SetupFieldWatching() after components register fields.
func NewFieldValueCache(ctx context.Context, instance Instance, pollInterval time.Duration) *FieldValueCache {
	cctx, ccancel := context.WithCancel(ctx)

	fc := &FieldValueCache{
		ctx:            cctx,
		cancel:         ccancel,
		instance:       instance,
		values:         make(map[uint]map[dcgm.Short]dcgm.FieldValue_v1),
		fieldGroupName: "fleetint-default-gpu-fields",
		pollInterval:   pollInterval,
	}

	if registrar, ok := instance.(reconnectCallbackRegistrar); ok {
		registrar.RegisterReconnectCallback(fc.resetAfterReconnect)
	}

	return fc
}

// SetupFieldWatching creates the field group and starts DCGM watching for all registered fields.
// For tests, use SetupFieldWatchingWithName to provide a unique name.
func (fc *FieldValueCache) SetupFieldWatching() error {
	return fc.SetupFieldWatchingWithName(fc.fieldGroupName)
}

// SetupFieldWatchingWithName creates the field group with a custom name.
// This is useful for tests to avoid naming conflicts when running in parallel.
func (fc *FieldValueCache) SetupFieldWatchingWithName(fieldGroupName string) error {
	fc.mu.Lock()
	fc.fieldGroupName = fieldGroupName
	fc.mu.Unlock()

	return fc.ensureFieldWatchingSetup()
}

func (fc *FieldValueCache) ensureFieldWatchingSetup() error {
	fc.registrationMu.Lock()
	defer fc.registrationMu.Unlock()

	if fc.fieldGroupID.GetHandle() != 0 {
		return nil
	}

	if fc.instance == nil || !fc.instance.DCGMExists() {
		log.Logger.Debugw("DCGM not available, skipping field watching setup")
		return nil
	}

	watchedFields := fc.instance.GetWatchedFields()
	if len(watchedFields) == 0 {
		log.Logger.Debugw("no fields registered, skipping field watching setup")
		return nil
	}

	fc.mu.RLock()
	fieldGroupName := fc.fieldGroupName
	fc.mu.RUnlock()

	fieldGroupID, err := dcgm.FieldGroupCreate(fieldGroupName, watchedFields)
	if err != nil {
		setupErr := fmt.Errorf("failed to create DCGM field group: %w", err)
		// Store error so GetResult() returns it to components
		fc.mu.Lock()
		fc.lastError = setupErr
		fc.mu.Unlock()
		return setupErr
	}
	fc.fieldGroupID = fieldGroupID

	updateFreqMicroseconds := int64(fc.pollInterval / time.Microsecond)
	maxKeepAge := fc.pollInterval.Seconds() * 2
	maxKeepSamples := int32(3)

	err = dcgm.WatchFieldsWithGroupEx(fieldGroupID, fc.instance.GetGroupHandle(),
		updateFreqMicroseconds, maxKeepAge, maxKeepSamples)
	if err != nil {
		dcgm.FieldGroupDestroy(fieldGroupID)
		setupErr := fmt.Errorf("failed to set up DCGM field watching: %w", err)
		// Store error so GetResult() returns it to components
		fc.mu.Lock()
		fc.lastError = setupErr
		fc.mu.Unlock()
		return setupErr
	}

	log.Logger.Infow("set up DCGM field watching with centralized field group",
		"updateFreq", fc.pollInterval,
		"maxKeepAge", maxKeepAge,
		"numFields", len(watchedFields))

	return nil
}

// Start begins background polling. Requires SetupFieldWatching() to be called first.
func (fc *FieldValueCache) Start() error {
	var startErr error
	fc.startOnce.Do(func() {
		fc.registrationMu.Lock()
		fc.started = true
		fc.registrationMu.Unlock()

		if err := fc.Poll(); err != nil {
			log.Logger.Warnw("initial poll failed", "error", err)
		}

		go fc.pollLoop()

		log.Logger.Infow("field cache polling started", "interval", fc.pollInterval)
	})

	return startErr
}

// Stop stops polling and destroys the field group.
func (fc *FieldValueCache) Stop() {
	fc.cancel()

	fc.registrationMu.Lock()
	fieldGroupID := fc.fieldGroupID
	fc.fieldGroupID = dcgm.FieldHandle{}
	fc.registrationMu.Unlock()

	if fc.instance != nil && fc.instance.DCGMExists() && fieldGroupID.GetHandle() != 0 {
		if err := dcgm.FieldGroupDestroy(fieldGroupID); err != nil {
			log.Logger.Warnw("failed to destroy field group", "error", err)
		}
	}
}

func (fc *FieldValueCache) resetAfterReconnect() {
	fc.registrationMu.Lock()
	fc.fieldGroupID = dcgm.FieldHandle{}
	fc.registrationMu.Unlock()

	fc.mu.Lock()
	fc.values = make(map[uint]map[dcgm.Short]dcgm.FieldValue_v1)
	fc.lastError = nil
	fc.lastUpdate = time.Time{}
	fc.mu.Unlock()
}

func (fc *FieldValueCache) pollLoop() {
	ticker := time.NewTicker(fc.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-fc.ctx.Done():
			return
		case <-ticker.C:
			if err := fc.Poll(); err != nil {
				log.Logger.Warnw("field polling failed", "error", err)
			}
		}
	}
}

// Poll queries DCGM and updates the cache. Used by background polling and scan mode.
func (fc *FieldValueCache) Poll() error {
	if fc.instance == nil {
		return fmt.Errorf("DCGM instance is nil")
	}

	if !fc.instance.DCGMExists() {
		// DCGM may become available after startup; keep polling until it is ready.
		log.Logger.Debugw("DCGM not available yet, skipping field poll")
		return nil
	}

	if err := fc.ensureFieldWatchingSetup(); err != nil {
		return err
	}

	watchedFields := fc.instance.GetWatchedFields()
	if len(watchedFields) == 0 {
		log.Logger.Debugw("no fields registered with DCGM, skipping field poll")
		return nil
	}

	devices := fc.instance.GetDevices()
	newValues := make(map[uint]map[dcgm.Short]dcgm.FieldValue_v1)
	var pollErr error

	for _, device := range devices {
		fieldValues, err := dcgm.GetLatestValuesForFields(device.ID, watchedFields)
		if err != nil {
			// Check for fatal errors that require restart
			if IsRestartRequired(err) {
				log.Logger.Errorw("DCGM fatal error, exiting for restart",
					"component", "field_cache",
					"deviceID", device.ID,
					"error", err,
					"action", "systemd/k8s will restart agent and recreate DCGM resources")
				os.Exit(1)
			}

			// Check if this is a transient error (benign, don't store)
			if IsTransientError(err) {
				log.Logger.Infow("DCGM transient error, will retry",
					"component", "field_cache",
					"deviceID", device.ID,
					"error", err)
				continue // Skip this device, try next
			}

			// Store error with priority: unhealthy > unknown
			// Wrap error with device context for better debugging
			if pollErr == nil {
				// No error stored yet, store this one
				pollErr = fmt.Errorf("device %d: %w", device.ID, err)
			} else if IsUnhealthyAPIError(err) && !IsUnhealthyAPIError(pollErr) {
				// Replace unknown error with unhealthy error (higher priority)
				pollErr = fmt.Errorf("device %d: %w", device.ID, err)
			}

			// Continue polling other devices regardless of error type
			continue
		}

		deviceValues := make(map[dcgm.Short]dcgm.FieldValue_v1)
		for _, fieldValue := range fieldValues {
			deviceValues[fieldValue.FieldID] = fieldValue
		}
		newValues[device.ID] = deviceValues
	}

	fc.mu.Lock()
	fc.values = newValues
	fc.lastUpdate = time.Now()
	fc.lastError = pollErr
	fc.mu.Unlock()

	log.Logger.Debugw("field values cached", "devices", len(newValues), "fields", len(watchedFields))
	if pollErr != nil {
		return fmt.Errorf("DCGM error during field polling: %w", pollErr)
	}
	return nil
}

// DeviceFieldValues represents field values for a single device with metadata.
type DeviceFieldValues struct {
	DeviceID uint
	UUID     string
	Values   []dcgm.FieldValue_v1
}

// GetResult returns field values for all devices. Primary API for components.
func (fc *FieldValueCache) GetResult(fields []dcgm.Short) ([]DeviceFieldValues, error) {
	return fc.getResult(fields, false)
}

// GetResultPreservingFieldErrors returns requested fields even when DCGM set a
// non-success field status. GetResult normally removes sentinel payloads, and
// failed fields commonly use those sentinels. Persistence-mode compatibility
// needs the status to distinguish GPU-lost/reset-required from an unsupported
// field, so it opts into retaining these entries.
//
// A failed field's payload may be a sentinel and must not be decoded. Callers
// must check FieldValue_v1.Status before reading its value.
func (fc *FieldValueCache) GetResultPreservingFieldErrors(fields []dcgm.Short) ([]DeviceFieldValues, error) {
	return fc.getResult(fields, true)
}

func (fc *FieldValueCache) getResult(fields []dcgm.Short, preserveFieldErrors bool) ([]DeviceFieldValues, error) {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	// Check lastError first - it contains more specific error information
	if fc.lastError != nil {
		return nil, fc.lastError
	}

	if fc.instance == nil {
		return nil, fmt.Errorf("DCGM instance not available")
	}

	devices := fc.instance.GetDevices()
	result := make([]DeviceFieldValues, 0, len(devices))

	for _, device := range devices {
		deviceValues, exists := fc.values[device.ID]
		if !exists {
			continue
		}

		fieldValues := make([]dcgm.FieldValue_v1, 0, len(fields))
		for _, fieldID := range fields {
			if fieldValue, exists := deviceValues[fieldID]; exists {
				if preserveFieldErrors && fieldValue.Status != dcgm.DCGM_ST_OK {
					fieldValues = append(fieldValues, fieldValue)
					continue
				}
				if isSentinel := CheckSentinel(fieldValue,
					"deviceID", device.ID,
					"uuid", device.UUID,
				); isSentinel {
					continue
				}
				fieldValues = append(fieldValues, fieldValue)
			}
		}

		if len(fieldValues) > 0 {
			result = append(result, DeviceFieldValues{
				DeviceID: device.ID,
				UUID:     device.UUID,
				Values:   fieldValues,
			})
		}
	}

	return result, nil
}
