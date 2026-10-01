// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The `nvidia-smi -L` MIG listing, which is the one text surface this package
// parses on purpose.
//
// `nvidia-smi -q -x` describes MIG in <mig_mode> and <mig_devices>, and the
// readings on Snapshot and GPU are where partition counts, per-GPU attribution
// and the mode itself come from. But that block carries no MIG UUID and no
// profile name: nvidia-smi emits neither anywhere in the document. A consumer
// addresses a partition by its UUID and picks one by profile name, so both are
// worth asserting, and `-L` is the only place either is printed.

// MigDevice is one MIG partition as `nvidia-smi -L` lists it.
//
// Index is the partition's position under its parent, which is what
// `nvidia-smi -i <gpu>` addressing uses; it restarts at 0 for each GPU and so
// is only meaningful together with GPU.
type MigDevice struct {
	GPU     int
	Index   int
	Profile string
	UUID    string
}

// `nvidia-smi -L` pads the profile column to align the listing, so the fields
// are matched rather than split.
var (
	gpuHeaderLine = regexp.MustCompile(`^GPU (\d+):`)
	migDeviceLine = regexp.MustCompile(`^\s+MIG\s+(\S+)\s+Device\s+(\d+):\s*\(UUID:\s*([^)]+)\)`)
)

// ListMigDevices parses the MIG partitions out of `nvidia-smi -L`.
//
// This is the listing a MIG consumer actually reads, and driving the real
// nvidia-smi binary through it is what makes it worth asserting on: it exercises
// the mock's MIG surface the way the tool does, rather than re-reading the
// layout the mock was configured with.
//
// Profile and UUID are what only this listing answers. A caller after a
// partition count reads <mig_devices> from `nvidia-smi -q -x` instead, where
// the count comes with the MIG mode that explains it.
func ListMigDevices(out string) []MigDevice {
	var (
		devices []MigDevice
		gpu     = -1
	)
	for line := range strings.Lines(out) {
		if m := gpuHeaderLine.FindStringSubmatch(line); m != nil {
			gpu, _ = strconv.Atoi(m[1])
			continue
		}
		m := migDeviceLine.FindStringSubmatch(line)
		if m == nil || gpu < 0 {
			continue
		}
		index, _ := strconv.Atoi(m[2])
		devices = append(devices, MigDevice{
			GPU:     gpu,
			Index:   index,
			Profile: m[1],
			UUID:    m[3],
		})
	}
	return devices
}

// MigProfiles is the sorted, deduplicated set of profiles among devices. The
// device plugin's migStrategy=single requires this to hold exactly one entry,
// so a caller asserts uniformity by asserting on its length.
func MigProfiles(devices []MigDevice) []string {
	profiles := make([]string, 0, len(devices))
	for _, d := range devices {
		if !slices.Contains(profiles, d.Profile) {
			profiles = append(profiles, d.Profile)
		}
	}
	slices.Sort(profiles)
	return profiles
}
