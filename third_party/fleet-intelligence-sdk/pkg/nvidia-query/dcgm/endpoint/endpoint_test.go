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

package endpoint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want []Candidate
	}{
		{
			name: "defaults to localhost TCP",
			want: []Candidate{{Address: "localhost"}},
		},
		{
			name: "explicit TCP URL takes precedence",
			env: map[string]string{
				"DCGM_URL":  "custom-dcgm:5555",
				"DCGM_URLS": "legacy-dcgm:5555,dra-dcgm:5555",
			},
			want: []Candidate{{Address: "custom-dcgm:5555"}},
		},
		{
			name: "supports explicit Unix socket",
			env: map[string]string{
				"DCGM_URL":                "/run/dcgm/dcgm.sock",
				"DCGM_URL_IS_UNIX_SOCKET": "true",
			},
			want: []Candidate{{Address: "/run/dcgm/dcgm.sock", IsUnixSocket: true}},
		},
		{
			name: "parses validates and deduplicates TCP candidates",
			env: map[string]string{
				"DCGM_URLS": " legacy-dcgm:5555, dra-dcgm:5555,legacy-dcgm:5555,https://invalid,/tmp/socket ",
			},
			want: []Candidate{{Address: "legacy-dcgm:5555"}, {Address: "dra-dcgm:5555"}},
		},
		{
			name: "falls back to localhost when all candidates are invalid",
			env:  map[string]string{"DCGM_URLS": "https://invalid,/tmp/socket"},
			want: []Candidate{{Address: "localhost"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.env[key] }
			assert.Equal(t, tt.want, resolve(getenv))
		})
	}
}

func TestCandidateUnixSocketFlag(t *testing.T) {
	assert.Equal(t, "0", (Candidate{}).UnixSocketFlag())
	assert.Equal(t, "1", (Candidate{IsUnixSocket: true}).UnixSocketFlag())
}
