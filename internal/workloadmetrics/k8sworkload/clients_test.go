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

package k8sworkload

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	podresourcesapi "k8s.io/kubelet/pkg/apis/podresources/v1"
)

func TestPodLabelReaderReportsUnsynchronizedCache(t *testing.T) {
	reader := &inClusterPodLabelReader{hasSynced: func() bool { return false }}

	_, err := reader.Labels(context.Background(), "ml", "worker")
	require.True(t, errors.Is(err, errPodMetadataNotSynced))
}

func TestPodResourcesReaderListsContainerDeviceIDs(t *testing.T) {
	reader := &podResourcesReader{client: &fakePodResourcesClient{response: &podresourcesapi.ListPodResourcesResponse{
		PodResources: []*podresourcesapi.PodResources{{
			Name:      "worker-0",
			Namespace: "ml",
			Containers: []*podresourcesapi.ContainerResources{{
				Name: "trainer",
				Devices: []*podresourcesapi.ContainerDevices{{
					ResourceName: "nvidia.com/gpu",
					DeviceIds:    []string{"GPU-abc", "GPU-def"},
				}, {
					ResourceName: "example.com/other-device",
					DeviceIds:    []string{"GPU-abc"},
				}},
			}},
		}},
	}}}

	allocations, err := reader.List(context.Background())
	require.NoError(t, err)
	require.Equal(t, []GPUAllocation{
		{Namespace: "ml", PodName: "worker-0", ContainerName: "trainer", DeviceID: "GPU-abc"},
		{Namespace: "ml", PodName: "worker-0", ContainerName: "trainer", DeviceID: "GPU-def"},
	}, allocations)
}

type fakePodResourcesClient struct {
	response *podresourcesapi.ListPodResourcesResponse
	err      error
}

func (c *fakePodResourcesClient) List(
	context.Context,
	*podresourcesapi.ListPodResourcesRequest,
	...grpc.CallOption,
) (*podresourcesapi.ListPodResourcesResponse, error) {
	return c.response, c.err
}

func (c *fakePodResourcesClient) GetAllocatableResources(
	context.Context,
	*podresourcesapi.AllocatableResourcesRequest,
	...grpc.CallOption,
) (*podresourcesapi.AllocatableResourcesResponse, error) {
	return nil, nil
}

func (c *fakePodResourcesClient) Get(
	context.Context,
	*podresourcesapi.GetPodResourcesRequest,
	...grpc.CallOption,
) (*podresourcesapi.GetPodResourcesResponse, error) {
	return nil, nil
}
