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

// Package precheck evaluates prerequisite checks for enrollment and installation flows.
package precheck

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	apiv1 "github.com/NVIDIA/fleet-intelligence-sdk/api/v1"
	pkgmachineinfo "github.com/NVIDIA/fleet-intelligence-sdk/pkg/machine-info"
	nvidiapci "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia/pci"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/dcgminventory"
	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/dcgmversion"
)

var supportedArchitectures = []string{"Hopper", "Blackwell", "Rubin", "Ampere", "Ada Lovelace"}

const minimumDCGMVersion = "4.2.3"
const minimumDriverMajorVersion = 510

var (
	collectGPUInventory = collectDCGMGPUInventory
	getMachineGPUInfo   = pkgmachineinfo.GetMachineGPUInfo
	lookPath            = exec.LookPath
	detectDCGMVersion   = dcgmversion.DetectHostengineVersion
	listPCIGPUs         = nvidiapci.ListPCIGPUs
)

type Input struct {
	GPUHardwarePresent bool
	GPUHardwareErr     error
	GPUInfo            *apiv1.MachineGPUInfo
	GPUInfoErr         error
	GPUDriverVersion   string
	NVAttestPresent    *bool
	DCGMReachable      *bool
	DCGMVersion        string
}

type Check struct {
	Name    string
	Passed  bool
	Message string
}

type Result struct {
	Checks []Check
}

func (r Result) Passed() bool {
	for _, check := range r.Checks {
		if !check.Passed {
			return false
		}
	}

	return true
}

func SupportedArchitectures() []string {
	return slices.Clone(supportedArchitectures)
}

func (r Result) FailedChecks() []Check {
	failed := make([]Check, 0, len(r.Checks))
	for _, check := range r.Checks {
		if !check.Passed {
			failed = append(failed, check)
		}
	}

	return failed
}

func Run() (Result, error) {
	input, err := CollectInput()
	if err != nil {
		return Result{}, err
	}

	return Evaluate(&input), nil
}

func CollectInput() (Input, error) {
	input := Input{}

	gpuInfo, driverVersion, gpuInfoErr := collectGPUInventory()
	if gpuInfoErr == nil {
		input.GPUInfo = gpuInfo
		input.GPUDriverVersion = driverVersion
		input.GPUHardwarePresent = hasDetectedGPUInfo(gpuInfo)
	} else {
		input.GPUInfoErr = gpuInfoErr
	}

	if !input.GPUHardwarePresent {
		input.GPUHardwarePresent, input.GPUHardwareErr = detectGPUHardware()
	}

	nvattestPresent := detectNVAttest()
	input.NVAttestPresent = &nvattestPresent

	dcgmReachable, dcgmVersion := detectDCGM()
	input.DCGMReachable = &dcgmReachable
	input.DCGMVersion = dcgmVersion

	return input, nil
}

func collectDCGMGPUInventory() (*apiv1.MachineGPUInfo, string, error) {
	dcgmCtx, dcgmCancel := context.WithTimeout(context.Background(), time.Minute)
	defer dcgmCancel()
	session, err := dcgminventory.Open(dcgmCtx, fmt.Sprintf("precheck-%d", os.Getpid()), time.Minute)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = session.Close() }()

	gpuInfo, driverVersion, _, err := getMachineGPUInfo(session.Instance, session.FieldCache)
	return gpuInfo, driverVersion, err
}

func Evaluate(input *Input) Result {
	if input == nil {
		input = &Input{}
	}

	checks := []Check{
		evaluateGPUPresence(input),
		evaluateArchitecture(input),
		evaluateDriver(input),
		evaluateNVAttest(input.NVAttestPresent),
		evaluateDCGM(input),
	}

	return Result{Checks: checks}
}

