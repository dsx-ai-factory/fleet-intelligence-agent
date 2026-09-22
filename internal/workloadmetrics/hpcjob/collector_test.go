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

package hpcjob

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	pkgmetricsscraper "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metrics/scraper"
)

type staticReader struct {
	mapping           Mapping
	err               error
	gpuIdentifierType GPUIdentifierType
}

type mutableReader struct {
	mu      sync.RWMutex
	mapping Mapping
}

func (r *mutableReader) GPUIdentifierType() GPUIdentifierType {
	return GPUIdentifierDCGMIndex
}

func (r *mutableReader) Read() (Mapping, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.mapping, nil
}

func (r *mutableReader) Set(mapping Mapping) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mapping = mapping
}

func (r staticReader) Read() (Mapping, error) {
	return r.mapping, r.err
}

func (r staticReader) GPUIdentifierType() GPUIdentifierType {
	if r.gpuIdentifierType == "" {
		return GPUIdentifierDCGMIndex
	}
	return r.gpuIdentifierType
}

func TestCollector(t *testing.T) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewCollector(staticReader{mapping: Mapping{
		gpuJobMapping("0", "", "123", "456"),
		gpuJobMapping("2", "", "789"),
	}}, staticGPUUUIDProvider(map[string]string{"0": "GPU-a", "2": "GPU-c"})))

	scraper, err := pkgmetricsscraper.NewPrometheusScraper(registry)
	require.NoError(t, err)
	metrics, err := scraper.Scrape(context.Background())
	require.NoError(t, err)
	require.Len(t, metrics, 3)

	got := make(map[string]map[string]string, len(metrics))
	for _, metric := range metrics {
		require.Equal(t, metricName, metric.Name)
		require.Equal(t, componentName, metric.Component)
		require.Equal(t, float64(1), metric.Value)
		got[metric.Labels["workload_id"]] = metric.Labels
	}
	require.Equal(t, map[string]map[string]string{
		"123": {
			"uuid": "GPU-a", "gpu": "0", "gpu_instance_id": "", "workload_source": "hpc",
			"workload_id": "123",
		},
		"456": {
			"uuid": "GPU-a", "gpu": "0", "gpu_instance_id": "", "workload_source": "hpc",
			"workload_id": "456",
		},
		"789": {
			"uuid": "GPU-c", "gpu": "2", "gpu_instance_id": "", "workload_source": "hpc",
			"workload_id": "789",
		},
	}, got)
}

func TestCollectorResolvesUUIDFilename(t *testing.T) {
	const uuid = "GPU-2cf69c7e-0d83-51f3-6d41-d3f7a6b08cb7"
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewCollector(staticReader{
		mapping:           Mapping{gpuJobMapping(uuid, "", "123")},
		gpuIdentifierType: GPUIdentifierUUID,
	}, staticGPUUUIDProvider(map[string]string{"0": uuid})))

	scraper, err := pkgmetricsscraper.NewPrometheusScraper(registry)
	require.NoError(t, err)
	metrics, err := scraper.Scrape(context.Background())
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.Equal(t, "0", metrics[0].Labels["gpu"])
	require.Equal(t, uuid, metrics[0].Labels["uuid"])
	require.Equal(t, "123", metrics[0].Labels["workload_id"])
}

