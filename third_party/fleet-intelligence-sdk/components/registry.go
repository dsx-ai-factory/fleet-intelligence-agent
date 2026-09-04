// Copyright 2024 Lepton AI Inc
// Source: https://github.com/leptonai/gpud

package components

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	nvidiacommon "github.com/NVIDIA/fleet-intelligence-sdk/pkg/config/common"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/eventstore"
	pkghost "github.com/NVIDIA/fleet-intelligence-sdk/pkg/host"
	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
	nvidianvml "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/nvml"
)

var (
	// ErrAlreadyRegistered is the error returned when a component is already registered.
	ErrAlreadyRegistered = errors.New("component already registered")
)

// GPUdInstance is the instance of the GPUd dependencies.
type GPUdInstance struct {
	RootCtx context.Context

	// MachineID is either the machine ID assigned from the control plane
	// or the unique UUID of the machine.
	// For example, it is used to identify itself for the NFS checker.
	MachineID string

	KernelModulesToCheck []string

	DCGMInstance         nvidiadcgm.Instance
	DCGMHealthCache      *nvidiadcgm.HealthCache     // Shared cache for DCGM health check results
	DCGMFieldValueCache  *nvidiadcgm.FieldValueCache // Shared cache for DCGM field values (GPU devices only)
	DCGMGroupNames       DCGMGroupNames
	NVMLInstance         nvidianvml.Instance
	NVIDIAToolOverwrites nvidiacommon.ToolOverwrites

	DBRW *sql.DB
	DBRO *sql.DB

	EventStore       eventstore.Store
	RebootEventStore pkghost.RebootEventStore

	MountPoints  []string
	MountTargets []string

	// HealthCheckInterval configures how often components perform health checks.
	HealthCheckInterval time.Duration

	FailureInjector *FailureInjector
}

// GPUDeviceProvider supplies a refreshable GPU inventory without
// exposing a vendor-library-specific query interface to components.
type GPUDeviceProvider interface {
	GPUDevices() []nvidiadcgm.DeviceInfo
}

// GPUDevices returns the current inventory exposed by the DCGM instance.
func (i *GPUdInstance) GPUDevices() []nvidiadcgm.DeviceInfo {
	if i == nil || i.DCGMInstance == nil {
		return nil
	}
	return i.DCGMInstance.GetDevices()
}

// DCGMGroupNames names the DCGM groups and field groups owned by one fleetint process.
type DCGMGroupNames struct {
	HealthMonitoringGroup string
	GPUFieldGroup         string
	CPUGroup              string
	CPUFieldGroup         string
	ProfilingFieldGroup   string
}

// NewDCGMGroupNames returns a fleetint-owned set of DCGM names for a single owner.
func NewDCGMGroupNames(owner string) DCGMGroupNames {
	return DCGMGroupNames{
		HealthMonitoringGroup: fmt.Sprintf("fleetint-%s-health", owner),
		GPUFieldGroup:         fmt.Sprintf("fleetint-%s-gpu-fields", owner),
		CPUGroup:              fmt.Sprintf("fleetint-%s-cpu", owner),
		CPUFieldGroup:         fmt.Sprintf("fleetint-%s-cpu-fields", owner),
		ProfilingFieldGroup:   fmt.Sprintf("fleetint-%s-prof-fields", owner),
	}
}

// DefaultDCGMGroupNames returns fleetint defaults for callers that do not provide names.
func DefaultDCGMGroupNames() DCGMGroupNames {
	return NewDCGMGroupNames("default")
}

// WithDefaults fills any missing names with fleetint defaults.
func (n DCGMGroupNames) WithDefaults() DCGMGroupNames {
	defaults := DefaultDCGMGroupNames()
	if n.HealthMonitoringGroup == "" {
		n.HealthMonitoringGroup = defaults.HealthMonitoringGroup
	}
	if n.GPUFieldGroup == "" {
		n.GPUFieldGroup = defaults.GPUFieldGroup
	}
	if n.CPUGroup == "" {
		n.CPUGroup = defaults.CPUGroup
	}
	if n.CPUFieldGroup == "" {
		n.CPUFieldGroup = defaults.CPUFieldGroup
	}
	if n.ProfilingFieldGroup == "" {
		n.ProfilingFieldGroup = defaults.ProfilingFieldGroup
	}
	return n
}