func evaluateGPUPresence(input *Input) Check {
	if input != nil && input.GPUHardwareErr != nil {
		return Check{
			Name:    "gpu-present",
			Message: "Unable to determine NVIDIA GPU presence; verify lspci is installed and accessible, then retry",
		}
	}

	if !gpuHardwareDetected(input) {
		return Check{
			Name:    "gpu-present",
			Message: "No NVIDIA GPU detected; verify the node has an NVIDIA GPU installed and visible to the OS",
		}
	}

	return Check{
		Name:    "gpu-present",
		Passed:  true,
		Message: "NVIDIA GPU detected",
	}
}

func evaluateArchitecture(input *Input) Check {
	if !gpuHardwareDetected(input) {
		return Check{
			Name:    "gpu-architecture",
			Passed:  true,
			Message: "GPU architecture check skipped because no NVIDIA GPU was detected",
		}
	}

	if input == nil || input.GPUDriverVersion == "" {
		return Check{
			Name:    "gpu-architecture",
			Passed:  true,
			Message: "GPU architecture check skipped because the NVIDIA driver is not available",
		}
	}

	if input.GPUInfo == nil || len(input.GPUInfo.GPUs) == 0 {
		return Check{
			Name:    "gpu-architecture",
			Passed:  true,
			Message: "GPU architecture check skipped because GPU details could not be collected; check agent logs and retry",
		}
	}

	if input.GPUInfo.Architecture == "" {
		return Check{
			Name:    "gpu-architecture",
			Message: "GPU detected, but its architecture could not be determined; verify the NVIDIA driver is installed and loaded",
		}
	}

	if !slices.ContainsFunc(supportedArchitectures, func(s string) bool {
		return architectureMatches(s, input.GPUInfo.Architecture)
	}) {
		return Check{
			Name: "gpu-architecture",
			Message: fmt.Sprintf(
				"Unsupported GPU architecture: %s; supported architectures are %s",
				input.GPUInfo.Architecture,
				formatSupportedArchitectures(supportedArchitectures),
			),
		}
	}

	return Check{
		Name:    "gpu-architecture",
		Passed:  true,
		Message: "supported GPU architecture detected: " + input.GPUInfo.Architecture,
	}
}

func architectureMatches(supported, detected string) bool {
	return normalizeArchitectureName(supported) == normalizeArchitectureName(detected)
}

func normalizeArchitectureName(v string) string {
	normalized := strings.TrimSpace(strings.ToLower(v))
	normalized = strings.ReplaceAll(normalized, "-", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, " ", "")
	return normalized
}

func formatSupportedArchitectures(architectures []string) string {
	switch len(architectures) {
	case 0:
		return ""
	case 1:
		return architectures[0]
	case 2:
		return architectures[0] + " and " + architectures[1]
	default:
		return strings.Join(architectures[:len(architectures)-1], ", ") + ", and " + architectures[len(architectures)-1]
	}
}

func evaluateDriver(input *Input) Check {
	if !gpuHardwareDetected(input) {
		return Check{
			Name:    "gpu-driver",
			Passed:  true,
			Message: "NVIDIA driver check skipped because no NVIDIA GPU was detected",
		}
	}

	if input == nil || input.GPUDriverVersion == "" {
		return Check{
			Name:    "gpu-driver",
			Message: "NVIDIA GPU hardware is present, but the NVIDIA driver was not detected; install or load the NVIDIA driver and retry",
		}
	}

	driverMajor, _, _, err := parseDriverVersion(input.GPUDriverVersion)
	if err != nil {
		return Check{
			Name:    "gpu-driver",
			Message: "failed to parse NVIDIA driver version: " + err.Error(),
		}
	}

	if driverMajor < minimumDriverMajorVersion {
		return Check{
			Name:    "gpu-driver",
			Message: fmt.Sprintf("NVIDIA driver version %s is below the required minimum %d; upgrade the driver and retry", input.GPUDriverVersion, minimumDriverMajorVersion),
		}
	}

	return Check{
		Name:    "gpu-driver",
		Passed:  true,
		Message: "NVIDIA driver detected: " + input.GPUDriverVersion,
	}
}