func TestCollectorPreservesGPUInstanceID(t *testing.T) {
	const uuid = "GPU-2cf69c7e-0d83-51f3-6d41-d3f7a6b08cb7"
	tests := []struct {
		name              string
		gpuIdentifierType GPUIdentifierType
		mapping           Mapping
		wantInstanceID    string
	}{
		{
			name:              "DCGM index filename",
			gpuIdentifierType: GPUIdentifierDCGMIndex,
			mapping:           Mapping{gpuJobMapping("2", "1", "123")},
			wantInstanceID:    "1",
		},
		{
			name:              "GPU UUID filename",
			gpuIdentifierType: GPUIdentifierUUID,
			mapping:           Mapping{gpuJobMapping(uuid, "3", "123")},
			wantInstanceID:    "3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			registry.MustRegister(NewCollector(
				staticReader{mapping: tt.mapping, gpuIdentifierType: tt.gpuIdentifierType},
				staticGPUUUIDProvider(map[string]string{"2": uuid}),
			))

			scraper, err := pkgmetricsscraper.NewPrometheusScraper(registry)
			require.NoError(t, err)
			metrics, err := scraper.Scrape(context.Background())
			require.NoError(t, err)
			require.Len(t, metrics, 1)
			require.Equal(t, "2", metrics[0].Labels["gpu"])
			require.Equal(t, uuid, metrics[0].Labels["uuid"])
			require.Equal(t, tt.wantInstanceID, metrics[0].Labels["gpu_instance_id"])
			require.Equal(t, "123", metrics[0].Labels["workload_id"])
		})
	}
}

func TestCollectorUsesConfiguredGPUIdentifierType(t *testing.T) {
	tests := []struct {
		name              string
		gpuIdentifierType GPUIdentifierType
		mapping           Mapping
	}{
		{
			name:              "index mode does not accept UUID",
			gpuIdentifierType: GPUIdentifierDCGMIndex,
			mapping:           Mapping{gpuJobMapping("GPU-a", "", "123")},
		},
		{
			name:              "UUID mode does not accept index",
			gpuIdentifierType: GPUIdentifierUUID,
			mapping:           Mapping{gpuJobMapping("0", "", "123")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := prometheus.NewRegistry()
			registry.MustRegister(NewCollector(
				staticReader{mapping: tt.mapping, gpuIdentifierType: tt.gpuIdentifierType},
				staticGPUUUIDProvider(map[string]string{"0": "GPU-a"}),
			))

			families, err := registry.Gather()
			require.NoError(t, err)
			require.Empty(t, families)
		})
	}
}

func TestCollectorDoesNotModifyExistingMetrics(t *testing.T) {
	baseRegistry := prometheus.NewRegistry()
	gpuMetric := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "gpu_metric"}, []string{"gpu"})
	baseRegistry.MustRegister(gpuMetric)
	gpuMetric.WithLabelValues("0").Set(10)

	jobRegistry := prometheus.NewRegistry()
	jobRegistry.MustRegister(NewCollector(
		staticReader{mapping: Mapping{gpuJobMapping("0", "", "123")}},
		staticGPUUUIDProvider(map[string]string{"0": "GPU-a"}),
	))

	families, err := prometheus.Gatherers{baseRegistry, jobRegistry}.Gather()
	require.NoError(t, err)
	require.Len(t, families, 2)
	for _, family := range families {
		if family.GetName() != "gpu_metric" {
			continue
		}
		require.Len(t, family.GetMetric(), 1)
		require.Len(t, family.GetMetric()[0].GetLabel(), 1)
		require.Equal(t, "gpu", family.GetMetric()[0].GetLabel()[0].GetName())
	}
}

func TestCollectorReaderFailureIsNonFatal(t *testing.T) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewCollector(staticReader{err: errors.New("unavailable")}, nil))

	families, err := registry.Gather()
	require.NoError(t, err)
	require.Empty(t, families)
}

func TestCollectorSkipsUnknownGPUs(t *testing.T) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewCollector(staticReader{mapping: Mapping{
		gpuJobMapping("0", "", "known"),
		gpuJobMapping("1", "", "missing"),
		gpuJobMapping("2", "", "empty-uuid"),
		gpuJobMapping("GPU-unknown", "", "missing-uuid"),
	}}, staticGPUUUIDProvider(map[string]string{
		"0": "GPU-a",
		"2": "",
	})))

	scraper, err := pkgmetricsscraper.NewPrometheusScraper(registry)
	require.NoError(t, err)
	metrics, err := scraper.Scrape(context.Background())
	require.NoError(t, err)
	require.Len(t, metrics, 1)
	require.Equal(t, "known", metrics[0].Labels["workload_id"])
	require.Equal(t, "GPU-a", metrics[0].Labels["uuid"])
	require.Equal(t, workloadSource, metrics[0].Labels["workload_source"])
	require.NotContains(t, metrics[0].Labels, "workload_kind")
	require.NotContains(t, metrics[0].Labels, "workload_namespace")
}

