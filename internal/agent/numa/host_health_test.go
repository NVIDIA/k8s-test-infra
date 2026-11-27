// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/allocwatch"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	podresourcesv1 "k8s.io/kubelet/pkg/apis/podresources/v1"
)

type healthKubelet struct {
	podresourcesv1.UnimplementedPodResourcesListerServer
	err error
}

func (k *healthKubelet) List(context.Context, *podresourcesv1.ListPodResourcesRequest) (*podresourcesv1.ListPodResourcesResponse, error) {
	if k.err != nil {
		return nil, k.err
	}
	return &podresourcesv1.ListPodResourcesResponse{}, nil
}

func healthTestHost(t *testing.T) *host.Host {
	t.Helper()
	// Unix socket paths must fit within the platform's ~104-byte limit.
	dir, err := os.MkdirTemp("/tmp", "numa-health-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	h := host.New(dir)
	require.NoError(t, os.MkdirAll(filepath.Dir(h.HostPath(allocwatch.DefaultSocketPath)), 0700))
	return h
}

func serveHealthKubelet(t *testing.T, h *host.Host, rpcErr error) {
	t.Helper()
	listener, err := net.Listen("unix", h.HostPath(allocwatch.DefaultSocketPath))
	require.NoError(t, err)
	server := grpc.NewServer()
	podresourcesv1.RegisterPodResourcesListerServer(server, &healthKubelet{err: rpcErr})
	go func() { _ = server.Serve(listener) }() // Stop closes the listener at cleanup.
	t.Cleanup(server.Stop)
}

func TestKubeletHealthAcceptsEmptyResourceList(t *testing.T) {
	t.Parallel()
	h := healthTestHost(t)
	serveHealthKubelet(t, h, nil)
	s := newHostSetup(h, hostOperations{run: func(_ context.Context, name string, args ...string) error {
		require.Equal(t, "systemctl", name)
		require.Equal(t, []string{"is-active", "--quiet", "kubelet"}, args)
		return nil
	}})
	require.NoError(t, s.waitForKubelet(t.Context()))
}

func TestKubeletHealthRequiresSuccessfulRPC(t *testing.T) {
	t.Parallel()
	h := healthTestHost(t)
	serveHealthKubelet(t, h, status.Error(codes.Unavailable, "resource API unavailable"))
	require.ErrorContains(t, kubeletHealth(t.Context(), h.HostPath(allocwatch.DefaultSocketPath)), "resource API unavailable")
	s := newHostSetup(h, hostOperations{run: func(context.Context, string, ...string) error { return nil }})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, s.waitForKubelet(ctx), context.DeadlineExceeded)
}

func TestKubeletHealthRejectsMissingOrStaleSocket(t *testing.T) {
	t.Parallel()
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "stale"}[stale], func(t *testing.T) {
			t.Parallel()
			h := healthTestHost(t)
			socket := h.HostPath(allocwatch.DefaultSocketPath)
			if stale {
				require.NoError(t, fsutil.Write(socket, nil, 0600))
			}
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			require.ErrorIs(t, kubeletHealth(ctx, socket), context.DeadlineExceeded)
		})
	}
}
