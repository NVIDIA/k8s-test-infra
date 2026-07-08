//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package nodes models host-level (Kind node-container) exec via `docker exec`.
// This is the transport for assertions that target the node itself rather than
// a pod: the host-driver-root nvidia-smi, mock-file existence checks, and the
// CDI spec. It is deliberately distinct from pod exec (framework/kube), which
// targets in-pod IB tooling.
package nodes

import (
	"context"
	"errors"

	"github.com/NVIDIA/k8s-test-infra/tests/e2e/go/framework/runner"
)

// Docker execs into Kind node containers via the docker CLI.
type Docker struct{}

// Exec runs argv directly inside the node container.
func (Docker) Exec(ctx context.Context, node string, argv ...string) (runner.Result, error) {
	args := append([]string{"exec", node}, argv...)
	return runner.Run(ctx, "docker", args...)
}

// ExecSh runs `sh -c shCmd` inside the node container.
func (Docker) ExecSh(ctx context.Context, node, shCmd string) (runner.Result, error) {
	return runner.Run(ctx, "docker", "exec", node, "sh", "-c", shCmd)
}

// Test reports whether path exists in the node container (`test -e`). A clean
// "not found" (exit 1) returns (false, nil); anything else is a real error.
func (Docker) Test(ctx context.Context, node, path string) (bool, error) {
	_, err := runner.Run(ctx, "docker", "exec", node, "test", "-e", path)
	if err == nil {
		return true, nil
	}
	var ce *runner.CmdError
	if errors.As(err, &ce) && ce.ExitCode == 1 {
		return false, nil
	}
	return false, err
}

// TestSymlink reports whether path is a symlink in the node container (`test -L`).
func (Docker) TestSymlink(ctx context.Context, node, path string) (bool, error) {
	_, err := runner.Run(ctx, "docker", "exec", node, "test", "-L", path)
	if err == nil {
		return true, nil
	}
	var ce *runner.CmdError
	if errors.As(err, &ce) && ce.ExitCode == 1 {
		return false, nil
	}
	return false, err
}
