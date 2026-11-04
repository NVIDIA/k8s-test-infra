// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package host

import "runtime"

func runOnDisposableThread(run func() error) error {
	result := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		// Do not unlock: Go destroys this thread on goroutine exit. Its namespaces
		// must never be reused for unrelated agent work.
		result <- run()
	}()
	return <-result
}
