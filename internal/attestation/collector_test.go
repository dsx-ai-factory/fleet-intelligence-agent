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

package attestation

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/backendclient"
)

func TestValidateNonce(t *testing.T) {
	require.NoError(t, validateNonce("abc123-_=/+"))
	require.Error(t, validateNonce(""))
	require.Error(t, validateNonce("bad nonce"))
}

func TestCLIEvidenceCollectorRejectsInvalidNonce(t *testing.T) {
	collector := NewCLIEvidenceCollector(time.Second)
	_, err := collector.Collect(context.Background(), "bad nonce")
	require.Error(t, err)
}

func TestCLIEvidenceCollectorParsesResponse(t *testing.T) {
	original := execCommandContext
	t.Cleanup(func() { execCommandContext = original })
	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		argv := append([]string{"-test.run=^TestHelperProcess$", "--", name}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], argv...)
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		return cmd
	}

	collector := NewCLIEvidenceCollector(time.Second)
	resp, err := collector.Collect(context.Background(), "abc123")
	require.NoError(t, err)
	require.Equal(t, 200, resp.ResultCode)
	require.Contains(t, resp.ResultMessage, "ok | raw_stderr:")
	require.Len(t, resp.Evidences, 1)
	require.Equal(t, "BLACKWELL", resp.Evidences[0].Arch)
	require.Equal(t, "550.120", resp.Evidences[0].DriverVersion)
	require.Equal(t, "97.00.B9.00.69", resp.Evidences[0].VBIOSVersion)
}

func TestCLIEvidenceCollectorExecutionAndParseErrors(t *testing.T) {
	original := execCommandContext
	t.Cleanup(func() { execCommandContext = original })

	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		argv := append([]string{"-test.run=^TestHelperProcessError$", "--", name}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], argv...)
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "HELPER_MODE=stderr_only")
		return cmd
	}
	collector := NewCLIEvidenceCollector(time.Second)
	_, err := collector.Collect(context.Background(), "abc123")
	require.Error(t, err)

	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		argv := append([]string{"-test.run=^TestHelperProcessError$", "--", name}, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], argv...)
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "HELPER_MODE=bad_json")
		return cmd
	}
	_, err = collector.Collect(context.Background(), "abc123")
	require.Error(t, err)
}

func TestBackendNonceProviderErrors(t *testing.T) {
	_, _, _, err := NewBackendNonceProvider(nil).GetNonce(context.Background(), "node", "jwt")
	require.ErrorContains(t, err, "backend client")

	client := &testNonceClient{}
	_, _, _, err = NewBackendNonceProvider(client).GetNonce(context.Background(), "", "jwt")
	require.ErrorContains(t, err, "node UUID")
	_, _, _, err = NewBackendNonceProvider(client).GetNonce(context.Background(), "node", "")
	require.ErrorContains(t, err, "jwt")

	nilClient := &nilNonceClient{}
	_, _, _, err = NewBackendNonceProvider(nilClient).GetNonce(context.Background(), "node", "jwt")
	require.ErrorContains(t, err, "nil")
}

type nilNonceClient struct{}

func (c *nilNonceClient) GetNonce(context.Context, string, string) (*backendclient.NonceResponse, error) {
	return nil, nil
}

func TestHelperProcessError(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	switch os.Getenv("HELPER_MODE") {
	case "stderr_only":
		_, _ = os.Stderr.WriteString("boom")
		os.Exit(1)
	case "bad_json":
		_, _ = os.Stdout.WriteString("{")
		_, _ = os.Stderr.WriteString("warn")
		os.Exit(1)
	default:
		_ = errors.New("unused")
		os.Exit(2)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString(`{"evidences":[{"arch":"BLACKWELL","certificate":"cert","driver_version":"550.120","evidence":"blob","nonce":"abc123","vbios_version":"97.00.B9.00.69","version":"1.0"}],"result_code":200,"result_message":"ok"}`)
	os.Exit(0)
}
