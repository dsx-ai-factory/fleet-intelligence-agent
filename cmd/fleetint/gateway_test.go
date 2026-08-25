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

	pkgmetadata "github.com/NVIDIA/fleet-intelligence-sdk/pkg/metadata"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/config"
)

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
