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

// Resetting one device's overrides as a single operation, for callers that have
// no command dispatcher to hang it off. nvml-mock-ctl reaches Doc.Reset through
// its generic load-mutate-validate-write flow; the mock NVML library serves
// `nvidia-smi --gpu-reset` from a CGo callback with no such flow around it, and
// needs the whole read-modify-write in one call.

import (
	"strconv"
)

// ResetDevice returns one device to its healthy baseline by removing its
// override bucket, taking the same flock as every other writer of this file so a
// concurrent injection cannot interleave with the reset.
//
// The index is deliberately not a Target: a Target can say "all", and the shared
// `all:` bucket is not resettable per device. Doc.Reset leaves that bucket alone
// for a device target (so state injected with `--gpu all` survives, and needs
// `Doc.Reset(Target{All: true})` to clear), and a caller resetting a single GPU
// must not be able to ask for something this cannot honour.
//
// A device with nothing injected is reset without touching the file at all — no
// write and no lock. That is not just an optimisation: resetting a healthy device
// is the common case, and skipping the write keeps it working where the overrides
// are not writable. Only a reset that genuinely has state to clear can fail.
func ResetDevice(path string, index int) error {
	if path == "" {
		// No config, so no injected state to clear: the device is already at its
		// baseline and the reset has nothing to do.
		return nil
	}
	if has, err := deviceHasOverrides(path, index); err != nil || !has {
		return err
	}

	unlock, err := LockOverride(path)
	if err != nil {
		return err
	}
	defer unlock()

	// Re-read under the lock: the peek above raced with any concurrent writer.
	doc, err := Load(path)
	if err != nil {
		return err
	}
	doc.Reset(Target{Index: index})
	return WriteAtomic(path, doc)
}

// deviceHasOverrides reports whether the document carries a bucket for the
// device. A missing or empty file is not an error: it means nothing was injected.
func deviceHasOverrides(path string, index int) (bool, error) {
	doc, err := Load(path)
	if err != nil {
		return false, err
	}
	_, ok := doc.Devices[strconv.Itoa(index)]
	return ok, nil
}
