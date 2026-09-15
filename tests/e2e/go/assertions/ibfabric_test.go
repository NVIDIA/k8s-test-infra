// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A two-node point-to-point scan: the local CA reports its own sysimgguid /
// caguid / port GUID, and the remote link record names the peer's.
const ibnetdiscoverTwoNode = `
#
# Topology file: generated on Tue Sep 15 12:00:00 2026
#
# Initiated from node a088c20300ab0000 port a088c20300ab0001

vendid=0x02c9
devid=0x1017
sysimgguid=0xa088c20300ab0000
caguid=0xa088c20300ab0000
Ca	1 "H-a088c20300ab0000"		# "node-a mlx5_0"
[1](0xa088c20300ab0001) 	"H-0xa088c20300bb0000"[1](0xa088c20300bb0001)		# lid 1 lmc 0 "node-b mlx5_0" lid 2 4xEDR
`

func TestGUIDSetCollectsEveryPrefixedGUIDNormalized(t *testing.T) {
	t.Parallel()

	got := guidSet(ibnetdiscoverTwoNode)

	require.Equal(t, map[string]struct{}{
		"a088c20300ab0000": {},
		"a088c20300ab0001": {},
		"a088c20300bb0000": {},
		"a088c20300bb0001": {},
	}, got)
}

func TestGUIDSetIsEmptyForOutputWithoutGUIDs(t *testing.T) {
	t.Parallel()

	require.Empty(t, guidSet("ibwarn: [42] mad_rpc: _do_madrpc failed\n"))
}

func TestFirstNonLocalGUIDReturnsThePeerGUID(t *testing.T) {
	t.Parallel()

	local := guidSet("0xa088c20300ab0000 0xa088c20300ab0001")

	got := firstNonLocalGUID(guidSet(ibnetdiscoverTwoNode), local)

	require.Equal(t, "a088c20300bb0000", got)
}

// The local CA's node_guid and sys_image_guid are NOT its port_guid: the mock
// derives the node GUID by clearing the EUI-64 U/L bit. A local set built from
// port GUIDs alone therefore lets the local node's own GUID pass as a peer and
// false-passes the cross-pod assertion, which is why the sysfs read collects
// all three.
func TestFirstNonLocalGUIDIsEmptyWhenTheScanSawOnlyLocalGUIDs(t *testing.T) {
	t.Parallel()

	found := guidSet("caguid=0xa088c20300ab0000\n[1](0xa088c20300ab0001)")
	local := guidSet("0xa088c20300ab0001 0xa088c20300ab0000")

	require.Empty(t, firstNonLocalGUID(found, local))
}

func TestFirstNonLocalGUIDPicksTheLowestSoFailuresReproduce(t *testing.T) {
	t.Parallel()

	found := guidSet("0xa088c20300cc0000 0xa088c20300bb0000")

	require.Equal(t, "a088c20300bb0000", firstNonLocalGUID(found, nil))
}

func TestIBNetDiscoverHasCARecord(t *testing.T) {
	t.Parallel()

	require.True(t, ibNetDiscoverHasCARecord(ibnetdiscoverTwoNode))
	require.False(t, ibNetDiscoverHasCARecord("vendid=0x02c9\nsysimgguid=0xa088c20300ab0000\n"),
		"a scan that resolved no HCA must not pass on sysimgguid alone")
}

func TestSMInfoMasterGUIDParsesTheElectedMaster(t *testing.T) {
	t.Parallel()

	out := "sminfo: sm lid 1 sm guid 0xa088c20300ab0001, activity count 0 priority 0 state 3 SMINFO_MASTER\n"

	require.True(t, sminfoReportsMaster(out))
	require.Equal(t, "a088c20300ab0001", sminfoMasterGUID(out))
}

// sminfo does not zero-pad the SM GUID, so the parser cannot require 16 digits.
func TestSMInfoMasterGUIDAcceptsAnUnpaddedGUID(t *testing.T) {
	t.Parallel()

	require.Equal(t, "2c9", sminfoMasterGUID("sminfo: sm lid 1 sm guid 0x2c9, state 3 SMINFO_MASTER\n"))
}

// An all-zero SM GUID is what an unanswered SubnGet leaves behind, so it must
// not read as a believable master.
func TestSMInfoMasterGUIDRejectsAnAllZeroGUID(t *testing.T) {
	t.Parallel()

	require.Empty(t, sminfoMasterGUID("sminfo: sm lid 1 sm guid 0x0000000000000000, state 3 SMINFO_MASTER\n"))
}

func TestSMInfoReportsMasterRejectsANonMasterState(t *testing.T) {
	t.Parallel()

	require.False(t, sminfoReportsMaster(
		"sminfo: sm lid 1 sm guid 0xa088c20300ab0001, activity count 0 priority 0 state 2 SMINFO_STANDBY\n"))
}

func TestFirstForbiddenNamesTheOffendingPattern(t *testing.T) {
	t.Parallel()

	require.Equal(t, "mad_rpc",
		firstForbidden("ibwarn: [7] mad_rpc: _do_madrpc failed\n", ibNetDiscoverForbidden))
	require.Empty(t, firstForbidden(ibnetdiscoverTwoNode, ibNetDiscoverForbidden))
}

func TestIBNetDiscoverPeerAcceptsATwoNodeScan(t *testing.T) {
	t.Parallel()

	guid, why := ibNetDiscoverPeer(ibnetdiscoverTwoNode, guidSet("0xa088c20300ab0000 0xa088c20300ab0001"))

	require.Empty(t, why)
	require.Equal(t, "a088c20300bb0000", guid)
}

// The MAD layer giving up is not an exit code — libibmad tools exit zero after
// it — so a scan that printed a plausible topology alongside a MAD error must
// still be rejected.
func TestIBNetDiscoverPeerRejectsAMADErrorEvenWithATopology(t *testing.T) {
	t.Parallel()

	_, why := ibNetDiscoverPeer("iberror: failed to query\n"+ibnetdiscoverTwoNode, nil)

	require.Equal(t, "ibnetdiscover output contains forbidden pattern iberror:", why)
}

func TestIBNetDiscoverPeerRejectsAScanThatFoundOnlyItself(t *testing.T) {
	t.Parallel()

	local := guidSet("0xa088c20300ab0000 0xa088c20300ab0001 0xa088c20300bb0000 0xa088c20300bb0001")

	_, why := ibNetDiscoverPeer(ibnetdiscoverTwoNode, local)

	require.Equal(t, "ibnetdiscover found only local GUIDs, no cross-pod peer", why)
}

func TestSMInfoMasterAcceptsAnElectedMaster(t *testing.T) {
	t.Parallel()

	guid, why := sminfoMaster(
		"sminfo: sm lid 1 sm guid 0xa088c20300ab0001, activity count 0 priority 0 state 3 SMINFO_MASTER\n")

	require.Empty(t, why)
	require.Equal(t, "a088c20300ab0001", guid)
}

func TestSMInfoMasterRejectsAnUnansweredQuery(t *testing.T) {
	t.Parallel()

	_, why := sminfoMaster("sminfo: iberror: failed to query SMInfo\n")

	require.Equal(t, "sminfo output contains forbidden pattern iberror:", why)
}
