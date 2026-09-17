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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileReaderDCGMIndex(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "0"), []byte("123\n456\n123\n\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "GPU-2cf69c7e-0d83-51f3-6d41-d3f7a6b08cb7"), []byte("789\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "2.1"), []byte("ignored\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "GPU-not-a-uuid"), []byte("ignored\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "not-a-gpu"), []byte("ignored\n"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "3"), 0o700))
	require.NoError(t, os.Symlink(filepath.Join(dir, "0"), filepath.Join(dir, "4")))

	mapping, err := NewFileReader(dir, GPUIdentifierDCGMIndex).Read()
	require.NoError(t, err)
	require.Equal(t, Mapping{
		"0": {"123", "456"},
	}, mapping)
}

func TestFileReaderUUID(t *testing.T) {
	dir := t.TempDir()
	const uuid = "GPU-2cf69c7e-0d83-51f3-6d41-d3f7a6b08cb7"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "0"), []byte("ignored\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, uuid), []byte("123\n"), 0o600))

	mapping, err := NewFileReader(dir, GPUIdentifierUUID).Read()
	require.NoError(t, err)
	require.Equal(t, Mapping{uuid: {"123"}}, mapping)
}

func TestFileReaderUnsupportedGPUIdentifierIgnoresAllFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "0"), []byte("123\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "GPU-2cf69c7e-0d83-51f3-6d41-d3f7a6b08cb7"), []byte("456\n"), 0o600))

	mapping, err := NewFileReader(dir, GPUIdentifierType("invalid")).Read()
	require.NoError(t, err)
	require.Empty(t, mapping)
}

func TestFileReaderMissingDirectory(t *testing.T) {
	mapping, err := NewFileReader(filepath.Join(t.TempDir(), "missing"), GPUIdentifierDCGMIndex).Read()
	require.NoError(t, err)
	require.Empty(t, mapping)
}

func TestReadJobIDsRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "target"), []byte("123\n"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(dir, "target"), filepath.Join(dir, "0")))

	directory, err := os.Open(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, directory.Close()) })

	_, err = readJobIDs(directory, "0")
	require.Error(t, err)
}

func TestReadJobIDsRejectsPathTraversal(t *testing.T) {
	directory, err := os.Open(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, directory.Close()) })

	_, err = readJobIDs(directory, "../mapping")
	require.ErrorContains(t, err, "invalid mapping filename")
}

func TestFileReaderSkipsInvalidUTF8JobID(t *testing.T) {
	dir := t.TempDir()
	contents := append([]byte{0xff, '\n'}, []byte("123\n")...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "0"), contents, 0o600))

	mapping, err := NewFileReader(dir, GPUIdentifierDCGMIndex).Read()
	require.NoError(t, err)
	require.Equal(t, Mapping{"0": {"123"}}, mapping)
}

func TestFileReaderRejectsTooManyJobIDsForGPU(t *testing.T) {
	dir := t.TempDir()
	var contents strings.Builder
	for i := 0; i <= maxJobIDsPerGPU; i++ {
		fmt.Fprintln(&contents, i)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "0"), []byte(contents.String()), 0o600))

	mapping, err := NewFileReader(dir, GPUIdentifierDCGMIndex).Read()
	require.NoError(t, err)
	require.Empty(t, mapping)
}
