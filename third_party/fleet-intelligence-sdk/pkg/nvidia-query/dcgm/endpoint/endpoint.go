// SPDX-FileCopyrightText: Copyright (c) 2026, NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package endpoint resolves configured DCGM HostEngine connection candidates.
package endpoint

import (
	"os"
	"strconv"
	"strings"

	"github.com/NVIDIA/fleet-intelligence-sdk/pkg/log"
)

const defaultAddress = "localhost"

// Candidate describes one DCGM HostEngine connection candidate.
type Candidate struct {
	Address      string
	IsUnixSocket bool
}

// UnixSocketFlag returns the "0" or "1" value expected by go-dcgm.
func (c Candidate) UnixSocketFlag() string {
	if c.IsUnixSocket {
		return "1"
	}
	return "0"
}

// ResolveFromEnv resolves connection candidates using the process environment.
// DCGM_URL is an authoritative single endpoint. When it is unset, DCGM_URLS is
// parsed as an ordered, comma-separated TCP fallback list. If neither supplies
// a valid endpoint, localhost TCP is returned.
func ResolveFromEnv() []Candidate {
	return resolve(os.Getenv)
}

func resolve(getenv func(string) string) []Candidate {
	address := strings.TrimSpace(getenv("DCGM_URL"))
	if address != "" && !isValidAddress(address) {
		log.Logger.Warnw("DCGM_URL contains invalid characters, ignoring override and using fallback discovery",
			"value", address)
		address = ""
	}

	if address != "" {
		isUnixSocket, _ := strconv.ParseBool(strings.TrimSpace(getenv("DCGM_URL_IS_UNIX_SOCKET")))
		return []Candidate{{Address: address, IsUnixSocket: isUnixSocket}}
	}

	seen := make(map[string]struct{})
	candidates := make([]Candidate, 0)
	for _, rawCandidate := range strings.Split(getenv("DCGM_URLS"), ",") {
		candidate := strings.TrimSpace(rawCandidate)
		if candidate == "" {
			continue
		}
		if !isValidAddress(candidate) || strings.HasPrefix(candidate, "/") {
			log.Logger.Warnw("DCGM_URLS contains an invalid TCP address; skipping candidate", "value", candidate)
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		candidates = append(candidates, Candidate{Address: candidate})
	}
	if len(candidates) > 0 {
		return candidates
	}

	return []Candidate{{Address: defaultAddress}}
}

// isValidAddress accepts an absolute Unix socket path or a hostname/host:port
// containing only characters understood by go-dcgm. URL schemes are rejected.
func isValidAddress(address string) bool {
	if strings.HasPrefix(address, "/") {
		return true
	}
	if strings.Contains(address, "://") {
		return false
	}
	for _, char := range address {
		switch {
		case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
		case char == '.', char == '-', char == '_', char == ':', char == '[', char == ']':
		default:
			return false
		}
	}
	return true
}
