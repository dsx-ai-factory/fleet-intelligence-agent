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
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	dcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
)

var _ Instance = &instance{}

const defaultDCGMReconnectInterval = 30 * time.Second
const defaultDCGMGroupName = "fleetint-default-health"

var dcgmReconnectInterval = defaultDCGMReconnectInterval
var errReconnectAborted = errors.New("dcgm reconnect aborted")

// dcgmInitParams defines how we initialize the go-dcgm client.
type dcgmInitParams struct {
	address      string
	isUnixSocket string // "0" or "1" for go-dcgm
}

// isValidDCGMAddress returns true if addr is a plausible DCGM address:
//   - an absolute unix socket path (starts with "/")
//   - a hostname or host:port using only safe characters (no URL scheme)
func isValidDCGMAddress(addr string) bool {
	if strings.HasPrefix(addr, "/") {
		return true // unix socket path
	}
	// Reject any value that looks like a URL scheme (e.g. "http://evil.com")
	if strings.Contains(addr, "://") {
		return false
	}
	// Allow hostname characters, dots, hyphens, underscores, colons (port), and brackets (IPv6)
	for _, c := range addr {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '-', c == '_', c == ':', c == '[', c == ']':
		default:
			return false
		}
	}
	return true
}

// - If DCGM_URL is set: connect via TCP to that address.
// - Otherwise: default to TCP "localhost" (default behavior).
// - If DCGM_URL_IS_UNIX_SOCKET is truthy: treat the address as a unix socket path.
func resolveInitFromEnv() dcgmInitParams {
	// DCGM_URL can be either:
	// - TCP address, optionally including port (e.g. "dcgm-service:5555")
	// - unix socket path (e.g. "/run/dcgm/dcgm.sock")
	addr := strings.TrimSpace(os.Getenv("DCGM_URL"))
	addrInvalid := false
	if addr != "" && !isValidDCGMAddress(addr) {
		log.Logger.Warnw("DCGM_URL contains invalid characters, ignoring override and using default",
			"value", addr, "default", "localhost")
		addr = ""
		addrInvalid = true
	}

	isUnixSocketRaw := strings.TrimSpace(os.Getenv("DCGM_URL_IS_UNIX_SOCKET"))
	isUnixSocket := "0"
	if isUnixSocketRaw != "" {
		parsed, err := strconv.ParseBool(isUnixSocketRaw)
		if err == nil && parsed {
			isUnixSocket = "1"
		}
	}

	if addr == "" {
		addr = "localhost"
		// When the address override was rejected, reset the socket flag so the
		// TCP "localhost" default is not accidentally treated as a socket path.
		if addrInvalid {
			isUnixSocket = "0"
		}
	}

	return dcgmInitParams{address: addr, isUnixSocket: isUnixSocket}
}

// allHealthSystems lists all DCGM health systems
var allHealthSystems = []dcgm.HealthSystem{
	dcgm.DCGM_HEALTH_WATCH_PCIE,
	dcgm.DCGM_HEALTH_WATCH_NVLINK,
	dcgm.DCGM_HEALTH_WATCH_MEM,
	dcgm.DCGM_HEALTH_WATCH_INFOROM,
	dcgm.DCGM_HEALTH_WATCH_THERMAL,
	dcgm.DCGM_HEALTH_WATCH_POWER,
	dcgm.DCGM_HEALTH_WATCH_NVSWITCH_NONFATAL,
	dcgm.DCGM_HEALTH_WATCH_NVSWITCH_FATAL,
}