func parseDriverVersion(version string) (major, minor, patch int, err error) {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return 0, 0, 0, fmt.Errorf("failed to parse driver version (expected at least 2 parts): %s", version)
	}
	if len(parts) > 3 {
		return 0, 0, 0, fmt.Errorf("failed to parse driver version (expected at most 3 parts): %s", version)
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to parse driver version (major): %w", err)
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to parse driver version (minor): %w", err)
	}
	if len(parts) == 3 {
		patch, err = strconv.Atoi(parts[2])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("failed to parse driver version (patch): %w", err)
		}
	}
	return major, minor, patch, nil
}

func gpuHardwareDetected(input *Input) bool {
	if input == nil {
		return false
	}

	if input.GPUHardwarePresent {
		return true
	}

	return hasDetectedGPUInfo(input.GPUInfo)
}

func hasDetectedGPUInfo(info *apiv1.MachineGPUInfo) bool {
	return info != nil && len(info.GPUs) > 0
}

func detectGPUHardware() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	devs, err := listPCIGPUs(ctx)
	if err != nil {
		return false, err
	}

	return len(devs) > 0, nil
}

// evaluateNVAttest checks whether nvattest is present in PATH.
// present may be nil when constructing a partial Input (e.g. in targeted tests
// that focus on other checks); in that case the check is skipped.
func evaluateNVAttest(present *bool) Check {
	if present == nil {
		return Check{
			Name:    "nvattest",
			Passed:  true,
			Message: "nvattest check skipped",
		}
	}

	if !*present {
		return Check{
			Name:    "nvattest",
			Message: "nvattest was not found in PATH; install nvattest and ensure it is available in PATH",
		}
	}

	return Check{
		Name:    "nvattest",
		Passed:  true,
		Message: "nvattest detected",
	}
}

func evaluateDCGM(input *Input) Check {
	if input == nil {
		return Check{
			Name:    "dcgm",
			Passed:  true,
			Message: "DCGM checks skipped",
		}
	}

	if input.DCGMReachable == nil {
		return Check{
			Name:    "dcgm",
			Passed:  true,
			Message: "DCGM checks skipped",
		}
	}

	if !*input.DCGMReachable {
		return Check{
			Name:    "dcgm",
			Message: "DCGM HostEngine is not reachable; verify DCGM is running and DCGM_URL is configured correctly",
		}
	}

	if input.DCGMVersion == "" {
		return Check{
			Name:    "dcgm",
			Message: "DCGM HostEngine is reachable, but its version could not be determined; verify the DCGM installation",
		}
	}

	versionOK, err := isVersionAtLeast(input.DCGMVersion, minimumDCGMVersion)
	if err != nil {
		return Check{
			Name:    "dcgm",
			Message: "failed to parse DCGM HostEngine version: " + err.Error(),
		}
	}

	if !versionOK {
		return Check{
			Name:    "dcgm",
			Message: "DCGM HostEngine version " + input.DCGMVersion + " is below the required minimum " + minimumDCGMVersion + "; upgrade DCGM and retry",
		}
	}

	return Check{
		Name:    "dcgm",
		Passed:  true,
		Message: "DCGM HostEngine version is supported: " + input.DCGMVersion,
	}
}

func isVersionAtLeast(version, minimum string) (bool, error) {
	versionParts, err := parseVersion(version)
	if err != nil {
		return false, err
	}

	minimumParts, err := parseVersion(minimum)
	if err != nil {
		return false, err
	}

	for i := range min(len(versionParts), len(minimumParts)) {
		if versionParts[i] > minimumParts[i] {
			return true, nil
		}
		if versionParts[i] < minimumParts[i] {
			return false, nil
		}
	}

	return len(versionParts) >= len(minimumParts), nil
}

func parseVersion(version string) ([]int, error) {
	parts := strings.Split(version, ".")
	parsed := make([]int, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid version %q", version)
		}
		parsed = append(parsed, value)
	}

	return parsed, nil
}

func detectNVAttest() bool {
	_, err := lookPath("nvattest")
	return err == nil
}

func detectDCGM() (bool, string) {
	version, err := detectDCGMVersion()
	if err != nil {
		return false, ""
	}

	return true, version
}
