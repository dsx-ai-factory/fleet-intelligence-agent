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

package sakauth

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config/configtls"
	"go.opentelemetry.io/collector/extension"
)

const (
	// typeStr is the identifier registered with the OTel Collector registry.
	typeStr = "sakauth"
)

// NewFactory returns the factory for the sakauth extension.
func NewFactory() extension.Factory {
	return extension.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		createExtension,
		component.StabilityLevelDevelopment,
	)
}

func createDefaultConfig() component.Config {
	return &Config{TLS: configtls.NewDefaultClientConfig()}
}

//nolint:gocritic // extension.CreateFunc requires Settings by value.
func createExtension(_ context.Context, _ extension.Settings, cfg component.Config) (extension.Extension, error) {
	return newSAKAuthExtension(cfg.(*Config))
}
