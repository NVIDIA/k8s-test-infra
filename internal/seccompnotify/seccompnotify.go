// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package seccompnotify answers syscalls that a container's seccomp policy
// notifies (SCMP_ACT_NOTIFY): runc hands each container's notification fd to
// the listener path, and the caller waits for the handler's answer. Once the
// server stops, those syscalls fail with ENOSYS.
package seccompnotify

import (
	"syscall"
)

// Handler answers a request with the syscall's return value, or fails the
// syscall with a non-zero errno.
type Handler func(Request) (int64, syscall.Errno)

// Server accepts the notification fds runc hands to a listener path.
type Server struct {
	path    string
	handler Handler
}

// NewServer returns a Server that listens at path and answers with handler.
// Requests are answered one at a time.
func NewServer(path string, handler Handler) *Server {
	return &Server{path: path, handler: handler}
}