// Instance is the DCGM library connector interface.
type Instance interface {
	// DCGMExists returns true if DCGM is available.
	DCGMExists() bool

	// AddEntityToGroup adds an entity to the DCGM group.
	AddEntityToGroup(entityID uint) error

	// AddHealthWatch registers health systems to monitor.
	AddHealthWatch(system dcgm.HealthSystem) error

	// RemoveHealthWatch unregisters health systems.
	RemoveHealthWatch(system dcgm.HealthSystem) error

	// HealthCheck performs a health check for the specified system.
	HealthCheck(system dcgm.HealthSystem) (dcgm.HealthResult, []dcgm.Incident, error)

	// AddFieldsToWatch registers fields to be watched.
	AddFieldsToWatch(fields []dcgm.Short) error

	// GetWatchedFields returns all fields that have been registered.
	GetWatchedFields() []dcgm.Short

	// RemoveFieldsFromWatch unregisters fields from tracking.
	RemoveFieldsFromWatch(fields []dcgm.Short) error

	// GetLatestValuesForFields returns the latest field values for the specified device.
	// Note: This should primarily be used through FieldValueCache for better performance.
	GetLatestValuesForFields(deviceID uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error)

	// GetGroupHandle returns the DCGM group handle for use by components.
	GetGroupHandle() dcgm.GroupHandle

	// GetDevices returns the cached list of GPU devices.
	GetDevices() []DeviceInfo

	// Shutdown shuts down the DCGM library.
	Shutdown() error
}

type reconnectCallbackRegistrar interface {
	RegisterReconnectCallback(callback func())
}

type deviceInventoryEnrichmentRetrier interface {
	retryDeviceInventoryEnrichment() error
}

var newInstanceFunc = newInitializedInstance

// New creates a DCGM instance. It returns a no-op instance when a complete
// DCGM session cannot be initialized.
func New() (Instance, error) {
	return newInitializedInstance()
}

// NewWithGroupName creates a DCGM instance with a caller-owned DCGM group name.
// It has the same initialization behavior as New.
func NewWithGroupName(groupName string) (Instance, error) {
	return newInitializedInstanceWithGroupName(groupName)
}

// NewWithContext creates a reconnecting DCGM instance with a bounded initial
// wait. If a complete session cannot be initialized, it starts with a no-op
// instance so callers can continue while initialization is retried.
func NewWithContext(ctx context.Context) (Instance, error) {
	return NewWithContextAndGroupName(ctx, defaultDCGMGroupName)
}

// NewWithContextAndGroupName creates a DCGM instance with a bounded wait and
// caller-owned DCGM group name.
func NewWithContextAndGroupName(ctx context.Context, groupName string) (Instance, error) {
	type result struct {
		inst Instance
		err  error
	}

	resultCh := make(chan result, 1)
	abandonCh := make(chan struct{})
	initFn := newInstanceFunc

	go func() {
		inst, err := initFnWithGroupName(initFn, groupName)
		select {
		case resultCh <- result{inst: inst, err: err}:
		case <-abandonCh:
			if err == nil && inst != nil {
				_ = inst.Shutdown()
			}
		}
	}()

	select {
	case res := <-resultCh:
		if res.err != nil {
			return nil, res.err
		}
		if res.inst == nil || !res.inst.DCGMExists() {
			log.Logger.Warnw(
				"DCGM session unavailable at startup; continuing with no-op instance and retrying complete initialization in background",
				"retryInterval", dcgmReconnectInterval.String(),
			)
		}
		return newReconnectingInstanceWithGroupName(res.inst, dcgmReconnectInterval, groupName), nil
	case <-ctx.Done():
		close(abandonCh)
		log.Logger.Warnw(
			"DCGM initialization timed out; continuing with no-op instance and retrying complete initialization in background",
			"error", ctx.Err(),
			"retryInterval", dcgmReconnectInterval.String(),
		)
		return newReconnectingInstanceWithGroupName(NewNoOp(), dcgmReconnectInterval, groupName), nil
	}
}

func initFnWithGroupName(initFn func() (Instance, error), groupName string) (Instance, error) {
	if groupName == "" || groupName == defaultDCGMGroupName {
		return initFn()
	}
	return newInitializedInstanceWithGroupName(groupName)
}

var newConnectedInstanceFunc = func() (Instance, error) {
	return newConnectedInstance(defaultDCGMGroupName)
}

var newConnectedInstanceWithGroupNameFunc = func(groupName string) (Instance, error) {
	return newConnectedInstance(groupName)
}

var dcgmInitFunc = func(initParams dcgmInitParams) (func(), error) {
	return dcgm.Init(dcgm.Standalone, initParams.address, initParams.isUnixSocket)
}