type FailureInjector struct {
	GPUUUIDsWithRowRemappingPending               []string
	GPUUUIDsWithRowRemappingFailed                []string
	GPUUUIDsWithHWSlowdown                        []string
	GPUUUIDsWithHWSlowdownThermal                 []string
	GPUUUIDsWithHWSlowdownPowerBrake              []string
	GPUUUIDsWithGPULost                           []string
	GPUUUIDsWithGPURequiresReset                  []string
	GPUUUIDsWithFabricStateHealthSummaryUnhealthy []string

	// GPUProductNameOverride overrides the detected GPU product name.
	// Useful for testing fabric state injection on systems where the actual GPU
	// (e.g., H100-PCIe) doesn't support fabric state monitoring.
	// Set to "H100-SXM" or "H200-SXM" to simulate a fabric-capable system.
	GPUProductNameOverride string
}

// InitFunc is the function that initializes a component.
type InitFunc func(*GPUdInstance) (Component, error)

// Registry is the interface for the registry of components.
type Registry interface {
	// MustRegister registers a component with the given initialization function.
	// It panics if the component is already registered.
	// It panics if the initialization function returns an error.
	MustRegister(initFunc InitFunc)

	// Register registers a component with the given initialization function.
	// It returns an error if the component is already registered.
	// It returns an error if the initialization function returns an error.
	Register(initFunc InitFunc) (Component, error)

	// All returns all registered components.
	All() []Component

	// Get returns a component by name.
	// It returns nil if the component is not registered.
	Get(name string) Component

	// Deregister deregisters a component by name, and returns the
	// underlying component if it is registered.
	// It returns nil if the component is not registered.
	// Meaning, it is safe to call it multiple times,
	// and it is also safe to call it with a non-registered name.
	Deregister(name string) Component
}

var _ Registry = &registry{}

type registry struct {
	mu           sync.RWMutex
	gpudInstance *GPUdInstance
	components   map[string]Component
}

// NewRegistry creates a new registry.
func NewRegistry(gpudInstance *GPUdInstance) Registry {
	return &registry{
		gpudInstance: gpudInstance,
		components:   make(map[string]Component),
	}
}

// MustRegister registers a component with the given name and initialization function.
// It panics if the component is already registered.
// It panics if the initialization function returns an error.
func (r *registry) MustRegister(initFunc InitFunc) {
	if _, err := r.Register(initFunc); err != nil {
		panic(err)
	}
}

// registerInit registers an initialization function for a component with the given name.
func (r *registry) Register(initFunc InitFunc) (Component, error) {
	c, err := initFunc(r.gpudInstance)
	if err != nil {
		return nil, err
	}

	if r.hasRegistered(c.Name()) {
		return nil, fmt.Errorf("component %s already registered: %w", c.Name(), ErrAlreadyRegistered)
	}

	r.mu.Lock()
	r.components[c.Name()] = c
	r.mu.Unlock()

	return c, nil
}

// hasRegistered checks if a component with the given name is already registered.
func (r *registry) hasRegistered(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.components[name]
	return ok
}

// All returns all registered components.
func (r *registry) All() []Component {
	all := r.listAll()
	sort.Slice(all, func(i, j int) bool {
		return all[i].Name() < all[j].Name()
	})
	return all
}

// listAll returns all registered components.
func (r *registry) listAll() []Component {
	r.mu.RLock()
	defer r.mu.RUnlock()

	all := make([]Component, 0, len(r.components))
	for _, c := range r.components {
		all = append(all, c)
	}
	return all
}

func (r *registry) Get(name string) Component {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.components[name]
}

func (r *registry) Deregister(name string) Component {
	r.mu.Lock()
	c := r.components[name]
	if c != nil {
		delete(r.components, name)
	}
	r.mu.Unlock()

	return c
}
