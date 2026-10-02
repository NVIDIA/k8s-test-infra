// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package migcaps

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// parseMinors mirrors nvidia-container-toolkit's processMinorsFile and
// MigCap.isValid (internal/nvcaps/nvcaps.go), the parser that actually consumes
// this file. Reproducing it here makes the consumer's acceptance the test
// oracle, so a format change fails as a mismatch rather than as an
// unschedulable pod in e2e.
func parseMinors(t *testing.T, content string) map[string]int {
	t.Helper()

	caps := make(map[string]int)
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, " ")
		require.Len(t, parts, 2, "line %q must be exactly two space-separated fields", line)

		require.True(t, validCapName(parts[0]), "cap %q is one the consumer rejects", parts[0])

		minor, err := strconv.Atoi(parts[1])
		require.NoError(t, err, "minor in %q", line)
		caps[parts[0]] = minor
	}
	require.NoError(t, scanner.Err())
	return caps
}

// validCapName mirrors MigCap.isValid.
func validCapName(name string) bool {
	switch name {
	case "config", "monitor":
		return true
	}
	var gpu, gi, ci int
	if n, _ := fmt.Sscanf(name, "gpu%d/gi%d/ci%d/access", &gpu, &gi, &ci); n == 3 {
		return true
	}
	n, _ := fmt.Sscanf(name, "gpu%d/gi%d/access", &gpu, &gi)
	return n == 2
}

func TestMinors_ConsumerCanParseEveryLine(t *testing.T) {
	t.Parallel()

	content := Minors(Caps([]GPU{
		{Minor: 0, GPUInstances: []GPUInstance{
			{ID: 0, ComputeInstanceIDs: []uint32{0}},
			{ID: 1, ComputeInstanceIDs: []uint32{0, 1}},
		}},
		{Minor: 3, GPUInstances: []GPUInstance{
			{ID: 5, ComputeInstanceIDs: []uint32{0}},
		}},
	}))

	caps := parseMinors(t, content)

	require.Equal(t, map[string]int{
		"config":              1,
		"monitor":             2,
		"gpu0/gi0/access":     3,
		"gpu0/gi0/ci0/access": 4,
		"gpu0/gi1/access":     5,
		"gpu0/gi1/ci0/access": 6,
		"gpu0/gi1/ci1/access": 7,
		"gpu3/gi5/access":     8,
		"gpu3/gi5/ci0/access": 9,
	}, caps)
}

// TestCaps_KeyedByGPUMinorNotIndex pins a distinction that is invisible until a
// profile assigns non-default minors: the consumer names caps by the GPU's
// device-node minor, so gpu<N> must be that minor, not the enumeration index.
func TestCaps_KeyedByGPUMinorNotIndex(t *testing.T) {
	t.Parallel()

	caps := Caps([]GPU{
		{Minor: 7, GPUInstances: []GPUInstance{{ID: 0, ComputeInstanceIDs: []uint32{0}}}},
	})

	names := make([]string, 0, len(caps))
	for _, c := range caps {
		names = append(names, c.Name)
	}
	require.Equal(t, []string{"config", "monitor", "gpu7/gi0/access", "gpu7/gi0/ci0/access"}, names)
}

func TestCaps_NoPartitionsIsEmpty(t *testing.T) {
	t.Parallel()

	// With nothing partitioned there is no MIG surface at all — not even the
	// config and monitor caps, which only exist on a MIG-enabled driver.
	require.Empty(t, Caps(nil))
	require.Empty(t, Caps([]GPU{{Minor: 0}}))
	require.Empty(t, Minors(nil))
}

func TestMinors_IsNewlineTerminated(t *testing.T) {
	t.Parallel()

	content := Minors(Caps([]GPU{
		{Minor: 0, GPUInstances: []GPUInstance{{ID: 0, ComputeInstanceIDs: []uint32{0}}}},
	}))

	// bufio.Scanner tolerates a missing final newline, but every real procfs
	// file has one and a reader doing ReadString('\n') would not.
	require.True(t, strings.HasSuffix(content, "\n"))
	require.NotContains(t, content, "\n\n")
}

func TestCaps_MinorsAreUniqueAndDense(t *testing.T) {
	t.Parallel()

	gpus := make([]GPU, 0, 8)
	for gpu := range 8 {
		instances := make([]GPUInstance, 0, 7)
		for gi := range 7 {
			instances = append(instances, GPUInstance{ID: uint32(gi), ComputeInstanceIDs: []uint32{0}}) //nolint:gosec // bounded
		}
		gpus = append(gpus, GPU{Minor: gpu, GPUInstances: instances})
	}

	caps := Caps(gpus)

	// A duplicate minor would silently alias two partitions onto one cap
	// device, which is the failure this whole table exists to prevent.
	seen := make(map[int]string, len(caps))
	for _, c := range caps {
		require.NotContains(t, seen, c.Minor, "minor %d already used by %q", c.Minor, seen[c.Minor])
		seen[c.Minor] = c.Name
	}
	require.Len(t, caps, 2+8*7*2)
	for i, c := range caps {
		require.Equal(t, i+1, c.Minor, "minors run 1..N without gaps")
	}
}