var dcgmNewDefaultGroupFunc = dcgm.NewDefaultGroup

func newInitializedInstance() (Instance, error) {
	connectedInst, err := newConnectedInstanceFunc()
	if err != nil {
		log.Logger.Warnw("DCGM initialization failed, returning no-op instance", "error", err)
		return NewNoOp(), nil
	}
	return connectedInst, nil
}

func newInitializedInstanceWithGroupName(groupName string) (Instance, error) {
	if groupName == "" {
		groupName = defaultDCGMGroupName
	}
	connectedInst, err := connectWithGroupName(groupName)
	if err != nil {
		log.Logger.Warnw("DCGM initialization failed, returning no-op instance", "error", err)
		return NewNoOp(), nil
	}
	return connectedInst, nil
}

func connectWithGroupName(groupName string) (Instance, error) {
	if groupName == "" || groupName == defaultDCGMGroupName {
		return newConnectedInstanceFunc()
	}
	return newConnectedInstanceWithGroupNameFunc(groupName)
}

func newConnectedInstance(groupName string) (Instance, error) {
	if groupName == "" {
		groupName = defaultDCGMGroupName
	}
	initParams := resolveInitFromEnv()

	cleanup, err := dcgmInitFunc(initParams)
	if err != nil {
		return nil, err
	}

	log.Logger.Debugw("DCGM initialized successfully")

	// Build the initial inventory before creating session resources. An
	// enumeration error invalidates this candidate session so a reconnecting
	// caller can retry the complete initialization instead of publishing an
	// empty, but apparently connected, inventory.
	devices, inventoryComplete, inventoryErr := queryDeviceInventory()
	if errors.Is(inventoryErr, errDeviceEnumeration) {
		if cleanup != nil {
			cleanup()
		}
		return nil, inventoryErr
	}

	// Create group with GPUs. Components add their own entities (e.g., NVSwitch).
	groupHandle, err := dcgmNewDefaultGroupFunc(groupName)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, fmt.Errorf("failed to create custom DCGM group: %w", err)
	}

	log.Logger.Infow("created custom DCGM group for isolated health monitoring", "groupName", groupName)

	if inventoryErr != nil {
		// Enumeration succeeded, so retain the ID-only devices for presence
		// checks and retry inventory enrichment in the background.
		log.Logger.Warnw("DCGM device inventory is incomplete; will retry", "error", inventoryErr)
	} else if !inventoryComplete {
		log.Logger.Warnw("DCGM device inventory has retryable field failures; will retry")
	}
	log.Logger.Infow("cached device information", "numDevices", len(devices))

	connectedInst := &instance{
		dcgmExists:        true,
		groupHandle:       groupHandle,
		cleanup:           cleanup,
		devices:           devices,
		inventoryEnriched: inventoryComplete,
	}

	return connectedInst, nil
}

var _ Instance = &instance{}

// SystemHealthResult stores health check results for a system.
type SystemHealthResult struct {
	Health    dcgm.HealthResult
	Incidents []dcgm.Incident
}

type instance struct {
	dcgmExists  bool
	groupHandle dcgm.GroupHandle
	cleanup     func()

	devicesMu         sync.RWMutex
	devices           []DeviceInfo
	inventoryEnriched bool

	// Health watch tracking
	watchedSystemsMu sync.Mutex
	watchedSystems   dcgm.HealthSystem

	// Field watch tracking
	watchedFieldsMu sync.Mutex
	watchedFields   []dcgm.Short
}

func (inst *instance) DCGMExists() bool {
	return inst.dcgmExists
}

func (inst *instance) AddEntityToGroup(entityID uint) error {
	if err := dcgm.AddEntityToGroup(inst.groupHandle, dcgm.FE_SWITCH, entityID); err != nil {
		return fmt.Errorf("failed to add entity %d to DCGM group: %w", entityID, err)
	}
	return nil
}

func (inst *instance) GetGroupHandle() dcgm.GroupHandle {
	return inst.groupHandle
}

