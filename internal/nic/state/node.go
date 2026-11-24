// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package state

import (
	"encoding/hex"
	"fmt"

	"github.com/NVIDIA/k8s-test-infra/internal/identity"
)

// Node is where a Node sits, the first part of every NIC identifier: its
// coordinates in the control plane, or its name in file mode.
type Node struct {
	inventoryUID         string
	rackGroup            string
	rackIndex, nodeIndex int32
	name                 string
}

// FileNode is a file-mode Node, known by its name.
func FileNode(name string) Node { return Node{name: name} }

// RackNode is a control-plane Node: its inventory, rack group, rack and Node
// index, as GPU UUIDs use them.
func RackNode(inventoryUID, rackGroup string, rackIndex, nodeIndex int32) Node {
	return Node{inventoryUID: inventoryUID, rackGroup: rackGroup, rackIndex: rackIndex, nodeIndex: nodeIndex}
}

// String names the Node for messages.
func (n Node) String() string {
	if n.inventoryUID == "" {
		return n.name
	}
	return fmt.Sprintf("%s/%s/%d/%d", n.inventoryUID, n.rackGroup, n.rackIndex, n.nodeIndex)
}

// The identifier kinds. Each kind is its own domain, so a GUID and a MAC of
// the same path are unrelated.
const (
	kindGUID = "nic-guid"
	kindMAC  = "nic-mac"
	kindID   = "nic-id"
)

// GUID is the 64-bit identifier of the NIC object at path on this Node, in
// the aaaa:bbbb:cccc:dddd form RDMA sysfs shows.
//
// Path names where the object sits below the Node: its slot, then what it is
// within the slot, or a board and what it is on the board. The same Node and
// path always give the same identifier, and nothing else on the Node changes
// it, so removing one NIC leaves the others' identifiers as they were.
func (n Node) GUID(path ...string) string {
	id := n.sum(kindGUID, path)
	h := hex.EncodeToString(id[:8])
	return h[0:4] + ":" + h[4:8] + ":" + h[8:12] + ":" + h[12:16]
}

// MAC is the locally administered unicast MAC of the NIC object at path on
// this Node. See GUID for path.
func (n Node) MAC(path ...string) string {
	id := n.sum(kindMAC, path)
	b := id[:6]
	b[0] = b[0]&0xfc | 0x02
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", b[0], b[1], b[2], b[3], b[4], b[5])
}

// ID is a 128-bit identifier of the NIC object at path on this Node, as 32
// hex digits, for serial numbers, VPD UUIDs and switch IDs. See GUID for
// path.
func (n Node) ID(path ...string) string {
	id := n.sum(kindID, path)
	return hex.EncodeToString(id[:])
}

func (n Node) sum(kind string, path []string) [16]byte {
	name := identity.New(kind)
	if n.inventoryUID == "" {
		// A file-mode Node has no inventory, and no control-plane Node has an
		// empty inventory UID, so the two modes never share a name.
		name = name.String("").String(n.name)
	} else {
		name = name.String(n.inventoryUID).String(n.rackGroup).Int(n.rackIndex, n.nodeIndex)
	}
	for _, p := range path {
		name = name.String(p)
	}
	return name.Sum()
}
