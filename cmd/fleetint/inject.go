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

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/urfave/cli"

	"github.com/dsx-ai-factory/fleet-intelligence-agent/internal/endpoint"
)

func injectCommand(c *cli.Context) error {
	component := c.String("component")
	if component == "" {
		return fmt.Errorf("component name is required")
	}

	// Get the server URL from flag
	address := c.String("server-url")
	serverURL, err := endpoint.ValidateLocalServerURL(address)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}

	// Check if clear flag is set
	clearFault := c.Bool("clear")
	if clearFault {
		return clearComponentFault(component, serverURL)
	}

	faultType := c.String("fault-type")
	if faultType == "" {
		faultType = "event"
	}

	faultMessage := c.String("fault-message")
	if faultMessage == "" {
		faultMessage = fmt.Sprintf("Injected fault for testing %s component", component)
	}

	eventType := c.String("event-type")
	if eventType == "" {
		eventType = "Fatal"
	}

	// Create the injection request
	var requestBody map[string]interface{}
	switch faultType {
	case "component-error":
		requestBody = map[string]interface{}{
			"component_error": map[string]string{
				"component": component,
				"message":   faultMessage,
			},
		}

	case "event":
		requestBody = map[string]interface{}{
			"event": map[string]string{
				"component": component,
				"name":      component,
				"type":      eventType,
				"message":   faultMessage,
			},
		}

	case "kernel-message", "kmsg":
		kernelMsg, err := getKernelMessageForComponent(component, faultMessage)
		if err != nil {
			return fmt.Errorf("failed to get kernel message for component %s: %w", component, err)
		}
		requestBody = map[string]interface{}{
			"kernel_message": map[string]string{
				"priority": "KERN_ERR",
				"message":  kernelMsg,
			},
		}

	default:
		return fmt.Errorf("invalid fault type: %s", faultType)
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make the POST request to the inject-fault endpoint
	url, err := endpoint.JoinPath(endpoint.AgentBaseURL(serverURL), "inject-fault")
	if err != nil {
		return fmt.Errorf("failed to construct inject-fault URL: %w", err)
	}
	fmt.Printf("Injecting fault into %s component at %s...\n", component, url)

	client := endpoint.NewAgentHTTPClient(serverURL)
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to make request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned error status %d", resp.StatusCode)
	}

	fmt.Printf("Successfully injected fault into %s component\n", component)
	return nil
}

// getKernelMessageForComponent returns the appropriate kernel message for a given component
func getKernelMessageForComponent(component, faultMessage string) (string, error) {
	switch component {
	case "accelerator-nvidia-error-xid":
		// XID 79: GPU has fallen off the bus (critical error)
		return "NVRM: Xid (PCI:0000:01:00): 79, GPU has fallen off the bus", nil

	case "accelerator-nvidia-error-sxid":
		// SXID 11001: Fatal ingress invalid command
		return "nvidia-nvswitch0: SXid (PCI:0000:00:00.0): 11001, Fatal, ingress invalid command", nil

	case "accelerator-nvidia-nccl":
		// NCCL segfault in libnccl.so
		return "pt_main_thread[2536443]: segfault at 7f797fe00000 ip 00007f7c7ac69996 sp 00007f7c12fd7c30 error 4 in libnccl.so.2[7f7c7ac00000+d3d3000]", nil

	case "disk":
		// Filesystem remounted read-only (critical disk error)
		return "EXT4-fs (dm-0): Remounting filesystem read-only", nil

	case "cpu":
		// CPU thermal throttling
		return "CPU0: Package temperature above threshold, cpu clock throttled (total events = 1)", nil

	case "memory":
		// Memory ECC error
		return "EDAC MC0: 1 CE memory read error on CPU_SrcID#0_Ha#0_Chan#0_DIMM#0 (channel:0 slot:0 page:0x0 offset:0x0 grain:8 syndrome:0x0)", nil

	case "os":
		// Out of memory killer
		return "Out of memory: Kill process 12345 (python3) score 900 or sacrifice child", nil

	default:
		return "", fmt.Errorf("no kernel message defined for component: %s", component)
	}
}

// clearComponentFault sends a request to clear injected faults from a component
func clearComponentFault(component string, serverURL *url.URL) error {
	// Create the clear request
	requestBody := map[string]interface{}{
		"component_clear": map[string]string{
			"component": component,
		},
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Make the POST request to the inject-fault endpoint with clear action
	url, err := endpoint.JoinPath(endpoint.AgentBaseURL(serverURL), "inject-fault")
	if err != nil {
		return fmt.Errorf("failed to construct inject-fault URL: %w", err)
	}
	fmt.Printf("Clearing fault from %s component at %s...\n", component, url)

	client := endpoint.NewAgentHTTPClient(serverURL)
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to make request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned error status %d", resp.StatusCode)
	}

	fmt.Printf("Successfully cleared fault from %s component\n", component)
	return nil
}
