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

package main

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pkgmetadata "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metadata"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
)

func TestConfigureHealthExporterFromEnvDCGMHealthCheckInterval(t *testing.T) {
	t.Setenv("FLEETINT_DCGM_HEALTH_CHECK_INTERVAL", "2m")
	cfg := &config.Config{HealthExporter: &config.HealthExporterConfig{}}

	require.NoError(t, configureHealthExporterFromEnv(cfg))
	require.Equal(t, 2*time.Minute, cfg.HealthExporter.DCGMHealthCheckInterval.Duration)
}

func TestConfigureHealthExporterFromEnvCollectorEndpoint(t *testing.T) {
	t.Setenv("FLEETINT_COLLECTOR_ENDPOINT", "http://fleetint-otel-gateway:4318")
	cfg := &config.Config{HealthExporter: &config.HealthExporterConfig{}}

	require.NoError(t, configureHealthExporterFromEnv(cfg))
	require.Equal(t, "http://fleetint-otel-gateway:4318", cfg.HealthExporter.CollectorEndpoint)
}

func TestConfigureHealthExporterFromEnvPreservesCollectorEndpointWhenUnset(t *testing.T) {
	t.Setenv("FLEETINT_COLLECTOR_ENDPOINT", "")
	cfg := &config.Config{HealthExporter: &config.HealthExporterConfig{
		CollectorEndpoint: "https://collector.example",
	}}

	require.NoError(t, configureHealthExporterFromEnv(cfg))
	require.Equal(t, "https://collector.example", cfg.HealthExporter.CollectorEndpoint)
}

func TestMaskMetadataValue(t *testing.T) {
	const secret = "secret-token-value"

	require.Equal(t, pkgmetadata.MaskToken(secret), maskMetadataValue(pkgmetadata.MetadataKeyToken, secret))
	require.Equal(t, pkgmetadata.MaskToken(secret), maskMetadataValue("sak_token", secret))
	require.Equal(t, secret, maskMetadataValue("backend_base_url", secret))
}

func TestConfigureDCGMExtendedInterval(t *testing.T) {
	for _, test := range []struct {
		value string
		want  time.Duration
	}{
		{"", time.Minute}, {"60s", time.Minute}, {"5s", 5 * time.Second}, {"90s", 90 * time.Second}, {"24h", 24 * time.Hour},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv("FLEETINT_DCGM_EXTENDED_INTERVAL", test.value)
			cfg := &config.Config{HealthExporter: &config.HealthExporterConfig{DCGMExtendedInterval: metav1.Duration{Duration: time.Minute}}}
			require.NoError(t, configureHealthExporterFromEnv(cfg))
			require.Equal(t, test.want, cfg.HealthExporter.DCGMExtendedInterval.Duration)
		})
	}
	for _, value := range []string{"0s", "999ms", "25h", "extended=60s", "oops"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("FLEETINT_DCGM_EXTENDED_INTERVAL", value)
			cfg := &config.Config{HealthExporter: &config.HealthExporterConfig{}}
			require.ErrorContains(t, configureHealthExporterFromEnv(cfg), "FLEETINT_DCGM_EXTENDED_INTERVAL")
		})
	}
}

func TestConfigureDCGMFieldProfiles(t *testing.T) {
	for _, value := range []string{"", "1110=extended", "1200-1219=standard,1404=extended"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("FLEETINT_DCGM_FIELD_PROFILES", value)
			cfg := &config.Config{HealthExporter: &config.HealthExporterConfig{}}
			require.NoError(t, configureHealthExporterFromEnv(cfg))
			require.Equal(t, value, cfg.HealthExporter.DCGMFieldProfiles)
		})
	}
	for _, value := range []string{"1110=custom", "1110=30s", "1200-1219=standard,1201=extended"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("FLEETINT_DCGM_FIELD_PROFILES", value)
			cfg := &config.Config{HealthExporter: &config.HealthExporterConfig{}}
			require.ErrorContains(t, configureHealthExporterFromEnv(cfg), "FLEETINT_DCGM_FIELD_PROFILES")
			require.Empty(t, cfg.HealthExporter.DCGMFieldProfiles)
		})
	}
}
