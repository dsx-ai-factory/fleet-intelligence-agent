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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractVersion(t *testing.T) {
	t.Run("extracts version", func(t *testing.T) {
		version, err := extractVersion("arch:x86_64;version: 4.2.3;build:123")
		assert.NoError(t, err)
		assert.Equal(t, "4.2.3", version)
	})

	t.Run("returns error when missing", func(t *testing.T) {
		version, err := extractVersion("arch:x86_64;build:123")
		assert.Error(t, err)
		assert.Empty(t, version)
	})

	t.Run("returns error when empty", func(t *testing.T) {
		version, err := extractVersion("arch:x86_64;version: ;build:123")
		assert.Error(t, err)
		assert.Empty(t, version)
	})
}