func (inst *instance) GetDevices() []DeviceInfo {
	inst.devicesMu.RLock()
	defer inst.devicesMu.RUnlock()
	return slices.Clone(inst.devices)
}

// retryDeviceInventoryEnrichment retries identity fields for the device IDs
// established at initialization. It never re-enumerates or changes membership.
func (inst *instance) retryDeviceInventoryEnrichment() error {
	inst.devicesMu.RLock()
	if inst.inventoryEnriched {
		inst.devicesMu.RUnlock()
		return nil
	}
	deviceIDs := make([]uint, 0, len(inst.devices))
	for _, device := range inst.devices {
		deviceIDs = append(deviceIDs, device.ID)
	}
	inst.devicesMu.RUnlock()

	devices, complete, err := queryDeviceInventoryFields(deviceIDs)
	if err != nil {
		return err
	}
	if !complete {
		return nil
	}

	inst.devicesMu.Lock()
	if !inst.inventoryEnriched {
		inst.devices = slices.Clone(devices)
		inst.inventoryEnriched = true
	}
	inst.devicesMu.Unlock()
	return nil
}

func (inst *instance) AddHealthWatch(system dcgm.HealthSystem) error {
	inst.watchedSystemsMu.Lock()
	defer inst.watchedSystemsMu.Unlock()

	newSystems := inst.watchedSystems | system

	if err := dcgm.HealthSet(inst.groupHandle, newSystems); err != nil {
		return fmt.Errorf("failed to set DCGM health watch for system 0x%x: %w", system, err)
	}

	inst.watchedSystems = newSystems
	log.Logger.Debugw("added DCGM health watch", "system", system, "totalSystems", inst.watchedSystems)
	return nil
}

func (inst *instance) RemoveHealthWatch(system dcgm.HealthSystem) error {
	inst.watchedSystemsMu.Lock()
	defer inst.watchedSystemsMu.Unlock()

	newSystems := inst.watchedSystems &^ system

	if err := dcgm.HealthSet(inst.groupHandle, newSystems); err != nil {
		return fmt.Errorf("failed to remove DCGM health watch for system 0x%x: %w", system, err)
	}

	inst.watchedSystems = newSystems
	log.Logger.Debugw("removed DCGM health watch", "system", system, "totalSystems", inst.watchedSystems)
	return nil
}

// HealthCheck performs a health check. Use DCGMHealthCache for efficient polling.
func (inst *instance) HealthCheck(system dcgm.HealthSystem) (dcgm.HealthResult, []dcgm.Incident, error) {
	healthResp, err := dcgm.HealthCheck(inst.groupHandle)
	if err != nil {
		return dcgm.DCGM_HEALTH_RESULT_FAIL, nil, fmt.Errorf("failed to perform DCGM health check: %w", err)
	}

	systemResults := make(map[dcgm.HealthSystem]SystemHealthResult)

	for _, sys := range allHealthSystems {
		systemResults[sys] = SystemHealthResult{
			Health:    dcgm.DCGM_HEALTH_RESULT_PASS,
			Incidents: nil,
		}
	}

	for _, incident := range healthResp.Incidents {
		result := systemResults[incident.System]
		result.Incidents = append(result.Incidents, incident)

		if incident.Health == dcgm.DCGM_HEALTH_RESULT_FAIL {
			result.Health = dcgm.DCGM_HEALTH_RESULT_FAIL
		} else if incident.Health == dcgm.DCGM_HEALTH_RESULT_WARN && result.Health != dcgm.DCGM_HEALTH_RESULT_FAIL {
			result.Health = dcgm.DCGM_HEALTH_RESULT_WARN
		}

		systemResults[incident.System] = result
	}

	result, exists := systemResults[system]
	if exists {
		return result.Health, result.Incidents, nil
	}
	return dcgm.DCGM_HEALTH_RESULT_PASS, nil, nil
}

