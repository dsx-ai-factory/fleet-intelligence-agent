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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	podresourcesapi "k8s.io/kubelet/pkg/apis/podresources/v1"
)

var (
	errNodeNameRequired     = errors.New("NODE_NAME is required for Kubernetes workload attribution")
	errPodMetadataNotSynced = errors.New("kubernetes pod metadata cache is not synchronized")
)

const (
	kubeletPodResourcesMaxRecvMsgSize = 16 * 1024 * 1024
	nvidiaGPUResourceName             = "nvidia.com/gpu"
	nvidiaMIGResourcePrefix           = "nvidia.com/mig-"
)

type podResourcesReader struct {
	connection *grpc.ClientConn
	client     podresourcesapi.PodResourcesListerClient
}

func newPodResourcesReader(socketPath string) (*podResourcesReader, error) {
	if !filepath.IsAbs(socketPath) {
		return nil, fmt.Errorf("kubernetes pod-resources socket path %q must be absolute", socketPath)
	}
	connection, err := grpc.NewClient(
		"unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes pod-resources client: %w", err)
	}
	return &podResourcesReader{
		connection: connection,
		client:     podresourcesapi.NewPodResourcesListerClient(connection),
	}, nil
}

func (r *podResourcesReader) List(ctx context.Context) ([]GPUAllocation, error) {
	response, err := r.client.List(
		ctx,
		&podresourcesapi.ListPodResourcesRequest{},
		grpc.MaxCallRecvMsgSize(kubeletPodResourcesMaxRecvMsgSize),
	)
	if err != nil {
		return nil, fmt.Errorf("list kubelet pod resources: %w", err)
	}

	var allocations []GPUAllocation
	for _, pod := range response.GetPodResources() {
		for _, container := range pod.GetContainers() {
			for _, device := range container.GetDevices() {
				if !isGPUResourceName(device.GetResourceName()) {
					continue
				}
				for _, deviceID := range device.GetDeviceIds() {
					if deviceID == "" {
						continue
					}
					allocations = append(allocations, GPUAllocation{
						Namespace:     pod.GetNamespace(),
						PodName:       pod.GetName(),
						ContainerName: container.GetName(),
						DeviceID:      deviceID,
					})
				}
			}
		}
	}
	return allocations, nil
}

func isGPUResourceName(resourceName string) bool {
	return resourceName == nvidiaGPUResourceName ||
		strings.HasPrefix(resourceName, nvidiaGPUResourceName+".") ||
		strings.HasPrefix(resourceName, nvidiaMIGResourcePrefix)
}

func (r *podResourcesReader) Close() error {
	if r == nil || r.connection == nil {
		return nil
	}
	return r.connection.Close()
}

type inClusterPodLabelReader struct {
	lister    corev1listers.PodLister
	hasSynced cache.InformerSynced
	stop      chan struct{}
	closeOnce sync.Once
}

func newInClusterPodLabelReader() (*inClusterPodLabelReader, error) {
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		return nil, errNodeNameRequired
	}
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster Kubernetes configuration: %w", err)
	}
	client, err := corev1client.NewForConfig(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}
	fieldSelector := fields.OneTermEqualSelector("spec.nodeName", nodeName).String()
	listWatch := cache.NewFilteredListWatchFromClient(
		client.RESTClient(),
		"pods",
		metav1.NamespaceAll,
		func(options *metav1.ListOptions) {
			options.FieldSelector = fieldSelector
		},
	)
	return newPodLabelReader(listWatch), nil
}

func newPodLabelReader(listWatch cache.ListerWatcher) *inClusterPodLabelReader {
	informer := cache.NewSharedIndexInformer(
		listWatch,
		&corev1.Pod{},
		0,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc},
	)
	reader := &inClusterPodLabelReader{
		lister:    corev1listers.NewPodLister(informer.GetIndexer()),
		hasSynced: informer.HasSynced,
		stop:      make(chan struct{}),
	}
	go informer.Run(reader.stop)
	return reader
}

func (r *inClusterPodLabelReader) Labels(ctx context.Context, namespace, podName string) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.hasSynced == nil || !r.hasSynced() {
		return nil, errPodMetadataNotSynced
	}
	pod, err := r.lister.Pods(namespace).Get(podName)
	if err != nil {
		return nil, err
	}
	return pod.Labels, nil
}

func (r *inClusterPodLabelReader) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		close(r.stop)
	})
	return nil
}
