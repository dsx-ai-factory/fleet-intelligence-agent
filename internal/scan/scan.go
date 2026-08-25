// SPDX-FileCopyrightText: Copyright (c) 2025, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
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

// Package scan provides system scanning functionality for Fleet Intelligence monitoring.
// This implementation is based on the upstream gpud scan package but maintained
// independently to give fleetint full control over the scanning behavior.
package scan

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	"github.com/NVIDIA/fleet-intelligence-sdk/components"
	nvidiainfiniband "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/infiniband"
	infinibandclass "github.com/NVIDIA/fleet-intelligence-sdk/components/accelerator/nvidia/infiniband/class"
	nvidiacommon "github.com/NVIDIA/fleet-intelligence-sdk/pkg/config/common"
	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	nvidiadcgm "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm"
	nvidianvml "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/nvml"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/cmdutil"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/machineinfo"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/registry"
)

// Op holds the configuration for a scan operation.
type Op struct {
	infinibandClassRootDir string
	debug                  bool
	failureInjector        *components.FailureInjector
}

// Option represents a functional option for configuring the scan operation.
type Option func(*Op)

func (op *Op) applyOpts(opts []Option) error {
	for _, opt := range opts {
		opt(op)
	}

	if op.infinibandClassRootDir == "" {
		op.infinibandClassRootDir = infinibandclass.DefaultRootDir
	}

	return nil
}

// WithInfinibandClassRootDir specifies the root directory of the InfiniBand class.
func WithInfinibandClassRootDir(p string) Option {
	return func(op *Op) {
		op.infinibandClassRootDir = p
	}
}

// WithFailureInjector sets the failure injector for testing purposes.
func WithFailureInjector(injector *components.FailureInjector) Option {
	return func(op *Op) {
		op.failureInjector = injector
	}
}

// WithDebug enables debug mode for the scan operation.
func WithDebug(b bool) Option {
	return func(op *Op) {
		op.debug = b
	}
}

func printSummary(result components.CheckResult) {
	header := cmdutil.CheckMark
	if result.HealthStateType() != apiv1.HealthStateTypeHealthy {
		header = cmdutil.WarningSign
	}
	fmt.Printf("%s %s\n", header, result.Summary())
	fmt.Println(result.String())
	println()
}

// Scan performs a comprehensive system scan to detect any major issues with
// Fleet Intelligence, infiniband connectivity, NFS mounts, and other critical components.
// It returns an error if the scan fails to execute, but prints warnings for
// detected issues.
func Scan(ctx context.Context, opts ...Option) error {
	op := &Op{}
	if err := op.applyOpts(opts); err != nil {
		return err
	}

	fmt.Printf("\n\n%s scanning the host (GOOS %s)\n\n", cmdutil.InProgress, runtime.GOOS)

	nvmlInstance, err := nvidianvml.New()
	if err != nil {
		return err
	}

	mi, err := machineinfo.GetMachineInfo(nvmlInstance)
	if err != nil {
		return err
	}
	fmt.Printf("\n%s machine info\n", cmdutil.CheckMark)
	mi.RenderTable(os.Stdout)

	if mi.GPUInfo != nil && mi.GPUInfo.Product != "" {
		threshold, err := nvidiainfiniband.SupportsInfinibandPortRate(mi.GPUInfo.Product)
		if err == nil {
			log.Logger.Infow("setting default expected port states", "product", mi.GPUInfo.Product, "at_least_ports", threshold.AtLeastPorts, "at_least_rate", threshold.AtLeastRate)
			nvidiainfiniband.SetDefaultExpectedPortStates(threshold)
		}
	}

	dcgmGroupNames := components.NewDCGMGroupNames(fmt.Sprintf("scan-%d", os.Getpid()))

	// Initialize DCGM instance for DCGM-based health checks
	dcgmInstance, err := nvidiadcgm.NewWithGroupName(dcgmGroupNames.HealthMonitoringGroup)
	if err != nil {
		return err
	}

	// For scan mode, create a health cache
	dcgmHealthCache := nvidiadcgm.NewHealthCache(ctx, dcgmInstance, time.Minute)

	// Create field value cache for GPU device fields (placeholder)
	// Field watching will be set up after components register their fields
	// Note: CPU component manages its own field watching separately
	dcgmFieldValueCache := nvidiadcgm.NewFieldValueCache(ctx, dcgmInstance, time.Minute)

	defer func() {
		if dcgmHealthCache != nil {
			dcgmHealthCache.Stop()
		}
		if dcgmFieldValueCache != nil {
			dcgmFieldValueCache.Stop()
		}
		if err := dcgmInstance.Shutdown(); err != nil {
			log.Logger.Warnw("DCGM shutdown failed", "error", err)
		}
	}()

	gpudInstance := &components.GPUdInstance{
		RootCtx: ctx,

		MachineID: mi.MachineID,

		NVMLInstance:        nvmlInstance,
		DCGMInstance:        dcgmInstance,
		DCGMHealthCache:     dcgmHealthCache,
		DCGMFieldValueCache: dcgmFieldValueCache,
		DCGMGroupNames:      dcgmGroupNames,
		NVIDIAToolOverwrites: nvidiacommon.ToolOverwrites{
			InfinibandClassRootDir: op.infinibandClassRootDir,
		},

		EventStore:       nil,
		RebootEventStore: nil,

		MountPoints:  []string{"/"},
		MountTargets: []string{},

		FailureInjector: op.failureInjector,
	}

	// Initialize all components first using fleetint's component registry
	var initializedComponents []components.Component
	for _, c := range registry.All() {
		comp, err := c.InitFunc(gpudInstance)
		if err != nil {
			return err
		}
		if !comp.IsSupported() {
			continue
		}
		initializedComponents = append(initializedComponents, comp)
	}

	// Perform one health check to populate the cache
	if err := dcgmHealthCache.Poll(); err != nil {
		log.Logger.Warnw("DCGM health check failed", "error", err)
	}

	// Set up DCGM field watching after all components have registered their fields
	if err := dcgmFieldValueCache.SetupFieldWatchingWithName(dcgmGroupNames.GPUFieldGroup); err != nil {
		log.Logger.Warnw("failed to set up DCGM field watching", "error", err)
	}

	// Perform one field value poll to populate the cache
	if err := dcgmFieldValueCache.Poll(); err != nil {
		log.Logger.Warnw("DCGM field value poll failed", "error", err)
	}

	// Run checks on all initialized components
	for _, c := range initializedComponents {
		printSummary(c.Check())
	}

	fmt.Printf("\n\n%s scan complete\n\n", cmdutil.CheckMark)
	return nil
}