// AddFieldsToWatch registers fields to be watched (tracking only).
// Field watching is set up by FieldValueCache when it's created.
func (inst *instance) AddFieldsToWatch(fields []dcgm.Short) error {
	inst.watchedFieldsMu.Lock()
	defer inst.watchedFieldsMu.Unlock()

	// Add fields, avoiding duplicates
	for _, field := range fields {
		found := false
		for _, existing := range inst.watchedFields {
			if existing == field {
				found = true
				break
			}
		}
		if !found {
			inst.watchedFields = append(inst.watchedFields, field)
		}
	}

	log.Logger.Debugw("registered fields with DCGM instance",
		"numFieldsAdded", len(fields),
		"totalFields", len(inst.watchedFields))

	return nil
}

// GetWatchedFields returns a copy of all registered fields.
// This is used by FieldValueCache to set up watching.
func (inst *instance) GetWatchedFields() []dcgm.Short {
	inst.watchedFieldsMu.Lock()
	defer inst.watchedFieldsMu.Unlock()

	// Return a defensive copy
	fieldsCopy := make([]dcgm.Short, len(inst.watchedFields))
	copy(fieldsCopy, inst.watchedFields)
	return fieldsCopy
}

// RemoveFieldsFromWatch unregisters fields from watching.
func (inst *instance) RemoveFieldsFromWatch(fields []dcgm.Short) error {
	inst.watchedFieldsMu.Lock()
	defer inst.watchedFieldsMu.Unlock()

	// Build a set of fields to remove for O(1) lookup
	toRemove := make(map[dcgm.Short]bool, len(fields))
	for _, field := range fields {
		toRemove[field] = true
	}

	// Build a new slice without the fields to remove
	newWatched := inst.watchedFields[:0]
	for _, field := range inst.watchedFields {
		if !toRemove[field] {
			newWatched = append(newWatched, field)
		}
	}
	inst.watchedFields = newWatched

	log.Logger.Debugw("unregistered fields from DCGM instance",
		"numFieldsRemoved", len(fields),
		"totalFields", len(inst.watchedFields))

	return nil
}

// GetLatestValuesForFields queries DCGM for the latest field values.
// Note: Prefer using FieldValueCache instead of calling this directly for better performance.
func (inst *instance) GetLatestValuesForFields(deviceID uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
	return dcgm.GetLatestValuesForFields(deviceID, fields)
}

func (inst *instance) Shutdown() error {
	if err := dcgm.DestroyGroup(inst.groupHandle); err != nil {
		log.Logger.Warnw("failed to destroy custom DCGM group", "error", err)
	} else {
		log.Logger.Debugw("destroyed custom DCGM group")
	}

	if inst.cleanup != nil {
		inst.cleanup()
	}
	return nil
}

var _ Instance = &reconnectingInstance{}

type reconnectingInstance struct {
	stateMu   sync.RWMutex
	currentMu sync.RWMutex
	// reconnectMu serializes reconnect and shutdown transitions.
	// Long-running connect operations happen outside this lock.
	reconnectMu sync.Mutex

	current        Instance
	watchedSystems dcgm.HealthSystem
	watchedFields  map[dcgm.Short]struct{}
	groupEntities  map[uint]struct{}
	callbacks      []func()
	shuttingDown   bool
	generation     uint64

	reconnectInterval time.Duration
	groupName         string
	stopCh            chan struct{}
	shutdownOnce      sync.Once
}

type reconnectStateSnapshot struct {
	groupEntities  []uint
	watchedSystems dcgm.HealthSystem
	watchedFields  []dcgm.Short
}

func newReconnectingInstance(initial Instance, reconnectInterval time.Duration) Instance {
	return newReconnectingInstanceWithGroupName(initial, reconnectInterval, defaultDCGMGroupName)
}

func newReconnectingInstanceWithGroupName(initial Instance, reconnectInterval time.Duration, groupName string) Instance {
	if initial == nil {
		initial = NewNoOp()
	}
	if reconnectInterval <= 0 {
		reconnectInterval = defaultDCGMReconnectInterval
	}
	if groupName == "" {
		groupName = defaultDCGMGroupName
	}

	inst := &reconnectingInstance{
		current:           initial,
		watchedFields:     make(map[dcgm.Short]struct{}),
		groupEntities:     make(map[uint]struct{}),
		reconnectInterval: reconnectInterval,
		groupName:         groupName,
		stopCh:            make(chan struct{}),
	}

	for _, field := range initial.GetWatchedFields() {
		inst.watchedFields[field] = struct{}{}
	}

	go inst.reconnectLoop()

	return inst
}

