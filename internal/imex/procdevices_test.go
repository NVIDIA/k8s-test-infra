// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// draParserOracle is a verbatim copy of the regex the NVIDIA DRA driver uses to
// read a device major out of /proc/devices
// (kubernetes-sigs/dra-driver-nvidia-gpu internal/common/nvcaps.go, getDeviceMajor).
//
// Using the consumer's own parser as the oracle is the point: it derives the
// expected result by a different path from the implementation, so these tests
// fail if the rendered file drifts out of the contract the DRA driver actually
// enforces. Asserting only "the string contains nvidia-caps-imex-channels"
// would pass against output the real parser rejects.
func draParserOracle(t *testing.T, content, name string) (int, bool) {
	t.Helper()

	re := regexp.MustCompile(
		"(?s)Character devices:.*?" +
			"([0-9]+) " + regexp.QuoteMeta(name) +
			"\n.*Block devices:",
	)
	matches := re.FindStringSubmatch(content)
	if len(matches) != 2 {
		return 0, false
	}
	major, err := strconv.Atoi(matches[1])
	require.NoError(t, err)
	return major, true
}

// realisticProcDevices mirrors the shape of a real /proc/devices on a node with
// no NVIDIA kernel driver loaded, which is exactly the Mokka case.
const realisticProcDevices = `Character devices:
  1 mem
  4 tty
  5 /dev/tty
 10 misc
 13 input
128 ptm
136 pts

Block devices:
  7 loop
  8 sd
259 blkext
`

func TestProcDevicesOutputSatisfiesTheDRAParser(t *testing.T) {
	t.Parallel()

	out, err := ProcDevices(realisticProcDevices, 235, 236)
	require.NoError(t, err)

	major, ok := draParserOracle(t, out, imexChannelsDeviceName)
	require.True(t, ok, "the DRA driver's own regex must find the IMEX channels entry")
	require.Equal(t, 235, major)

	capsMajor, ok := draParserOracle(t, out, capsDeviceName)
	require.True(t, ok, "the DRA driver's own regex must also find the nvidia-caps entry")
	require.Equal(t, 236, capsMajor)
}

// The failure this reproduces is the one observed on the POC cluster:
// "error parsing '/proc/devices': unexpected regex match: []".
func TestUnmodifiedProcDevicesFailsTheDRAParser(t *testing.T) {
	t.Parallel()

	_, ok := draParserOracle(t, realisticProcDevices, imexChannelsDeviceName)

	require.False(t, ok,
		"an unmodified /proc/devices must NOT satisfy the parser, otherwise this test proves nothing")
}

// The parser requires the entry to sit between the two section headers, so the
// entry must land in the character-devices section and not be appended at the end.
func TestEntriesLandBeforeTheBlockDevicesSection(t *testing.T) {
	t.Parallel()

	out, err := ProcDevices(realisticProcDevices, 235, 236)
	require.NoError(t, err)

	imexAt := strings.Index(out, imexChannelsDeviceName)
	blockAt := strings.Index(out, "Block devices:")
	require.NotEqual(t, -1, imexAt)
	require.NotEqual(t, -1, blockAt)

	require.Less(t, imexAt, blockAt,
		"the IMEX entry must precede the Block devices header or the parser will not match it")
}

func TestProcDevicesPreservesExistingEntries(t *testing.T) {
	t.Parallel()

	out, err := ProcDevices(realisticProcDevices, 235, 236)
	require.NoError(t, err)

	for _, keep := range []string{"1 mem", "4 tty", "136 pts", "7 loop", "259 blkext"} {
		require.Contains(t, out, keep, "existing entry %q must survive rendering", keep)
	}
}

// setup.sh may re-run on DaemonSet restart, so rendering twice must not produce
// a duplicate entry, which would make the parser's ".*?" match ambiguous.
func TestProcDevicesIsIdempotent(t *testing.T) {
	t.Parallel()

	once, err := ProcDevices(realisticProcDevices, 235, 236)
	require.NoError(t, err)
	twice, err := ProcDevices(once, 235, 236)
	require.NoError(t, err)

	require.Equal(t, once, twice, "rendering an already-rendered file must be a no-op")
	require.Equal(t, 1, strings.Count(twice, imexChannelsDeviceName),
		"the IMEX entry must appear exactly once")
}

// Re-rendering with a different major must update rather than append, otherwise
// the file would carry two conflicting majors.
func TestProcDevicesReplacesAnExistingEntryWithADifferentMajor(t *testing.T) {
	t.Parallel()

	first, err := ProcDevices(realisticProcDevices, 235, 236)
	require.NoError(t, err)
	second, err := ProcDevices(first, 240, 241)
	require.NoError(t, err)

	major, ok := draParserOracle(t, second, imexChannelsDeviceName)
	require.True(t, ok)
	require.Equal(t, 240, major, "the major must be updated, not duplicated")
	require.Equal(t, 1, strings.Count(second, imexChannelsDeviceName))
}

func TestProcDevicesRejectsInputWithoutBlockDevicesSection(t *testing.T) {
	t.Parallel()

	_, err := ProcDevices("Character devices:\n  1 mem\n", 235, 236)

	require.Error(t, err, "without a Block devices section the DRA parser can never match")
	require.Contains(t, err.Error(), "Block devices")
}

func TestProcDevicesRejectsInputWithoutCharacterDevicesSection(t *testing.T) {
	t.Parallel()

	_, err := ProcDevices("Block devices:\n  7 loop\n", 235, 236)

	require.Error(t, err)
	require.Contains(t, err.Error(), "Character devices")
}

func TestProcDevicesRejectsOutOfRangeMajors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		imex, caps int
	}{
		{"imex zero", 0, 236},
		{"imex negative", -1, 236},
		{"caps zero", 235, 0},
		{"imex too large", 1 << 20, 236},
		{"imex equals caps", 235, 235},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ProcDevices(realisticProcDevices, tc.imex, tc.caps)
			require.Error(t, err, "major pair (%d,%d) must be rejected", tc.imex, tc.caps)
		})
	}
}

// A major already claimed by another driver would make the mock entry collide
// with a real device, so it must be refused rather than silently written.
func TestProcDevicesRejectsAMajorAlreadyInUse(t *testing.T) {
	t.Parallel()

	_, err := ProcDevices(realisticProcDevices, 10, 236)

	require.Error(t, err, "major 10 is already 'misc' in the fixture and must be refused")
	require.Contains(t, err.Error(), "10")
}
