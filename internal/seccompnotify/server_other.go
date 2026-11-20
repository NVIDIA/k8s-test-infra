// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package seccompnotify

import (
	"context"
	"errors"
)

// Linux-only; this file keeps the package building elsewhere.

// Request is one notified syscall.
type Request struct {
	Container string
	Pid       uint32
	Args      [6]uint64
}

var errUnsupported = errors.New("seccompnotify: only supported on linux")

// Run fails: there is nothing to serve outside Linux.
func (s *Server) Run(_ context.Context) error { return errUnsupported }

// String fails: there is no caller outside Linux.
func (r Request) String(_ uint64, _ int) (string, error) { return "", errUnsupported }