func (inst *reconnectingInstance) reconnectLoop() {
	retryAttempt := 0

	if !inst.DCGMExists() {
		log.Logger.Infow("DCGM unavailable, starting background reconnect loop", "retryInterval", inst.reconnectInterval.String())
		if err := inst.reconnectNow(); err != nil {
			if errors.Is(err, errReconnectAborted) {
				return
			}
			retryAttempt++
			log.Logger.Infow(
				"DCGM reconnect attempt failed, will retry",
				"attempt", retryAttempt,
				"retryInterval", inst.reconnectInterval.String(),
				"error", err,
			)
		}
	}

	ticker := time.NewTicker(inst.reconnectInterval)
	defer ticker.Stop()

	for {
		select {
		case <-inst.stopCh:
			return
		case <-ticker.C:
			if inst.DCGMExists() {
				if err := inst.retryDeviceInventoryEnrichment(); err != nil {
					log.Logger.Warnw("DCGM device inventory enrichment retry failed", "error", err)
				}
				continue
			}
			if err := inst.reconnectNow(); err != nil {
				if errors.Is(err, errReconnectAborted) {
					return
				}
				retryAttempt++
				log.Logger.Infow(
					"DCGM reconnect attempt failed, will retry",
					"attempt", retryAttempt,
					"retryInterval", inst.reconnectInterval.String(),
					"error", err,
				)
				continue
			}
			retryAttempt = 0
		}
	}
}

func (inst *reconnectingInstance) retryDeviceInventoryEnrichment() error {
	inst.currentMu.RLock()
	defer inst.currentMu.RUnlock()

	currentInst := inst.current
	if currentInst == nil || !currentInst.DCGMExists() {
		return nil
	}
	if retrier, ok := currentInst.(deviceInventoryEnrichmentRetrier); ok {
		return retrier.retryDeviceInventoryEnrichment()
	}
	return nil
}

func (inst *reconnectingInstance) reconnectNow() error {
	inst.reconnectMu.Lock()
	if inst.shuttingDown {
		inst.reconnectMu.Unlock()
		return errReconnectAborted
	}
	expectedGeneration := inst.generation
	inst.reconnectMu.Unlock()

	connectedInst, err := connectWithGroupName(inst.groupName)
	if err != nil {
		return err
	}

	inst.reconnectMu.Lock()
	defer inst.reconnectMu.Unlock()
	if inst.shuttingDown || inst.generation != expectedGeneration {
		_ = connectedInst.Shutdown()
		return errReconnectAborted
	}

	// Hold stateMu through replay + current swap so newly-registered watches/fields/entities
	// cannot slip in between snapshot and swap and get lost on the newly connected instance.
	inst.stateMu.Lock()
	snapshot := inst.snapshotStateLocked()
	if err := inst.replayState(connectedInst, snapshot); err != nil {
		inst.stateMu.Unlock()
		_ = connectedInst.Shutdown()
		return err
	}

	inst.currentMu.Lock()
	previousInst := inst.current
	inst.current = connectedInst
	inst.currentMu.Unlock()
	inst.stateMu.Unlock()

	inst.runReconnectCallbacks()

	if previousInst != nil {
		_ = previousInst.Shutdown()
	}

	log.Logger.Infow("DCGM reconnected successfully")
	return nil
}

func (inst *reconnectingInstance) snapshotStateLocked() reconnectStateSnapshot {
	groupEntities := make([]uint, 0, len(inst.groupEntities))
	for entityID := range inst.groupEntities {
		groupEntities = append(groupEntities, entityID)
	}

	watchedSystems := inst.watchedSystems

	watchedFields := make([]dcgm.Short, 0, len(inst.watchedFields))
	for fieldID := range inst.watchedFields {
		watchedFields = append(watchedFields, fieldID)
	}

	return reconnectStateSnapshot{
		groupEntities:  groupEntities,
		watchedSystems: watchedSystems,
		watchedFields:  watchedFields,
	}
}

