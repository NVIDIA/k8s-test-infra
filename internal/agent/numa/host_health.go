// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

func (s *hostSetup) waitForKubelet(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: s.host.DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
	var lastErr error
	for {
		lastErr = s.systemctl(ctx, "is-active", "--quiet", "kubelet")
		if lastErr == nil {
			lastErr = kubeletHealth(ctx, client)
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

func kubeletHealth(ctx context.Context, client *http.Client) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:10248/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // A read-only health response has no state to flush.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("kubelet health status: %s", resp.Status)
	}
	return nil
}
