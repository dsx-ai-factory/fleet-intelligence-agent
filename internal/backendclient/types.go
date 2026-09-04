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

package backendclient

import "time"

// NodeUpsertRequest is the backend DTO for node inventory upserts.
type NodeUpsertRequest struct {
	Hostname                string        `json:"hostname"`
	AgentConfig             AgentConfig   `json:"agentConfig,omitempty"`
	Resources               NodeResources `json:"resources"`
	AgentVersion            string        `json:"agentVersion"`
	GPUDriverVersion        string        `json:"gpuDriverVersion"`
	CUDAVersion             string        `json:"cudaVersion"`
	DCGMVersion             string        `json:"dcgmVersion"`
	ContainerRuntimeVersion string        `json:"containerRuntimeVersion"`
	KernelVersion           string        `json:"kernelVersion"`
	OSImage                 string        `json:"osImage"`
	OperatingSystem         string        `json:"operatingSystem"`
	SystemUUID              string        `json:"systemUUID"`
	MachineID               string        `json:"machineId"`
	BootID                  string        `json:"bootID"`
	Uptime                  *time.Time    `json:"uptime,omitempty"`
	EnrolledAt              *time.Time    `json:"enrolledAt,omitempty"`
	NetPrivateIP            string        `json:"netPrivateIP,omitempty"`
	NodeGroup               string        `json:"nodeGroup"`
	ComputeZone             string        `json:"computeZone"`
}

// NodeUpsertResponse is the backend-resolved node membership after inventory upsert.
type NodeUpsertResponse struct {
	NodeUUID    string `json:"nodeUUID"`
	ComputeZone string `json:"computeZone"`
	NodeGroup   string `json:"nodeGroup"`
}

type NodeResources struct {
	CPUInfo    CPUInfo    `json:"cpuInfo"`
	MemoryInfo MemoryInfo `json:"memoryInfo"`
	GPUInfo    GPUInfo    `json:"gpuInfo"`
	DiskInfo   DiskInfo   `json:"diskInfo"`
	NICInfo    NICInfo    `json:"nicInfo"`
}

type AgentConfig struct {
	TotalComponents             int64    `json:"totalComponents"`
	RetentionPeriodSeconds      int64    `json:"retentionPeriodSeconds"`
	MetricScrapeIntervalSeconds int64    `json:"metricScrapeIntervalSeconds"`
	EnabledComponents           []string `json:"enabledComponents"`
	DisabledComponents          []string `json:"disabledComponents"`
	InventoryEnabled            bool     `json:"inventoryEnabled"`
	InventoryIntervalSeconds    int64    `json:"inventoryIntervalSeconds"`
	AttestationEnabled          bool     `json:"attestationEnabled"`
	AttestationIntervalSeconds  int64    `json:"attestationIntervalSeconds"`
}

type CPUInfo struct {
	Type         string `json:"type"`
	Manufacturer string `json:"manufacturer"`
	Architecture string `json:"architecture"`
	LogicalCores string `json:"logicalCores"`
}

type MemoryInfo struct {
	TotalBytes string `json:"totalBytes"`
}

type GPUInfo struct {
	Product      string      `json:"product"`
	Manufacturer string      `json:"manufacturer"`
	Architecture string      `json:"architecture"`
	Memory       string      `json:"memory"`
	GPUs         []GPUDevice `json:"gpus"`
}

type GPUDevice struct {
	UUID         string  `json:"uuid"`
	BusID        string  `json:"busID"`
	ClusterUUID  string  `json:"clusterUUID,omitempty"`
	CliqueID     *uint32 `json:"cliqueID,omitempty"`
	SN           string  `json:"sn"`
	MinorID      string  `json:"minorID"`
	BoardID      int     `json:"boardID,omitempty"`
	VBIOSVersion string  `json:"vbiosVersion"`
	ChassisSN    string  `json:"chassisSN"`
	GPUIndex     string  `json:"gpuIndex,omitempty"`
}

type DiskInfo struct {
	ContainerRootDisk string        `json:"containerRootDisk"`
	BlockDevices      []BlockDevice `json:"blockDevices"`
}

type BlockDevice struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Size       int64    `json:"size"`
	WWN        string   `json:"wwn"`
	MountPoint string   `json:"mountPoint"`
	FSType     string   `json:"fsType"`
	PartUUID   string   `json:"partUUID"`
	Parents    []string `json:"parents"`
}

type NICInfo struct {
	PrivateIPInterfaces []NICInterface `json:"privateIPInterfaces"`
}

type NICInterface struct {
	Interface string `json:"interface"`
	MAC       string `json:"mac"`
	IP        string `json:"ip"`
}

// NonceResponse is the backend DTO for node-scoped nonce responses.
type NonceResponse struct {
	Nonce                 string    `json:"nonce"`
	JWTAssertion          string    `json:"jwtAssertion,omitempty"`
	NonceRefreshTimestamp time.Time `json:"nonceRefreshTimestamp"`
}

// AttestationRequest is the backend DTO for attestation submission.
type AttestationRequest struct {
	AttestationData AttestationData `json:"attestationData"`
}

type AttestationData struct {
	SDKResponse           AttestationSDKResponse `json:"sdkResponse"`
	NonceRefreshTimestamp time.Time              `json:"nonceRefreshTimestamp"`
	Success               bool                   `json:"success"`
	ErrorMessage          string                 `json:"errorMessage,omitempty"`
}

type AttestationSDKResponse struct {
	Evidences     []EvidenceItem `json:"evidences"`
	ResultCode    int            `json:"resultCode"`
	ResultMessage string         `json:"resultMessage"`
}

type EvidenceItem struct {
	Arch          string `json:"arch"`
	Certificate   string `json:"certificate"`
	DriverVersion string `json:"driverVersion"`
	Evidence      string `json:"evidence"`
	Nonce         string `json:"nonce"`
	VBIOSVersion  string `json:"vbiosVersion"`
	Version       string `json:"version"`
}
