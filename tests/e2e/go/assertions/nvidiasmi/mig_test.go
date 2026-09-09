// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Captured from `nvidia-smi -L` on a MIG-enabled A100 node. The column padding
// is the driver's own and is reproduced verbatim, since it is exactly what a
// naive field split would trip over.
const migListOutput = `GPU 0: NVIDIA A100-SXM4-40GB (UUID: GPU-c4e0b2a1-1111-2222-3333-444455556666)
  MIG 1g.5gb      Device  0: (UUID: MIG-11110000-0000-0000-0000-000000000000)
  MIG 1g.5gb      Device  1: (UUID: MIG-11110000-0000-0000-0000-000000000001)
GPU 1: NVIDIA A100-SXM4-40GB (UUID: GPU-c4e0b2a1-1111-2222-3333-444455556667)
  MIG 1g.5gb      Device  0: (UUID: MIG-22220000-0000-0000-0000-000000000000)
`

func TestListMigDevices(t *testing.T) {
	t.Parallel()
	devices := ListMigDevices(migListOutput)

	require.Equal(t, []MigDevice{
		{GPU: 0, Index: 0, Profile: "1g.5gb", UUID: "MIG-11110000-0000-0000-0000-000000000000"},
		{GPU: 0, Index: 1, Profile: "1g.5gb", UUID: "MIG-11110000-0000-0000-0000-000000000001"},
		{GPU: 1, Index: 0, Profile: "1g.5gb", UUID: "MIG-22220000-0000-0000-0000-000000000000"},
	}, devices)
}

// A GPU with MIG off lists no partitions, and the parser must report that as
// empty rather than as an unattributed device: the MIG scenario's negative
// control asserts on exactly this.
func TestListMigDevices_NoPartitions(t *testing.T) {
	t.Parallel()
	const out = `GPU 0: NVIDIA A100-SXM4-40GB (UUID: GPU-c4e0b2a1-1111-2222-3333-444455556666)
GPU 1: NVIDIA A100-SXM4-40GB (UUID: GPU-c4e0b2a1-1111-2222-3333-444455556667)
`
	require.Empty(t, ListMigDevices(out))
}

// A pod allocated one MIG device sees only its own partition, and nvidia-smi
// still prints the parent GPU header above it. Attribution has to survive that,
// because the parent's index inside the pod is 0 regardless of which physical
// GPU it is.
func TestListMigDevices_SingleAllocatedPartition(t *testing.T) {
	t.Parallel()
	const out = `GPU 0: NVIDIA A100-SXM4-40GB (UUID: GPU-c4e0b2a1-1111-2222-3333-444455556666)
  MIG 1g.5gb      Device  0: (UUID: MIG-33330000-0000-0000-0000-000000000000)
`
	require.Equal(t, []MigDevice{
		{GPU: 0, Index: 0, Profile: "1g.5gb", UUID: "MIG-33330000-0000-0000-0000-000000000000"},
	}, ListMigDevices(out))
}

func TestMigProfiles(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"1g.5gb"}, MigProfiles(ListMigDevices(migListOutput)))

	mixed := ListMigDevices(`GPU 0: NVIDIA A100-SXM4-40GB (UUID: GPU-a)
  MIG 3g.20gb     Device  0: (UUID: MIG-a)
  MIG 1g.5gb      Device  1: (UUID: MIG-b)
`)
	require.Equal(t, []string{"1g.5gb", "3g.20gb"}, MigProfiles(mixed),
		"profiles are reported sorted and deduplicated so a caller can assert uniformity")
}
