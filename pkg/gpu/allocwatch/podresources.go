// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
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

package allocwatch

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	podresourcesv1 "k8s.io/kubelet/pkg/apis/podresources/v1"
)

// DefaultSocketPath is where the kubelet serves the pod-resources API. The
// socket is node-local and read-only, so it needs a hostPath mount rather than
// any API-server RBAC.
const DefaultSocketPath = "/var/lib/kubelet/pod-resources/kubelet.sock"

// GPUResourceName is the extended resource the NVIDIA device plugin advertises.
// pod-resources reports device IDs per resource name and a node commonly serves
// others (SR-IOV NICs, hugepages, other vendors' accelerators), so this name is
// the filter that keeps the watcher to GPUs.
const GPUResourceName = "nvidia.com/gpu"

// Lister reads the node's current GPU allocation.
type Lister interface {
	List(ctx context.Context) ([]Claim, error)
	Close() error
}

type podResourcesLister struct {
	conn   *grpc.ClientConn
	client podresourcesv1.PodResourcesListerClient
}

// NewPodResourcesLister dials the kubelet pod-resources socket.
//
// Chosen over the NRI plugin because this API is level-triggered: one call
// returns the node's complete current allocation, so a reader that misses a
// window recovers on the next poll rather than carrying a stale delta forever.
// It also reports device-plugin, DRA and NRI allocations alike, and does not
// depend on nri.enabled, which ships false.
//
// The dial blocks so a missing or unmountable socket fails at startup. The
// alternative — a lazy connection — would leave the watcher reporting an idle
// node indefinitely, which is indistinguishable from a genuinely idle node.
func NewPodResourcesLister(ctx context.Context, socketPath string) (Lister, error) {
	if socketPath == "" {
		socketPath = DefaultSocketPath
	}

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, "unix://"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("dial kubelet pod-resources at %s: %w", socketPath, err)
	}

	return &podResourcesLister{
		conn:   conn,
		client: podresourcesv1.NewPodResourcesListerClient(conn),
	}, nil
}

// List returns one Claim per (container, device) pair holding a GPU.
//
// Deliberately NOT deduplicated. A time-sliced GPU is handed to several
// containers and each holder is a separate claim that Reconcile adds up;
// collapsing them here would silently under-report a shared card.
func (l *podResourcesLister) List(ctx context.Context) ([]Claim, error) {
	resp, err := l.client.List(ctx, &podresourcesv1.ListPodResourcesRequest{})
	if err != nil {
		return nil, fmt.Errorf("list pod resources: %w", err)
	}

	var claims []Claim
	for _, pod := range resp.GetPodResources() {
		for _, container := range pod.GetContainers() {
			for _, dev := range container.GetDevices() {
				if dev.GetResourceName() != GPUResourceName {
					continue
				}
				for _, id := range dev.GetDeviceIds() {
					claims = append(claims, Claim{
						DeviceUUID: id,
						Namespace:  pod.GetNamespace(),
						Pod:        pod.GetName(),
						Container:  container.GetName(),
					})
				}
			}
		}
	}
	return claims, nil
}

func (l *podResourcesLister) Close() error {
	if l.conn == nil {
		return nil
	}
	return l.conn.Close()
}
