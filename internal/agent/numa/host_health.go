// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/allocwatch"
)

func (s *hostSetup) waitForKubelet(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var lastErr error
	for {
		lastErr = s.systemctl(ctx, "is-active", "--quiet", "kubelet")
		if lastErr == nil {
			lastErr = kubeletHealth(ctx, s.host.HostPath(allocwatch.DefaultSocketPath))
		}
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("kubelet did not become healthy: %w", errors.Join(ctx.Err(), lastErr))
		case <-time.After(time.Second):
		}
	}
}

func kubeletHealth(ctx context.Context, socket string) (result error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, err := allocwatch.NewPodResourcesLister(ctx, socket)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, client.Close()) }()
	// A successful RPC proves the API responds; an empty node is healthy too.
	_, result = client.List(ctx)
	return result
}
