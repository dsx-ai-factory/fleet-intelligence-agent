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

// Package hpcjob reads scheduler-maintained GPU-to-job mapping files and
// exposes normalized workload attribution metrics.
package hpcjob

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	maxMappingFileSize = 1 << 20
	maxJobIDLength     = 256
	// maxJobIDsPerGPU bounds the number of identity series one mapping file can create.
	maxJobIDsPerGPU = 64
)

// GPUIdentifierType selects how mapping filenames identify a GPU.
type GPUIdentifierType string

const (
	GPUIdentifierDCGMIndex GPUIdentifierType = "dcgm_index"
	GPUIdentifierUUID      GPUIdentifierType = "uuid"
)

var (
	dcgmIndexMappingFileRE = regexp.MustCompile(`^[0-9]+$`)
	uuidMappingFileRE      = regexp.MustCompile(`^GPU-[[:xdigit:]-]+$`)
)

// Mapping maps a whole-GPU identifier to its active job IDs. An identifier is
// either the numeric DCGM GPU ID used by the gpu metric label or a GPU UUID.
type Mapping map[string][]string

// Reader returns the current scheduler-maintained GPU-to-job mapping.
type Reader interface {
	Read() (Mapping, error)
}

// FileReader reads the file convention used by dcgm-exporter and FleetInt's
// UUID extension. Each regular file is named for a numeric DCGM GPU ID or GPU
// UUID and contains one job ID per line.
type FileReader struct {
	directory         string
	gpuIdentifierType GPUIdentifierType
}

// NewFileReader creates a reader for directory.
func NewFileReader(directory string, gpuIdentifierType GPUIdentifierType) *FileReader {
	return &FileReader{directory: directory, gpuIdentifierType: gpuIdentifierType}
}

// Read reads a consistent-enough snapshot of the mapping directory. Mapping
// writers are expected to use locking and atomic replacement.
func (r *FileReader) Read() (Mapping, error) {
	directory, err := os.Open(r.directory)
	if err != nil {
		if os.IsNotExist(err) {
			return Mapping{}, nil
		}
		return nil, fmt.Errorf("open job mapping directory: %w", err)
	}
	defer directory.Close()

	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("read job mapping directory: %w", err)
	}

	mapping := make(Mapping)
	for _, entry := range entries {
		if !r.matchesGPUIdentifier(entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
			continue
		}

		jobIDs, err := readJobIDs(directory, entry.Name())
		if err != nil {
			continue
		}
		if len(jobIDs) > 0 {
			mapping[entry.Name()] = jobIDs
		}
	}
	return mapping, nil
}

func (r *FileReader) matchesGPUIdentifier(filename string) bool {
	switch r.gpuIdentifierType {
	case GPUIdentifierDCGMIndex:
		return dcgmIndexMappingFileRE.MatchString(filename)
	case GPUIdentifierUUID:
		return uuidMappingFileRE.MatchString(filename)
	default:
		return false
	}
}

func readJobIDs(directory *os.File, filename string) ([]string, error) {
	if directory == nil || filename == "" || filename == "." || filename == ".." || strings.ContainsRune(filename, os.PathSeparator) {
		return nil, fmt.Errorf("invalid mapping filename %q", filename)
	}

	fd, err := unix.Openat(
		int(directory.Fd()),
		filename,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
		0,
	)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), filename)
	if f == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open mapping file %q", filename)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("mapping file %q is not a regular file", filename)
	}
	if info.Size() > maxMappingFileSize {
		return nil, fmt.Errorf("mapping file exceeds %d bytes", maxMappingFileSize)
	}

	limited := io.LimitReader(f, maxMappingFileSize)
	scanner := bufio.NewScanner(limited)
	seen := make(map[string]struct{})
	for scanner.Scan() {
		jobID := strings.TrimSpace(scanner.Text())
		if jobID == "" || len(jobID) > maxJobIDLength || !utf8.ValidString(jobID) {
			continue
		}
		if _, found := seen[jobID]; found {
			continue
		}
		if len(seen) >= maxJobIDsPerGPU {
			return nil, fmt.Errorf("mapping file exceeds %d unique job IDs", maxJobIDsPerGPU)
		}
		seen[jobID] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	jobIDs := make([]string, 0, len(seen))
	for jobID := range seen {
		jobIDs = append(jobIDs, jobID)
	}
	sort.Strings(jobIDs)
	return jobIDs, nil
}
