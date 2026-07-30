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
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	podresourcesv1 "k8s.io/kubelet/pkg/apis/podresources/v1"
)

// fakeKubelet serves the real pod-resources gRPC service over a real unix
// socket. Only the kubelet itself is substituted: the transport, the protobuf
// wire format and the generated client are the production ones, so a field name
// or resource-name filter that is wrong here is wrong in a cluster too.
type fakeKubelet struct {
	podresourcesv1.UnimplementedPodResourcesListerServer
	resp *podresourcesv1.ListPodResourcesResponse
	err  error
}

func (f *fakeKubelet) List(context.Context, *podresourcesv1.ListPodResourcesRequest) (
	*podresourcesv1.ListPodResourcesResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

// startFakeKubelet returns the socket path of a running fake, torn down with t.
func startFakeKubelet(t *testing.T, fake *fakeKubelet) string {
	t.Helper()
	// A unix socket path is capped at ~104 bytes on darwin and 108 on Linux.
	// t.TempDir() embeds the (long) test name and blows past that, so allocate
	// a short directory instead of deriving one from the test name.
	dir, err := os.MkdirTemp("", "prl")
	require.NoError(t, err, "temp dir for the fake kubelet socket")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "k.sock")
	lis, err := net.Listen("unix", sock)
	require.NoError(t, err, "listen on %s (%d bytes)", sock, len(sock))

	srv := grpc.NewServer()
	podresourcesv1.RegisterPodResourcesListerServer(srv, fake)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return sock
}

func gpuDevices(ids ...string) []*podresourcesv1.ContainerDevices {
	return []*podresourcesv1.ContainerDevices{
		{ResourceName: GPUResourceName, DeviceIds: ids},
	}
}

func TestPodResourcesLister_ReportsOneClaimPerHoldingContainer(t *testing.T) {
	fake := &fakeKubelet{resp: &podresourcesv1.ListPodResourcesResponse{
		PodResources: []*podresourcesv1.PodResources{
			{
				Name: "trainer", Namespace: "ml",
				Containers: []*podresourcesv1.ContainerResources{
					{Name: "app", Devices: gpuDevices("GPU-aaa")},
				},
			},
		},
	}}
	sock := startFakeKubelet(t, fake)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	lister, err := NewPodResourcesLister(ctx, sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lister.Close() })

	claims, err := lister.List(ctx)
	require.NoError(t, err)

	require.Equal(t, []Claim{{
		DeviceUUID: "GPU-aaa", Namespace: "ml", Pod: "trainer", Container: "app",
	}}, claims, "pod and container identity must survive; #506 item 2 needs them for `processes`")
}

// The filter that keeps this watcher to GPUs. A node commonly advertises other
// extended resources through the same API; attributing an SR-IOV NIC or another
// vendor's accelerator to a GPU index would fabricate memory usage.
func TestPodResourcesLister_IgnoresNonGPUResources(t *testing.T) {
	fake := &fakeKubelet{resp: &podresourcesv1.ListPodResourcesResponse{
		PodResources: []*podresourcesv1.PodResources{
			{
				Name: "netapp", Namespace: "infra",
				Containers: []*podresourcesv1.ContainerResources{
					{Name: "app", Devices: []*podresourcesv1.ContainerDevices{
						{ResourceName: "intel.com/sriov_netdevice", DeviceIds: []string{"net-0"}},
						{ResourceName: "amd.com/gpu", DeviceIds: []string{"amd-0"}},
					}},
				},
			},
		},
	}}
	sock := startFakeKubelet(t, fake)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	lister, err := NewPodResourcesLister(ctx, sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lister.Close() })

	claims, err := lister.List(ctx)
	require.NoError(t, err)
	require.Empty(t, claims,
		"only %s must be collected; another vendor's device was attributed to a GPU", GPUResourceName)
}

// Time slicing: one physical GPU handed to two containers. Both holders must
// survive as separate claims, because Reconcile adds them up.
func TestPodResourcesLister_KeepsDuplicateDeviceIDs(t *testing.T) {
	fake := &fakeKubelet{resp: &podresourcesv1.ListPodResourcesResponse{
		PodResources: []*podresourcesv1.PodResources{
			{
				Name: "a", Namespace: "ml",
				Containers: []*podresourcesv1.ContainerResources{
					{Name: "app", Devices: gpuDevices("GPU-shared")},
				},
			},
			{
				Name: "b", Namespace: "ml",
				Containers: []*podresourcesv1.ContainerResources{
					{Name: "app", Devices: gpuDevices("GPU-shared")},
				},
			},
		},
	}}
	sock := startFakeKubelet(t, fake)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	lister, err := NewPodResourcesLister(ctx, sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lister.Close() })

	claims, err := lister.List(ctx)
	require.NoError(t, err)
	require.Len(t, claims, 2,
		"deduplicating GPU-shared would under-report a time-sliced card by half")
	require.Equal(t, "a", claims[0].Pod)
	require.Equal(t, "b", claims[1].Pod)
}

// A pod with no GPU request appears in the response with an empty device list.
// It must not become a claim.
func TestPodResourcesLister_IgnoresPodsHoldingNoDevices(t *testing.T) {
	fake := &fakeKubelet{resp: &podresourcesv1.ListPodResourcesResponse{
		PodResources: []*podresourcesv1.PodResources{
			{
				Name: "plain", Namespace: "default",
				Containers: []*podresourcesv1.ContainerResources{{Name: "app"}},
			},
		},
	}}
	sock := startFakeKubelet(t, fake)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	lister, err := NewPodResourcesLister(ctx, sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lister.Close() })

	claims, err := lister.List(ctx)
	require.NoError(t, err)
	require.Empty(t, claims)
}

// An empty node yields an empty claim list, NOT an error. This is the reading
// that drives every GPU back to idle, so turning it into an error would strand
// the last busy value forever.
func TestPodResourcesLister_EmptyNodeIsNotAnError(t *testing.T) {
	sock := startFakeKubelet(t, &fakeKubelet{resp: &podresourcesv1.ListPodResourcesResponse{}})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	lister, err := NewPodResourcesLister(ctx, sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lister.Close() })

	claims, err := lister.List(ctx)
	require.NoError(t, err, "an idle node must not be an error")
	require.Empty(t, claims)
}

func TestNewPodResourcesLister_FailsOnMissingSocket(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := NewPodResourcesLister(ctx, filepath.Join(t.TempDir(), "absent.sock"))
	require.Error(t, err,
		"a missing socket must fail at startup, not silently report an idle node forever")
}
