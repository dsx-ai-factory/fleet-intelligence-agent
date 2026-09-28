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

package workloadmetrics

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
)

func TestManagerLifecycle(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "0"), []byte("123456\n"), 0o600))

	registry := prometheus.NewRegistry()
	manager, err := startWithRegisterer(registry, hpcConfig(dir), staticGPUUUIDProvider(map[string]string{"0": "GPU-a"}))
	require.NoError(t, err)

	families, err := registry.Gather()
	require.NoError(t, err)
	require.Len(t, families, 1)
	require.Equal(t, "fleetint_gpu_workload_info", families[0].GetName())
	require.Len(t, families[0].GetMetric(), 1)

	labels := make(map[string]string)
	for _, label := range families[0].GetMetric()[0].GetLabel() {
		labels[label.GetName()] = label.GetValue()
	}
	require.Equal(t, "GPU-a", labels["uuid"])
	require.Equal(t, "0", labels["gpu"])
	require.Equal(t, "hpc", labels["workload_source"])
	require.Equal(t, "123456", labels["workload_id"])
	require.NotContains(t, labels, "workload_kind")
	require.NotContains(t, labels, "workload_namespace")

	manager.Close()
	manager.Close()
	families, err = registry.Gather()
	require.NoError(t, err)
	require.Empty(t, families)
}

func TestManagerDisabled(t *testing.T) {
	registry := prometheus.NewRegistry()
	manager, err := startWithRegisterer(registry, nil, staticGPUUUIDProvider(map[string]string{"0": "GPU-a"}))
	require.NoError(t, err)
	require.NotNil(t, manager)

	families, err := registry.Gather()
	require.NoError(t, err)
	require.Empty(t, families)
	manager.Close()
}

func TestManagerRegistrationFailure(t *testing.T) {
	dir := t.TempDir()
	registry := prometheus.NewRegistry()

	first, err := startWithRegisterer(registry, hpcConfig(dir), nil)
	require.NoError(t, err)
	defer first.Close()

	second, err := startWithRegisterer(registry, hpcConfig(dir), nil)
	require.ErrorContains(t, err, "register HPC workload identity metric collector")
	require.Nil(t, second)
}

func TestManagerKubernetesCollectorCreationFailure(t *testing.T) {
	t.Setenv("NODE_NAME", "")
	manager, err := startWithRegisterer(
		prometheus.NewRegistry(),
		&config.WorkloadAttributionConfig{
			Source: config.WorkloadSourceKubernetes,
			Kubernetes: &config.KubernetesWorkloadConfig{
				WorkloadLabels: []string{"job-name"},
			},
		},
		staticGPUUUIDProvider(map[string]string{"0": "GPU-a"}),
	)
	require.ErrorContains(t, err, "create Kubernetes workload identity metric collector")
	require.Nil(t, manager)
}

func TestManagerClosesRegisteredCollector(t *testing.T) {
	collector := &fakeCloseableCollector{
		desc:     prometheus.NewDesc("fleetint_test_workload_info", "test", nil, nil),
		closeErr: errors.New("close failed"),
	}
	registry := prometheus.NewRegistry()
	manager := &Manager{registerer: registry}
	require.NoError(t, manager.registerCollector("test", collector))

	manager.Close()
	manager.Close()
	require.Equal(t, 1, collector.closeCalls)
}

func TestManagerClosesCollectorWhenRegistrationFails(t *testing.T) {
	desc := prometheus.NewDesc("fleetint_test_workload_info", "test", nil, nil)
	registry := prometheus.NewRegistry()
	manager := &Manager{registerer: registry}
	require.NoError(t, manager.registerCollector("test", &fakeCloseableCollector{desc: desc}))

	duplicate := &fakeCloseableCollector{desc: desc}
	err := manager.registerCollector("test", duplicate)
	require.ErrorContains(t, err, "register test workload identity metric collector")
	require.Equal(t, 1, duplicate.closeCalls)
}

func TestManagerRejectsUnsupportedSource(t *testing.T) {
	manager, err := startWithRegisterer(
		prometheus.NewRegistry(),
		&config.WorkloadAttributionConfig{Source: "container"},
		nil,
	)
	require.ErrorContains(t, err, `unsupported workload_attribution source "container"`)
	require.Nil(t, manager)
}

func TestManagerRejectsUnsupportedGPUIdentifier(t *testing.T) {
	manager, err := startWithRegisterer(
		prometheus.NewRegistry(),
		&config.WorkloadAttributionConfig{
			Source: config.WorkloadSourceHPC,
			HPC: &config.HPCWorkloadConfig{
				JobMappingDir: t.TempDir(),
				GPUIdentifier: "invalid",
			},
		},
		nil,
	)
	require.ErrorContains(t, err, `unsupported workload_attribution.hpc.gpu_identifier "invalid"`)
	require.Nil(t, manager)
}

func hpcConfig(dir string) *config.WorkloadAttributionConfig {
	return &config.WorkloadAttributionConfig{
		Source: config.WorkloadSourceHPC,
		HPC: &config.HPCWorkloadConfig{
			JobMappingDir: dir,
		},
	}
}

func staticGPUUUIDProvider(gpuUUIDByIndex map[string]string) func() map[string]string {
	return func() map[string]string {
		return gpuUUIDByIndex
	}
}

type fakeCloseableCollector struct {
	desc       *prometheus.Desc
	closeErr   error
	closeCalls int
}

func (c *fakeCloseableCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

func (c *fakeCloseableCollector) Collect(chan<- prometheus.Metric) {}

func (c *fakeCloseableCollector) Close() error {
	c.closeCalls++
	return c.closeErr
}
