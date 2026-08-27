// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package imex generates the mock IMEX capability surface that the NVIDIA DRA
// driver's compute-domain kubelet plugin needs on a node with no NVIDIA kernel
// driver.
//
// The plugin reads a device major for "nvidia-caps-imex-channels" out of
// /proc/devices at startup. On a Mokka node that entry does not exist, because
// there is no kernel module, and the plugin aborts with:
//
//	error getting nvcap for IMEX channel '0': error getting device major:
//	error parsing '/proc/devices': unexpected regex match: []
//
// The DRA driver supports pointing at a substitute file through the
// ALT_PROC_DEVICES_PATH environment variable, wired by its chart's
// altProcDevices value. This package renders that file.
//
// The output must satisfy the consumer's parser, which requires the entry to
// appear between the "Character devices:" and "Block devices:" headers and to be
// newline-terminated. See internal/common/nvcaps.go in
// kubernetes-sigs/dra-driver-nvidia-gpu.
package imex

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Device names the DRA driver looks up.
const (
	imexChannelsDeviceName = "nvidia-caps-imex-channels"
	capsDeviceName         = "nvidia-caps"
)

const (
	charDevicesHeader  = "Character devices:"
	blockDevicesHeader = "Block devices:"

	// maxDeviceMajor is the largest major this renderer will emit. Linux
	// allocates majors well below this; keeping the bound tight makes a
	// transposed or garbage value fail loudly rather than produce a file the
	// consumer will parse into nonsense.
	maxDeviceMajor = 4095
)

// entryPattern matches an existing "<major> <name>" line so re-rendering can
// replace it instead of appending a duplicate.
func entryPattern(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^[ \t]*[0-9]+ ` + regexp.QuoteMeta(name) + `[ \t]*$` + "\n?")
}

// existingMajors returns every device major already listed in src.
func existingMajors(src string) map[int]string {
	majors := make(map[int]string)
	re := regexp.MustCompile(`(?m)^[ \t]*([0-9]+) +(\S+)[ \t]*$`)
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		major, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		// Keep the first binding; a major can legitimately appear in both the
		// character and block sections for different drivers.
		if _, seen := majors[major]; !seen {
			majors[major] = m[2]
		}
	}
	return majors
}

// ProcDevices returns src with NVIDIA caps character-device entries inserted
// into the character-devices section, so the DRA driver's compute-domain plugin
// can resolve its majors.
//
// It is idempotent: rendering an already-rendered file is a no-op, and
// re-rendering with a different major replaces the entry rather than appending
// a second one.
func ProcDevices(src string, imexMajor, capsMajor int) (string, error) {
	if err := validateMajors(src, imexMajor, capsMajor); err != nil {
		return "", err
	}

	charAt := strings.Index(src, charDevicesHeader)
	if charAt < 0 {
		return "", fmt.Errorf("input has no %q header, so the consumer's parser can never match", charDevicesHeader)
	}
	blockAt := strings.Index(src, blockDevicesHeader)
	if blockAt < 0 {
		return "", fmt.Errorf("input has no %q header, so the consumer's parser can never match", blockDevicesHeader)
	}
	if blockAt < charAt {
		return "", fmt.Errorf("%q appears before %q, which is not a valid /proc/devices layout",
			blockDevicesHeader, charDevicesHeader)
	}

	// Drop any previous rendering so a changed major replaces rather than
	// duplicates. Only the character-devices section is rewritten.
	charSection := src[:blockAt]
	rest := src[blockAt:]
	for _, name := range []string{imexChannelsDeviceName, capsDeviceName} {
		charSection = entryPattern(name).ReplaceAllString(charSection, "")
	}

	// Trim trailing blank lines from the character section, append the entries,
	// then restore the blank-line separator before the block header.
	charSection = strings.TrimRight(charSection, "\n \t")
	entries := fmt.Sprintf("\n%3d %s\n%3d %s\n\n", imexMajor, imexChannelsDeviceName, capsMajor, capsDeviceName)

	return charSection + entries + rest, nil
}

func validateMajors(src string, imexMajor, capsMajor int) error {
	for _, m := range []struct {
		name  string
		major int
	}{
		{imexChannelsDeviceName, imexMajor},
		{capsDeviceName, capsMajor},
	} {
		if m.major <= 0 || m.major > maxDeviceMajor {
			return fmt.Errorf("device major %d for %s is out of range, want 1..%d",
				m.major, m.name, maxDeviceMajor)
		}
	}

	if imexMajor == capsMajor {
		return fmt.Errorf("device majors for %s and %s must differ, both are %d",
			imexChannelsDeviceName, capsDeviceName, imexMajor)
	}

	inUse := existingMajors(src)
	for _, m := range []struct {
		name  string
		major int
	}{
		{imexChannelsDeviceName, imexMajor},
		{capsDeviceName, capsMajor},
	} {
		if owner, taken := inUse[m.major]; taken && owner != m.name {
			return fmt.Errorf("device major %d is already assigned to %q, refusing to reuse it for %s",
				m.major, owner, m.name)
		}
	}

	return nil
}
