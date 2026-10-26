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
	// lastError records the last setup or cached-read error. A complete
	// successful poll clears it; all selected fields are read together.
	lastError error

	// fieldWatches owns the DCGM sampling groups for registered fields.
	// It is initialized after enabled components register their fields.
	fieldWatches *FieldWatches
	// watchIntervals retains settings until deferred field-watch construction.
	watchIntervals CollectionIntervals
	fieldGroupName string
	pollInterval   time.Duration
	startOnce      sync.Once
	// registrationMu serializes setup, reads, reconnect, and cleanup.
	registrationMu sync.Mutex
}

// NewFieldValueCache creates a placeholder cache. Call SetupFieldWatching() after components register fields.
func NewFieldValueCache(ctx context.Context, instance Instance, pollInterval time.Duration) *FieldValueCache {
	return NewFieldValueCacheWithIntervals(ctx, instance, pollInterval, CollectionIntervals{})
}

// NewFieldValueCacheWithIntervals configures native sampling watches for registered fields.
// All selected cached fields are read on the pollInterval cycle.
func NewFieldValueCacheWithIntervals(ctx context.Context, instance Instance, pollInterval time.Duration, watchIntervals CollectionIntervals) *FieldValueCache {
	cctx, ccancel := context.WithCancel(ctx)
	if pollInterval <= 0 {
		pollInterval = 30 * time.Second
	}

	fc := &FieldValueCache{
		ctx:            cctx,
		watchIntervals: watchIntervals,
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
	return fc.setupLocked()
}

func (fc *FieldValueCache) setupLocked() error {
	if fc.ctx.Err() != nil {
		return context.Canceled
	}

	if fc.instance == nil || !fc.instance.DCGMExists() {
		log.Logger.Debugw("DCGM not available, skipping field watching setup")
		return nil
	}

	// The watch owner retains the selected fields across setup retries and reconnects.
	if fc.fieldWatches == nil {
		watchedFields := fc.instance.GetWatchedFields()
		if len(watchedFields) == 0 {
			log.Logger.Debugw("no fields registered, skipping field watching setup")
			return nil
		}
		fc.fieldWatches = NewFieldWatches(watchedFields, fc.pollInterval, fc.watchIntervals)
	}

	fc.mu.RLock()
	fieldGroupName := fc.fieldGroupName
	fc.mu.RUnlock()

	if err := fc.fieldWatches.Setup(fieldGroupName, fc.instance.GetGroupHandle()); err != nil {
		fc.mu.Lock()
		fc.lastError = err
		fc.mu.Unlock()
		return err
	}

	return nil
}

// Start begins background polling. Requires SetupFieldWatching() to be called first.
func (fc *FieldValueCache) Start() error {
	var startErr error
	fc.startOnce.Do(func() {
		if err := fc.Poll(); err != nil {
			log.Logger.Warnw("initial poll failed", "error", err)
		}

		go fc.pollLoop()

		log.Logger.Infow("field cache polling started", "interval", fc.pollInterval)
	})

	return startErr
}

// Stop cancels polling and releases all field groups after any in-flight poll.
func (fc *FieldValueCache) Stop() {
	fc.cancel()

	fc.registrationMu.Lock()
	defer fc.registrationMu.Unlock()
	if fc.fieldWatches != nil {
		_ = fc.fieldWatches.Close()
	}
}

func (fc *FieldValueCache) resetAfterReconnect() {
	fc.registrationMu.Lock()
	if fc.fieldWatches != nil {
		fc.fieldWatches.Reset()
	}
	defer fc.registrationMu.Unlock()

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

// Poll reads all selected fields from DCGM's cache. Used by background polling and scan mode.
func (fc *FieldValueCache) Poll() error {
	fc.registrationMu.Lock()
	defer fc.registrationMu.Unlock()
	if fc.ctx.Err() != nil {
		return context.Canceled
	}
	if fc.instance == nil {
		return fmt.Errorf("DCGM instance is nil")
	}

	if !fc.instance.DCGMExists() {
		// DCGM may become available after startup; keep polling until it is ready.
		log.Logger.Debugw("DCGM not available yet, skipping field poll")
		return nil
	}

	// Inventory identity fields are queried live only until the fixed device
	// membership has a complete snapshot. Sharing this cadence avoids a second
	// background ticker and keeps reconnect responsible only for session setup.
	if enricher, ok := fc.instance.(deviceInventoryEnricher); ok {
		if err := enricher.enrichDeviceInventoryIfIncomplete(); err != nil {
			exitForRestartIfRequired("device_inventory", err)
			log.Logger.Warnw("DCGM device inventory enrichment retry failed", "error", err)
		}
	}

	if err := fc.setupLocked(); err != nil {
		return err
	}

	if fc.fieldWatches == nil {
		return nil
	}
	watchedFields := fc.fieldWatches.Fields()
	if len(watchedFields) == 0 {
		log.Logger.Debugw("no selected fields, skipping field poll")
		return nil
	}

	devices := fc.instance.GetDevices()
	newValues := make(map[uint][]dcgm.FieldValue_v1, len(devices))
	var pollErr error
	readComplete := len(devices) > 0

	for _, device := range devices {
		if err := fc.ctx.Err(); err != nil {
			return err
		}
		fieldValues, err := fc.instance.GetLatestValuesForFields(device.ID, watchedFields)
		if err != nil {
			exitForRestartIfRequired("field_cache", err, "deviceID", device.ID)

			// Check if this is a transient error (benign, don't store)
			if IsTransientError(err) {
				readComplete = false
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

		newValues[device.ID] = fieldValues
	}

	fc.mu.Lock()
	// Publish the full batch under one lock. Replace old samples even when a
	// read fails, so unavailable values cannot appear as current samples.
	for _, device := range devices {
		deviceValues := fc.values[device.ID]
		clear(deviceValues)
		if deviceValues == nil && len(newValues[device.ID]) > 0 {
			deviceValues = make(map[dcgm.Short]dcgm.FieldValue_v1, len(newValues[device.ID]))
			fc.values[device.ID] = deviceValues
		}
		for _, fieldValue := range newValues[device.ID] {
			deviceValues[fieldValue.FieldID] = fieldValue
		}
	}
	fc.lastUpdate = time.Now()
	if pollErr != nil {
		fc.lastError = pollErr
	} else if readComplete {
		fc.lastError = nil
	}
	fc.mu.Unlock()

	log.Logger.Debugw("field values cached", "devices", len(devices), "fields", len(watchedFields))
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