func (inst *reconnectingInstance) replayState(connectedInst Instance, snapshot reconnectStateSnapshot) error {
	groupEntities := snapshot.groupEntities
	watchedSystems := snapshot.watchedSystems
	watchedFields := snapshot.watchedFields

	for _, entityID := range groupEntities {
		if err := connectedInst.AddEntityToGroup(entityID); err != nil {
			return fmt.Errorf("failed to replay DCGM entity group state for entity %d: %w", entityID, err)
		}
	}

	if watchedSystems != 0 {
		if err := connectedInst.AddHealthWatch(watchedSystems); err != nil {
			return fmt.Errorf("failed to replay DCGM health watch state: %w", err)
		}
	}

	if len(watchedFields) > 0 {
		if err := connectedInst.AddFieldsToWatch(watchedFields); err != nil {
			return fmt.Errorf("failed to replay DCGM watched fields state: %w", err)
		}
	}

	return nil
}

func (inst *reconnectingInstance) RegisterReconnectCallback(callback func()) {
	if callback == nil {
		return
	}
	inst.stateMu.Lock()
	inst.callbacks = append(inst.callbacks, callback)
	inst.stateMu.Unlock()
}

func (inst *reconnectingInstance) runReconnectCallbacks() {
	inst.stateMu.RLock()
	callbacks := make([]func(), len(inst.callbacks))
	copy(callbacks, inst.callbacks)
	inst.stateMu.RUnlock()

	for _, callback := range callbacks {
		callback()
	}
}

func (inst *reconnectingInstance) DCGMExists() bool {
	inst.currentMu.RLock()
	currentInst := inst.current
	defer inst.currentMu.RUnlock()
	if currentInst == nil {
		return false
	}
	return currentInst.DCGMExists()
}

func (inst *reconnectingInstance) AddEntityToGroup(entityID uint) error {
	inst.stateMu.Lock()
	inst.groupEntities[entityID] = struct{}{}
	inst.stateMu.Unlock()

	inst.currentMu.RLock()
	currentInst := inst.current
	defer inst.currentMu.RUnlock()
	if currentInst != nil && currentInst.DCGMExists() {
		return currentInst.AddEntityToGroup(entityID)
	}
	return nil
}

func (inst *reconnectingInstance) AddHealthWatch(system dcgm.HealthSystem) error {
	inst.stateMu.Lock()
	inst.watchedSystems |= system
	inst.stateMu.Unlock()

	inst.currentMu.RLock()
	currentInst := inst.current
	defer inst.currentMu.RUnlock()
	if currentInst != nil && currentInst.DCGMExists() {
		return currentInst.AddHealthWatch(system)
	}
	return nil
}

func (inst *reconnectingInstance) RemoveHealthWatch(system dcgm.HealthSystem) error {
	inst.stateMu.Lock()
	inst.watchedSystems &^= system
	inst.stateMu.Unlock()

	inst.currentMu.RLock()
	currentInst := inst.current
	defer inst.currentMu.RUnlock()
	if currentInst != nil && currentInst.DCGMExists() {
		return currentInst.RemoveHealthWatch(system)
	}
	return nil
}

func (inst *reconnectingInstance) HealthCheck(system dcgm.HealthSystem) (dcgm.HealthResult, []dcgm.Incident, error) {
	inst.currentMu.RLock()
	currentInst := inst.current
	defer inst.currentMu.RUnlock()
	if currentInst == nil {
		return dcgm.DCGM_HEALTH_RESULT_PASS, nil, nil
	}
	return currentInst.HealthCheck(system)
}

func (inst *reconnectingInstance) AddFieldsToWatch(fields []dcgm.Short) error {
	inst.stateMu.Lock()
	for _, field := range fields {
		inst.watchedFields[field] = struct{}{}
	}
	inst.stateMu.Unlock()

	inst.currentMu.RLock()
	currentInst := inst.current
	defer inst.currentMu.RUnlock()
	if currentInst != nil && currentInst.DCGMExists() {
		return currentInst.AddFieldsToWatch(fields)
	}
	return nil
}

