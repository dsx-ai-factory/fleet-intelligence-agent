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
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	podresourcesapi "k8s.io/kubelet/pkg/apis/podresources/v1"
)

func TestNewPodResourcesReader(t *testing.T) {
	_, err := newPodResourcesReader("relative/kubelet.sock")
	require.ErrorContains(t, err, "must be absolute")

	reader, err := newPodResourcesReader(filepath.Join(t.TempDir(), "kubelet.sock"))
	require.NoError(t, err)
	require.NotNil(t, reader.client)
	require.NoError(t, reader.Close())
	require.NoError(t, (*podResourcesReader)(nil).Close())
}

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

func TestPodResourcesReaderHandlesErrorsAndGPUResourceVariants(t *testing.T) {
	reader := &podResourcesReader{client: &fakePodResourcesClient{err: errors.New("socket unavailable")}}
	_, err := reader.List(context.Background())
	require.ErrorContains(t, err, "list kubelet pod resources")

	reader.client = &fakePodResourcesClient{response: &podresourcesapi.ListPodResourcesResponse{
		PodResources: []*podresourcesapi.PodResources{{
			Name:      "worker-0",
			Namespace: "ml",
			Containers: []*podresourcesapi.ContainerResources{{
				Name: "trainer",
				Devices: []*podresourcesapi.ContainerDevices{
					{ResourceName: "nvidia.com/gpu.shared", DeviceIds: []string{"", "GPU-abc"}},
					{ResourceName: "nvidia.com/mig-1g.10gb", DeviceIds: []string{"MIG-abc"}},
				},
			}},
		}},
	}}
	allocations, err := reader.List(context.Background())
	require.NoError(t, err)
	require.Equal(t, []GPUAllocation{
		{Namespace: "ml", PodName: "worker-0", ContainerName: "trainer", DeviceID: "GPU-abc"},
		{Namespace: "ml", PodName: "worker-0", ContainerName: "trainer", DeviceID: "MIG-abc"},
	}, allocations)
}

func TestPodLabelReader(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	require.NoError(t, indexer.Add(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ml",
			Name:      "worker",
			Labels:    map[string]string{"job-name": "training-42"},
		},
	}))
	reader := &inClusterPodLabelReader{
		lister:    corev1listers.NewPodLister(indexer),
		hasSynced: func() bool { return true },
		stop:      make(chan struct{}),
	}

	labels, err := reader.Labels(context.Background(), "ml", "worker")
	require.NoError(t, err)
	require.Equal(t, "training-42", labels["job-name"])

	_, err = reader.Labels(context.Background(), "ml", "missing")
	require.Error(t, err)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = reader.Labels(canceled, "ml", "worker")
	require.ErrorIs(t, err, context.Canceled)

	require.NoError(t, reader.Close())
	require.NoError(t, reader.Close())
	require.NoError(t, (*inClusterPodLabelReader)(nil).Close())
}

func TestNewInClusterPodLabelReaderRequiresNodeName(t *testing.T) {
	t.Setenv("NODE_NAME", "")
	reader, err := newInClusterPodLabelReader()
	require.ErrorIs(t, err, errNodeNameRequired)
	require.Nil(t, reader)
}

func TestNewInClusterPodLabelReaderReportsMissingClusterConfiguration(t *testing.T) {
	t.Setenv("NODE_NAME", "gpu-node-1")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	reader, err := newInClusterPodLabelReader()
	require.ErrorContains(t, err, "load in-cluster Kubernetes configuration")
	require.Nil(t, reader)
}

func TestPodLabelReaderSynchronizesInformer(t *testing.T) {
	watcher := watch.NewFake()
	reader := newPodLabelReader(&cache.ListWatch{
		ListFunc: func(metav1.ListOptions) (runtime.Object, error) {
			return &corev1.PodList{Items: []corev1.Pod{{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "ml",
					Name:      "worker",
					Labels:    map[string]string{"job-name": "training-42"},
				},
			}}}, nil
		},
		WatchFunc: func(metav1.ListOptions) (watch.Interface, error) {
			return watcher, nil
		},
	})
	t.Cleanup(func() {
		watcher.Stop()
		require.NoError(t, reader.Close())
	})

	require.Eventually(t, reader.hasSynced, time.Second, 10*time.Millisecond)
	labels, err := reader.Labels(context.Background(), "ml", "worker")
	require.NoError(t, err)
	require.Equal(t, "training-42", labels["job-name"])
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
