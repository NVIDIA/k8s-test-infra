// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package engine

// Where the writes NVML setters make are recorded.
//
// On real hardware a cap or a profile request is driver state: set it in one
// process and every other process on the node sees it. Each consumer loads its
// own copy of the mock, so state kept on ConfigurableDevice was private to the
// writer and a read-after-write across two processes reported the old value.
// The override document is the only state those processes share, so that is
// where the writes go.
//
// The engine declares the port and the bridge installs the implementation,
// rather than calling the writer directly, because the writer lives in mockctl
// and mockctl imports this package.

import "sync/atomic"

// OverrideWriter records setter writes in the shared override document.
type OverrideWriter interface {
	// SetPowerLimit records a power cap, in milliwatts, for one device.
	SetPowerLimit(index int, milliwatts uint32) error

	// UpdateWorkloadProfiles replaces a device's requested profile list with
	// the one apply computes from the list already recorded.
	//
	// The caller passes apply rather than a finished list because the NVML
	// operations are read-modify-write, and the base has to be read under the
	// same lock as the write or concurrent callers lose an update. present
	// separates "nothing has been written" from "written, then cleared": only
	// the first may fall back to the profile's configured request.
	UpdateWorkloadProfiles(index int, apply func(base []uint32, present bool) ([]uint32, error)) error
}

// overrideWriterRef is atomic because it is installed once at load time but
// read by every setter call, which NVML makes from arbitrary consumer threads.
var overrideWriterRef atomic.Pointer[OverrideWriter]

// SetOverrideWriter installs the writer the setters persist through. A nil
// writer uninstalls, which is what a test restoring the previous one passes.
func SetOverrideWriter(w OverrideWriter) {
	if w == nil {
		overrideWriterRef.Store(nil)
		return
	}
	overrideWriterRef.Store(&w)
}

// overrideWriter returns the installed writer, or nil when a consumer links the
// engine without the bridge that installs one.
func overrideWriter() OverrideWriter {
	if w := overrideWriterRef.Load(); w != nil {
		return *w
	}
	return nil
}