func (inst *reconnectingInstance) GetWatchedFields() []dcgm.Short {
	inst.stateMu.RLock()
	defer inst.stateMu.RUnlock()

	fields := make([]dcgm.Short, 0, len(inst.watchedFields))
	for fieldID := range inst.watchedFields {
		fields = append(fields, fieldID)
	}

	return fields
}

func (inst *reconnectingInstance) RemoveFieldsFromWatch(fields []dcgm.Short) error {
	inst.stateMu.Lock()
	for _, field := range fields {
		delete(inst.watchedFields, field)
	}
	inst.stateMu.Unlock()

	inst.currentMu.RLock()
	currentInst := inst.current
	defer inst.currentMu.RUnlock()
	if currentInst != nil && currentInst.DCGMExists() {
		return currentInst.RemoveFieldsFromWatch(fields)
	}
	return nil
}

func (inst *reconnectingInstance) GetLatestValuesForFields(deviceID uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
	inst.currentMu.RLock()
	currentInst := inst.current
	defer inst.currentMu.RUnlock()
	if currentInst == nil {
		return nil, fmt.Errorf("DCGM is not available")
	}
	return currentInst.GetLatestValuesForFields(deviceID, fields)
}

func (inst *reconnectingInstance) GetGroupHandle() dcgm.GroupHandle {
	inst.currentMu.RLock()
	currentInst := inst.current
	defer inst.currentMu.RUnlock()
	if currentInst == nil {
		return dcgm.GroupHandle{}
	}
	return currentInst.GetGroupHandle()
}

func (inst *reconnectingInstance) GetDevices() []DeviceInfo {
	inst.currentMu.RLock()
	currentInst := inst.current
	defer inst.currentMu.RUnlock()
	if currentInst == nil {
		return nil
	}
	return currentInst.GetDevices()
}

func (inst *reconnectingInstance) Shutdown() error {
	var shutdownErr error
	inst.shutdownOnce.Do(func() {
		inst.reconnectMu.Lock()
		inst.shuttingDown = true
		inst.generation++
		close(inst.stopCh)

		inst.currentMu.Lock()
		currentInst := inst.current
		inst.current = nil
		inst.currentMu.Unlock()

		if currentInst != nil {
			shutdownErr = currentInst.Shutdown()
		}
		inst.reconnectMu.Unlock()
	})
	return shutdownErr
}

var _ Instance = &noOpInstance{}

// NewNoOp creates a no-op DCGM instance.
func NewNoOp() Instance {
	return &noOpInstance{}
}

type noOpInstance struct{}

func (inst *noOpInstance) DCGMExists() bool                     { return false }
func (inst *noOpInstance) AddEntityToGroup(entityID uint) error { return nil }
func (inst *noOpInstance) GetGroupHandle() dcgm.GroupHandle {
	return dcgm.GroupHandle{}
}
func (inst *noOpInstance) GetDevices() []DeviceInfo { return nil }
func (inst *noOpInstance) AddHealthWatch(system dcgm.HealthSystem) error {
	return nil
}
func (inst *noOpInstance) RemoveHealthWatch(system dcgm.HealthSystem) error {
	return nil
}
func (inst *noOpInstance) HealthCheck(system dcgm.HealthSystem) (dcgm.HealthResult, []dcgm.Incident, error) {
	return dcgm.DCGM_HEALTH_RESULT_PASS, nil, nil
}
func (inst *noOpInstance) AddFieldsToWatch(fields []dcgm.Short) error {
	return nil
}
func (inst *noOpInstance) GetWatchedFields() []dcgm.Short {
	return nil
}
func (inst *noOpInstance) RemoveFieldsFromWatch(fields []dcgm.Short) error {
	return nil
}
func (inst *noOpInstance) GetLatestValuesForFields(deviceID uint, fields []dcgm.Short) ([]dcgm.FieldValue_v1, error) {
	return nil, fmt.Errorf("DCGM is not available")
}
func (inst *noOpInstance) Shutdown() error { return nil }
