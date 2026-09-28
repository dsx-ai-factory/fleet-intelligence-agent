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

package k8sworkload

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
)

func TestCollectorNormalizesOrderedPodLabels(t *testing.T) {
	allocations := &fakeAllocationReader{allocations: []GPUAllocation{
		{Namespace: "ml", PodName: "jobset-worker", ContainerName: "trainer", DeviceID: "GPU-abc"},
		{Namespace: "ml", PodName: "pytorch-worker", ContainerName: "trainer", DeviceID: "GPU-abc"},
		// A repeated allocation must not create duplicate pod or workload series.
		{Namespace: "ml", PodName: "pytorch-worker", ContainerName: "trainer", DeviceID: "GPU-abc"},
	}}
	labels := &fakePodLabelReader{labels: map[string]map[string]string{
		"ml/jobset-worker": {
			"jobset.sigs.k8s.io/jobset-name": "training-42",
			"training.kubeflow.org/job-name": "lower-priority-value",
		},
		"ml/pytorch-worker": {
			"training.kubeflow.org/job-name": "pytorch-17",
		},
	}}
	collector := newCollector(
		&config.KubernetesWorkloadConfig{WorkloadLabels: []string{
			"jobset.sigs.k8s.io/jobset-name",
			"training.kubeflow.org/job-name",
		}},
		allocations,
		labels,
		staticGPUUUIDProvider(map[string]string{"0": "GPU-abc"}),
	)

	families := gatherMetricFamilies(t, collector)
	metrics := families[workloadMetricName]
	require.Len(t, metrics, 2)
	require.Equal(t, map[string]string{
		"gpud_component":     "workload-attribution",
		"gpu":                "0",
		"uuid":               "GPU-abc",
		"workload_id":        "pytorch-17",
		"workload_namespace": "ml",
		"workload_source":    "kubernetes",
	}, metricLabels(metrics[0]))
	require.Equal(t, "training-42", metricLabels(metrics[1])["workload_id"])

	podMetrics := families[podMetricName]
	require.Len(t, podMetrics, 2)
	require.Equal(t, map[string]string{
		"container_name": "trainer",
		"gpud_component": "workload-attribution",
		"gpu":            "0",
		"pod_name":       "jobset-worker",
		"pod_namespace":  "ml",
		"uuid":           "GPU-abc",
	}, metricLabels(podMetrics[0]))
	require.Equal(t, "pytorch-worker", metricLabels(podMetrics[1])["pod_name"])
	require.Equal(t, 2, labels.calls)
}

func TestCollectorStopsEmittingRemovedAllocation(t *testing.T) {
	allocations := &fakeAllocationReader{allocations: []GPUAllocation{{
		Namespace: "ml",
		PodName:   "worker",
		DeviceID:  "GPU-abc",
	}}}
	collector := newCollector(
		&config.KubernetesWorkloadConfig{WorkloadLabels: []string{"job-name"}},
		allocations,
		&fakePodLabelReader{labels: map[string]map[string]string{
			"ml/worker": {"job-name": "training-42"},
		}},
		staticGPUUUIDProvider(map[string]string{"0": "GPU-abc"}),
	)

	require.Len(t, gatherMetrics(t, collector, workloadMetricName), 1)
	allocations.allocations = nil
	require.Empty(t, gatherMetrics(t, collector, workloadMetricName))
	require.Empty(t, gatherMetrics(t, collector, podMetricName))
}

func TestCollectorSupportsDeviceNames(t *testing.T) {
	collector := newCollector(
		&config.KubernetesWorkloadConfig{
			WorkloadLabels: []string{"job-name"},
			GPUIdentifier:  config.KubernetesGPUIdentifierDeviceName,
		},
		&fakeAllocationReader{allocations: []GPUAllocation{{
			Namespace: "ml",
			PodName:   "worker",
			DeviceID:  "nvidia3",
		}}},
		&fakePodLabelReader{labels: map[string]map[string]string{
			"ml/worker": {"job-name": "training-42"},
		}},
		staticGPUUUIDProvider(map[string]string{"3": "GPU-def"}),
	)

	metrics := gatherMetrics(t, collector, workloadMetricName)
	require.Len(t, metrics, 1)
	require.Equal(t, "3", metricLabels(metrics[0])["gpu"])
	require.Equal(t, "GPU-def", metricLabels(metrics[0])["uuid"])
}

func TestCollectorEmitsPodAssignmentWithoutConfiguredWorkloadLabel(t *testing.T) {
	collector := newCollector(
		&config.KubernetesWorkloadConfig{WorkloadLabels: []string{"job-name"}},
		&fakeAllocationReader{allocations: []GPUAllocation{{
			Namespace:     "ml",
			PodName:       "worker",
			ContainerName: "trainer",
			DeviceID:      "GPU-abc",
		}}},
		&fakePodLabelReader{labels: map[string]map[string]string{
			"ml/worker": {"unrelated": "value"},
		}},
		staticGPUUUIDProvider(map[string]string{"0": "GPU-abc"}),
	)

	families := gatherMetricFamilies(t, collector)
	require.Empty(t, families[workloadMetricName])
	require.Len(t, families[podMetricName], 1)
	require.Equal(t, "worker", metricLabels(families[podMetricName][0])["pod_name"])
}

