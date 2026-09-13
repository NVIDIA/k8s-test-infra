// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kernellog

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// DefaultPath is the kernel log an injected Xid is announced on.
//
// A real driver raises an Xid with a printk, and that kernel line — not NVML —
// is what the health agents Mokka simulates for consume: some read /dev/kmsg
// directly, and journald ingests it as a _TRANSPORT=kernel entry, which is the
// only transport NVSentinel's syslog monitor admits. An Xid that lands only in
// the NVML event path is invisible to both.
const DefaultPath = "/dev/kmsg"

// xidRecord renders the kernel log record announcing Xid code on the GPU at
// busID (an NVML bus ID such as 0000:1A:00.0).
//
// None of the three deviations from the NVML spelling is cosmetic. The driver
// names the PCI device without its function and prints hex in lower case, so a
// real Xid reads "(PCI:0000:1a:00)" — the form agents normalize against, and
// the one that matches the addresses in the simulated /sys/bus/pci tree. The
// "kernel: " prefix is what keeps "NVRM: " inside the journal's MESSAGE field:
// journald parses a leading "ident: " out of every /dev/kmsg line, so without
// it the record arrives as SYSLOG_IDENTIFIER=NVRM with the prefix stripped and
// patterns anchored on "NVRM: Xid" no longer match.
func xidRecord(busID string, code uint64) string {
	return fmt.Sprintf("kernel: NVRM: Xid (PCI:%s): %d", driverPCI(busID), code)
}

// emitXid announces Xid code on the kernel log at path for the GPU at busID,
// and reports whether it wrote. One record per call, because /dev/kmsg turns
// each write into exactly one kernel message.
//
// A missing kernel log is not an error. /dev/kmsg is absent from an
// unprivileged container and from any host that does not expose it, and the
// NVML side of the injection stands on its own — so a node that cannot be
// written to degrades to the NVML-only behaviour rather than failing the
// injection.
func emitXid(path, busID string, code uint64) (bool, error) {
	if path == "" || busID == "" {
		return false, nil
	}

	// Never O_CREATE: on a node without a kernel log a created regular file
	// would swallow the record and report success.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	if _, err := f.WriteString(xidRecord(busID, code) + "\n"); err != nil {
		return false, err
	}

	return true, nil
}

// driverPCI reduces an NVML bus ID to the lower-case domain:bus:device form the
// driver prints, dropping the function suffix.
func driverPCI(busID string) string {
	if before, _, ok := strings.Cut(busID, "."); ok {
		busID = before
	}

	return strings.ToLower(busID)
}
