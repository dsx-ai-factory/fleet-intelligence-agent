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

package dcgmversion

import (
	"errors"
	"fmt"
	"strings"

	dcgmendpoint "github.com/NVIDIA/fleet-intelligence-sdk/pkg/nvidia-query/dcgm/endpoint"
	godcgm "github.com/NVIDIA/go-dcgm/pkg/dcgm"
)

// DetectHostengineVersion returns the DCGM HostEngine version for the current
// environment. It initializes a standalone DCGM connection and extracts the
// semantic version from the build info string.
func DetectHostengineVersion() (string, error) {
	var initErrors []error
	for _, candidate := range dcgmendpoint.ResolveFromEnv() {
		cleanup, err := godcgm.Init(godcgm.Standalone, candidate.Address, candidate.UnixSocketFlag())
		if err != nil {
			initErrors = append(initErrors, fmt.Errorf("%s: %w", candidate.Address, err))
			continue
		}

		versionInfo, err := godcgm.GetHostengineVersionInfo()
		cleanup()
		if err != nil {
			initErrors = append(initErrors, fmt.Errorf("%s: %w", candidate.Address, err))
			continue
		}
		version, err := extractVersion(versionInfo.RawBuildInfoString)
		if err != nil {
			initErrors = append(initErrors, fmt.Errorf("%s: %w", candidate.Address, err))
			continue
		}
		return version, nil
	}
	return "", fmt.Errorf("failed to query any DCGM HostEngine endpoint: %w", errors.Join(initErrors...))
}

func extractVersion(raw string) (string, error) {
	for _, pair := range strings.Split(raw, ";") {
		key, value, ok := strings.Cut(pair, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) == "version" {
			version := strings.TrimSpace(value)
			if version != "" {
				return version, nil
			}
			break
		}
	}

	return "", errors.New("version missing from DCGM HostEngine build information")
}