func TestCollectorEmitsPodAssignmentWithoutPodMetadataReader(t *testing.T) {
	collector := newCollector(
		&config.KubernetesWorkloadConfig{WorkloadLabels: []string{"job-name"}},
		&fakeAllocationReader{allocations: []GPUAllocation{{
			Namespace:     "ml",
			PodName:       "worker",
			ContainerName: "trainer",
			DeviceID:      "GPU-abc",
		}}},
		nil,
		staticGPUUUIDProvider(map[string]string{"0": "GPU-abc"}),
	)

	families := gatherMetricFamilies(t, collector)
	require.Empty(t, families[workloadMetricName])
	require.Len(t, families[podMetricName], 1)
	require.Equal(t, "worker", metricLabels(families[podMetricName][0])["pod_name"])
}

func TestCollectorOmitsScrapeWhenPodResourcesFails(t *testing.T) {
	collector := newCollector(
		&config.KubernetesWorkloadConfig{WorkloadLabels: []string{"job-name"}},
		&fakeAllocationReader{err: errors.New("socket unavailable")},
		&fakePodLabelReader{},
		staticGPUUUIDProvider(map[string]string{"0": "GPU-abc"}),
	)

	require.Empty(t, gatherMetrics(t, collector, workloadMetricName))
	require.Empty(t, gatherMetrics(t, collector, podMetricName))
}

func TestCollectorSkipsInvalidMetricLabels(t *testing.T) {
	collector := newCollector(
		&config.KubernetesWorkloadConfig{WorkloadLabels: []string{"job-name"}},
		&fakeAllocationReader{allocations: []GPUAllocation{
			{Namespace: "ml", PodName: "bad-\xff", ContainerName: "trainer", DeviceID: "GPU-abc"},
			{Namespace: "ml", PodName: "good", ContainerName: "trainer", DeviceID: "GPU-abc"},
		}},
		&fakePodLabelReader{labels: map[string]map[string]string{
			"ml/bad-\xff": {"job-name": "valid-workload"},
			"ml/good":     {"job-name": "invalid-\xff"},
		}},
		staticGPUUUIDProvider(map[string]string{"0": "GPU-abc"}),
	)

	families := gatherMetricFamilies(t, collector)
	require.Len(t, families[podMetricName], 1)
	require.Equal(t, "good", metricLabels(families[podMetricName][0])["pod_name"])
	require.Len(t, families[workloadMetricName], 1)
	require.Equal(t, "valid-workload", metricLabels(families[workloadMetricName][0])["workload_id"])
}

func TestCollectorBoundsSharedGPUWorkloads(t *testing.T) {
	allocations := make([]GPUAllocation, 0, maxAssignmentsPerGPU+1)
	labels := make(map[string]map[string]string, maxAssignmentsPerGPU+1)
	for i := 0; i <= maxAssignmentsPerGPU; i++ {
		podName := fmt.Sprintf("worker-%d", i)
		allocations = append(allocations, GPUAllocation{
			Namespace: "ml",
			PodName:   podName,
			DeviceID:  "GPU-abc",
		})
		labels["ml/"+podName] = map[string]string{"job-name": fmt.Sprintf("job-%d", i)}
	}
	collector := newCollector(
		&config.KubernetesWorkloadConfig{WorkloadLabels: []string{"job-name"}},
		&fakeAllocationReader{allocations: allocations},
		&fakePodLabelReader{labels: labels},
		staticGPUUUIDProvider(map[string]string{"0": "GPU-abc"}),
	)

	families := gatherMetricFamilies(t, collector)
	require.Len(t, families[workloadMetricName], maxAssignmentsPerGPU)
	require.Len(t, families[podMetricName], maxAssignmentsPerGPU)
}

func TestCollectorCloseClosesAllocationReader(t *testing.T) {
	allocations := &fakeAllocationReader{}
	collector := newCollector(
		&config.KubernetesWorkloadConfig{WorkloadLabels: []string{"job-name"}},
		allocations,
		&fakePodLabelReader{},
		nil,
	)

	require.NoError(t, collector.Close())
	require.NoError(t, collector.Close())
	require.True(t, allocations.closed)
}

type fakeAllocationReader struct {
	allocations []GPUAllocation
	err         error
	closed      bool
}

func (r *fakeAllocationReader) List(context.Context) ([]GPUAllocation, error) {
	return r.allocations, r.err
}

func (r *fakeAllocationReader) Close() error {
	r.closed = true
	return nil
}

type fakePodLabelReader struct {
	labels map[string]map[string]string
	calls  int
}

func (r *fakePodLabelReader) Labels(_ context.Context, namespace, podName string) (map[string]string, error) {
	r.calls++
	return r.labels[namespace+"/"+podName], nil
}

func gatherMetrics(t *testing.T, collector prometheus.Collector, metricName string) []*dto.Metric {
	t.Helper()
	return gatherMetricFamilies(t, collector)[metricName]
}

func gatherMetricFamilies(t *testing.T, collector prometheus.Collector) map[string][]*dto.Metric {
	t.Helper()
	registry := prometheus.NewRegistry()
	require.NoError(t, registry.Register(collector))
	families, err := registry.Gather()
	require.NoError(t, err)
	metricsByName := make(map[string][]*dto.Metric, len(families))
	for _, family := range families {
		metricsByName[family.GetName()] = family.GetMetric()
	}
	return metricsByName
}

func metricLabels(metric *dto.Metric) map[string]string {
	labels := make(map[string]string, len(metric.GetLabel()))
	for _, label := range metric.GetLabel() {
		labels[label.GetName()] = label.GetValue()
	}
	return labels
}

func staticGPUUUIDProvider(gpuUUIDByIndex map[string]string) func() map[string]string {
	return func() map[string]string {
		return gpuUUIDByIndex
	}
}
