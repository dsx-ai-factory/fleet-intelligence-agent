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

// Package k8sworkload maps kubelet GPU allocations to pods and configured
// workload labels, then exposes normalized allocation and workload metrics.
package k8sworkload

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
	pkgmetrics "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
)

const (
	workloadMetricName = "fleetint_gpu_workload_info"
	podMetricName      = "fleetint_gpu_pod_info"
	componentName      = "workload-attribution"
	workloadSource     = "kubernetes"

	collectionTimeout    = 10 * time.Second
	maxAssignmentsPerGPU = 16
)

// GPUAllocation identifies a pod and the GPU device ID assigned to it by the
// kubelet device plugin.
type GPUAllocation struct {
	Namespace     string
	PodName       string
	ContainerName string
	DeviceID      string
}

type allocationReader interface {
	List(context.Context) ([]GPUAllocation, error)
	Close() error
}

type podLabelReader interface {
	Labels(context.Context, string, string) (map[string]string, error)
}

// Collector emits current GPU-to-pod assignments and normalized workload
// identities derived from configured pod labels.
type Collector struct {
	allocations            allocationReader
	podLabels              podLabelReader
	workloadLabels         []string
	gpuIdentifier          string
	gpuUUIDByIndexProvider func() map[string]string
	workloadDesc           *prometheus.Desc
	podDesc                *prometheus.Desc
	closeOnce              sync.Once
}

// NewCollector creates a collector backed by the in-cluster Kubernetes API
// and the kubelet pod-resources socket.
func NewCollector(
	cfg *config.KubernetesWorkloadConfig,
	gpuUUIDByIndexProvider func() map[string]string,
) (*Collector, error) {
	allocations, err := newPodResourcesReader(cfg.KubernetesPodResourcesSocket())
	if err != nil {
		return nil, err
	}
	var podLabels podLabelReader
	reader, err := newInClusterPodLabelReader()
	if err != nil {
		if errors.Is(err, errNodeNameRequired) {
			_ = allocations.Close()
			return nil, err
		}
		log.Logger.Warnw(
			"Kubernetes pod metadata is unavailable; workload identity metrics are disabled",
			"error", err,
		)
	} else {
		podLabels = reader
	}
	return newCollector(cfg, allocations, podLabels, gpuUUIDByIndexProvider), nil
}

func newCollector(
	cfg *config.KubernetesWorkloadConfig,
	allocations allocationReader,
	podLabels podLabelReader,
	gpuUUIDByIndexProvider func() map[string]string,
) *Collector {
	return &Collector{
		allocations:            allocations,
		podLabels:              podLabels,
		workloadLabels:         append([]string(nil), cfg.WorkloadLabels...),
		gpuIdentifier:          cfg.KubernetesGPUIdentifier(),
		gpuUUIDByIndexProvider: gpuUUIDByIndexProvider,
		workloadDesc: prometheus.NewDesc(
			workloadMetricName,
			"Current workload assignment for a GPU.",
			[]string{
				"uuid",
				"gpu",
				"workload_source",
				"workload_namespace",
				"workload_id",
			},
			prometheus.Labels{pkgmetrics.MetricComponentLabelKey: componentName},
		),
		podDesc: prometheus.NewDesc(
			podMetricName,
			"Current Kubernetes pod and container assignment for a GPU.",
			[]string{
				"uuid",
				"gpu",
				"pod_namespace",
				"pod_name",
				"container_name",
			},
			prometheus.Labels{pkgmetrics.MetricComponentLabelKey: componentName},
		),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.workloadDesc
	ch <- c.podDesc
}

// Collect implements prometheus.Collector.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	if c.allocations == nil || c.gpuUUIDByIndexProvider == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), collectionTimeout)
	defer cancel()

	allocations, err := c.allocations.List(ctx)
	if err != nil {
		log.Logger.Warnw("failed to read Kubernetes GPU allocations; omitting pod and workload identity metrics", "error", err)
		return
	}

	gpuUUIDByIndex := c.gpuUUIDByIndexProvider()
	gpuIndexByDeviceID := make(map[string]string, len(gpuUUIDByIndex))
	for gpuIndex, uuid := range gpuUUIDByIndex {
		switch c.gpuIdentifier {
		case config.KubernetesGPUIdentifierUUID:
			if uuid != "" {
				gpuIndexByDeviceID[uuid] = gpuIndex
			}
		case config.KubernetesGPUIdentifierDeviceName:
			// Match dcgm-exporter's device-name behavior: represent DCGM GPU
			// entity N as "nvidiaN" when matching kubelet allocations. This is
			// a compatibility convention, not discovery of the host device minor.
			gpuIndexByDeviceID["nvidia"+gpuIndex] = gpuIndex
		}
	}

	type podReference struct {
		namespace string
		name      string
	}
	workloadsByPod := make(map[podReference]string)
	workloadSeries := make(map[workloadIdentity]struct{})
	podSeries := make(map[podIdentity]struct{})
	failedPodReads := 0

	for _, allocation := range allocations {
		gpuIndex, uuid, found := resolveGPU(
			allocation.DeviceID,
			gpuUUIDByIndex,
			gpuIndexByDeviceID,
		)
		if !found {
			continue
		}
		podSeries[podIdentity{
			uuid:          uuid,
			gpuIndex:      gpuIndex,
			namespace:     allocation.Namespace,
			podName:       allocation.PodName,
			containerName: allocation.ContainerName,
		}] = struct{}{}
		if c.podLabels == nil {
			continue
		}

		pod := podReference{namespace: allocation.Namespace, name: allocation.PodName}
		workloadID, cached := workloadsByPod[pod]
		if !cached {
			labels, err := c.podLabels.Labels(ctx, allocation.Namespace, allocation.PodName)
			if err != nil {
				failedPodReads++
				workloadsByPod[pod] = ""
				continue
			}
			workloadID = firstWorkloadID(labels, c.workloadLabels)
			workloadsByPod[pod] = workloadID
		}
		if workloadID == "" {
			continue
		}

		workloadSeries[workloadIdentity{
			uuid:       uuid,
			gpuIndex:   gpuIndex,
			namespace:  allocation.Namespace,
			workloadID: workloadID,
		}] = struct{}{}
	}

	if failedPodReads > 0 {
		log.Logger.Infow("failed to read labels for Kubernetes pods; omitting some workload identity metrics", "podCount", failedPodReads)
	}
	podOmitted, podInvalid := emitPodMetrics(ch, c.podDesc, podSeries)
	if podOmitted > 0 {
		log.Logger.Infow("Kubernetes GPU has more pod assignments than the collection limit; omitting some pod identity metrics", "omittedSeries", podOmitted)
	}
	workloadOmitted, workloadInvalid := emitWorkloadMetrics(ch, c.workloadDesc, workloadSeries)
	if workloadOmitted > 0 {
		log.Logger.Infow("Kubernetes GPU has more workload assignments than the collection limit; omitting some workload identity metrics", "omittedSeries", workloadOmitted)
	}
	if invalidSeries := podInvalid + workloadInvalid; invalidSeries > 0 {
		log.Logger.Infow(
			"failed to construct Kubernetes identity metrics; omitting invalid series",
			"seriesCount", invalidSeries,
		)
	}
}

