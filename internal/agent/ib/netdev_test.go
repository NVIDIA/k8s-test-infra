// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package ib

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The PCI renderer's net/<ifname> entry and the kernel link have to carry the
// same name, or a consumer resolves an interface that does not exist and skips
// the HCA. Both derive from the prefix and count, so this pins the derivation
// the two sides share.
func TestNetdevNames_MatchTheRenderedAssociation(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"mockib0", "mockib1"}, netdevNames("mockib", 2))
}

// A profile with no HCAs asks for no interfaces, which has to be nothing to do
// rather than one unnamed link.
func TestNetdevNames_EmptyWithoutHCAs(t *testing.T) {
	t.Parallel()

	require.Empty(t, netdevNames("mockib", 0))
	require.NoError(t, ensureNetdevs("/proc/1/ns/net", "mockib", 0))
	require.NoError(t, removeNetdevs("/proc/1/ns/net", "mockib", 0))
}
