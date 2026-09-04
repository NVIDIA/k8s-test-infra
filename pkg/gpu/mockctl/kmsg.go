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

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// DefaultKernelLog is the kernel log an injected Xid is announced on.
//
// A real driver raises an Xid with a printk, and that kernel line — not NVML —
// is what the health agents Mokka simulates for consume: some read /dev/kmsg
// directly, and journald ingests it as a _TRANSPORT=kernel entry, which is the
// only transport NVSentinel's syslog monitor admits. An Xid that lands only in
// the NVML event path is invisible to both.
const DefaultKernelLog = "/dev/kmsg"

// XidRecord renders the kernel log record announcing Xid code on the GPU at
// busID (an NVML bus ID such as 0000:1A:00.0).
//
// Neither of the two deviations from the NVML spelling is cosmetic. The driver
// names the PCI device without its function, so a real Xid reads
// "(PCI:0000:1A:00)", which is the form agents normalize against. And the
// "kernel: " prefix is what keeps "NVRM: " inside the journal's MESSAGE field:
// journald parses a leading "ident: " out of every /dev/kmsg line, so without
// it the record arrives as SYSLOG_IDENTIFIER=NVRM with the prefix stripped and
// patterns anchored on "NVRM: Xid" no longer match.
func XidRecord(busID string, code uint64) string {
	return fmt.Sprintf("kernel: NVRM: Xid (PCI:%s): %d", driverPCI(busID), code)
}

// EmitXid announces Xid code on the kernel log at path, one record per bus ID,
// and reports whether it wrote.
//
// A missing kernel log is not an error. /dev/kmsg is absent from an
// unprivileged container and from any host that does not expose it, and the
// NVML side of the injection stands on its own — so a node that cannot be
// written to degrades to the NVML-only behaviour rather than failing the
// injection.
func EmitXid(path string, busIDs []string, code uint64) (bool, error) {
	records := make([]string, 0, len(busIDs))

	for _, busID := range busIDs {
		if busID == "" {
			continue
		}
		records = append(records, XidRecord(busID, code)+"\n")
	}

	if path == "" || len(records) == 0 {
		return false, nil
	}

	// Never O_CREATE: on a node without a kernel log a created regular file
	// would swallow the records and report success.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	// One write per record: /dev/kmsg turns each write into exactly one kernel
	// message, so a batched write would arrive as a single mangled line.
	for _, record := range records {
		if _, err := f.WriteString(record); err != nil {
			return false, err
		}
	}

	return true, nil
}

// driverPCI reduces an NVML bus ID to the domain:bus:device form the driver
// prints, dropping the function suffix.
func driverPCI(busID string) string {
	if before, _, ok := strings.Cut(busID, "."); ok {
		return before
	}

	return busID
}