type workloadIdentity struct {
	uuid       string
	gpuIndex   string
	namespace  string
	workloadID string
}

type podIdentity struct {
	uuid          string
	gpuIndex      string
	namespace     string
	podName       string
	containerName string
}

func emitWorkloadMetrics(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	series map[workloadIdentity]struct{},
) (omitted int, invalid int) {
	identities := make([]workloadIdentity, 0, len(series))
	for item := range series {
		identities = append(identities, item)
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].gpuIndex != identities[j].gpuIndex {
			return identities[i].gpuIndex < identities[j].gpuIndex
		}
		if identities[i].namespace != identities[j].namespace {
			return identities[i].namespace < identities[j].namespace
		}
		return identities[i].workloadID < identities[j].workloadID
	})

	workloadsByGPU := make(map[string]int)
	for _, item := range identities {
		if workloadsByGPU[item.uuid] >= maxAssignmentsPerGPU {
			omitted++
			continue
		}
		metric, err := prometheus.NewConstMetric(
			desc,
			prometheus.GaugeValue,
			1,
			item.uuid,
			item.gpuIndex,
			workloadSource,
			item.namespace,
			item.workloadID,
		)
		if err != nil {
			invalid++
			continue
		}
		workloadsByGPU[item.uuid]++
		ch <- metric
	}
	return omitted, invalid
}

func emitPodMetrics(
	ch chan<- prometheus.Metric,
	desc *prometheus.Desc,
	series map[podIdentity]struct{},
) (omitted int, invalid int) {
	identities := make([]podIdentity, 0, len(series))
	for item := range series {
		identities = append(identities, item)
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].gpuIndex != identities[j].gpuIndex {
			return identities[i].gpuIndex < identities[j].gpuIndex
		}
		if identities[i].namespace != identities[j].namespace {
			return identities[i].namespace < identities[j].namespace
		}
		if identities[i].podName != identities[j].podName {
			return identities[i].podName < identities[j].podName
		}
		return identities[i].containerName < identities[j].containerName
	})

	assignmentsByGPU := make(map[string]int)
	for _, item := range identities {
		if assignmentsByGPU[item.uuid] >= maxAssignmentsPerGPU {
			omitted++
			continue
		}
		metric, err := prometheus.NewConstMetric(
			desc,
			prometheus.GaugeValue,
			1,
			item.uuid,
			item.gpuIndex,
			item.namespace,
			item.podName,
			item.containerName,
		)
		if err != nil {
			invalid++
			continue
		}
		assignmentsByGPU[item.uuid]++
		ch <- metric
	}
	return omitted, invalid
}

func firstWorkloadID(labels map[string]string, workloadLabels []string) string {
	for _, label := range workloadLabels {
		if value := labels[label]; value != "" {
			return value
		}
	}
	return ""
}

func resolveGPU(
	deviceID string,
	gpuUUIDByIndex map[string]string,
	gpuIndexByDeviceID map[string]string,
) (gpuIndex string, uuid string, found bool) {
	gpuIndex, found = gpuIndexByDeviceID[deviceID]
	if !found {
		return "", "", false
	}
	uuid = gpuUUIDByIndex[gpuIndex]
	return gpuIndex, uuid, uuid != ""
}

// Close releases the Kubernetes workload collector's runtime resources.
func (c *Collector) Close() error {
	if c == nil {
		return nil
	}
	var err error
	c.closeOnce.Do(func() {
		if c.allocations != nil {
			err = c.allocations.Close()
		}
		if closer, ok := c.podLabels.(interface{ Close() error }); ok {
			if closeErr := closer.Close(); err == nil {
				err = closeErr
			}
		}
	})
	if err != nil {
		return fmt.Errorf("close Kubernetes workload collector: %w", err)
	}
	return nil
}