func TestCollectorReflectsJobLifecycle(t *testing.T) {
	reader := &mutableReader{}
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewCollector(reader, staticGPUUUIDProvider(map[string]string{"0": "GPU-a"})))

	workloadIDs := func() []string {
		families, err := registry.Gather()
		require.NoError(t, err)

		var got []string
		for _, family := range families {
			for _, metric := range family.GetMetric() {
				for _, label := range metric.GetLabel() {
					if label.GetName() == "workload_id" {
						got = append(got, label.GetValue())
					}
				}
			}
		}
		return got
	}

	reader.Set(Mapping{gpuJobMapping("0", "", "123")})
	require.ElementsMatch(t, []string{"123"}, workloadIDs())

	reader.Set(Mapping{gpuJobMapping("0", "", "456")})
	require.ElementsMatch(t, []string{"456"}, workloadIDs())

	reader.Set(Mapping{})
	require.Empty(t, workloadIDs())
}

func TestCollectorRecoversWhenGPUInventoryBecomesAvailable(t *testing.T) {
	gpuUUIDByIndex := map[string]string{}
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewCollector(
		staticReader{mapping: Mapping{gpuJobMapping("0", "", "123")}},
		func() map[string]string { return gpuUUIDByIndex },
	))

	families, err := registry.Gather()
	require.NoError(t, err)
	require.Empty(t, families)

	gpuUUIDByIndex = map[string]string{"0": ""}
	families, err = registry.Gather()
	require.NoError(t, err)
	require.Empty(t, families)

	gpuUUIDByIndex = map[string]string{"0": "GPU-a"}
	families, err = registry.Gather()
	require.NoError(t, err)
	require.Len(t, families, 1)
	require.Len(t, families[0].GetMetric(), 1)

	labels := make(map[string]string)
	for _, label := range families[0].GetMetric()[0].GetLabel() {
		labels[label.GetName()] = label.GetValue()
	}
	require.Equal(t, "GPU-a", labels["uuid"])
	require.Equal(t, "123", labels["workload_id"])
}

func TestCollectorStopsEmittingRemovedMappingFile(t *testing.T) {
	dir := t.TempDir()
	mappingFile := filepath.Join(dir, "0")
	require.NoError(t, os.WriteFile(mappingFile, []byte("123\n"), 0o600))

	registry := prometheus.NewRegistry()
	registry.MustRegister(NewCollector(
		NewFileReader(dir, GPUIdentifierDCGMIndex),
		staticGPUUUIDProvider(map[string]string{"0": "GPU-a"}),
	))

	families, err := registry.Gather()
	require.NoError(t, err)
	require.Len(t, families, 1)
	require.Len(t, families[0].GetMetric(), 1)

	require.NoError(t, os.Remove(mappingFile))
	families, err = registry.Gather()
	require.NoError(t, err)
	require.Empty(t, families)
}

func staticGPUUUIDProvider(gpuUUIDByIndex map[string]string) func() map[string]string {
	return func() map[string]string {
		return gpuUUIDByIndex
	}
}

func gpuJobMapping(physicalGPUIdentifier, gpuInstanceID string, jobIDs ...string) GPUJobMapping {
	return GPUJobMapping{
		PhysicalGPUIdentifier: physicalGPUIdentifier,
		GPUInstanceID:         gpuInstanceID,
		JobIDs:                jobIDs,
	}
}
