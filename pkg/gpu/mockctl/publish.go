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

package mockctl

// Publishing an override document is shared by nvml-mock-ctl and the allocation
// watcher. Both do a read-modify-write of the same file, so they must take the
// same lock: two independent lock implementations on one path drift apart the
// moment either changes.

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// WriteAtomic writes the doc via a temp file + rename in the same directory so
// readers (and the bind-mounted view in consumer containers) never observe a
// partial file.
func WriteAtomic(path string, doc *Doc) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := doc.Bytes()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".overrides-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// os.CreateTemp makes the file 0600, but the published config override is
	// bind-mounted into consumer containers and read by the mock library,
	// which may run as a non-root UID. Make it world-readable (matching how
	// config.yaml is consumed) so those reads don't silently fail.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// LockOverride takes an exclusive flock on a sibling .lock file so concurrent
// kubectl exec invocations serialize their read-modify-write.
func LockOverride(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lf.Fd()), unix.LOCK_EX); err != nil {
		_ = lf.Close()
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(lf.Fd()), unix.LOCK_UN)
		_ = lf.Close()
	}, nil
}

// ResetOverrides removes the override document so simulated state falls back to
// the pristine profile. It takes the same lock as WriteAtomic's callers because
// the allocation watcher publishes on its own interval and would otherwise
// re-create the file mid-removal. A missing file is already the desired state.
func ResetOverrides(path string) error {
	unlock, err := LockOverride(path)
	if err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer unlock()

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
