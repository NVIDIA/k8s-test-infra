// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package engine

import (
	"crypto/sha1" // UUIDv5 is defined in terms of SHA-1; this is not a security boundary.
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"

	"sigs.k8s.io/yaml"
)

// IdentityDeriveNode makes every identity a profile hard-codes unique to the
// node: GPU UUIDs, board serials and, for a node a topology places in a rack,
// the chassis serial, slot and tray. Empty leaves the profile's values alone,
// which is what a cluster of one node, or one release per node, wants.
const IdentityDeriveNode = "node"

// EnvIdentityDerive selects the derivation, for the node agent and the engine
// alike: the agent stages the config workloads read, and the engine derives the
// same values for a process that reads the chart's profile directly, such as
// nvidia-smi run in the node pod.
const EnvIdentityDerive = "MOKKA_IDENTITY_DERIVE"

const (
	// nvl72ComputeTrays is how many compute trays a rack holds.
	nvl72ComputeTrays = 18

	// serialDigits is how many trailing digits of a serial are replaced. The
	// leading digits stay, so a derived serial still looks like the family of
	// board the profile models, while 10^9 values keep a cluster of thousands
	// of boards collision-free in practice.
	serialDigits = 9
)

// identityNamespace is a fixed UUIDv5 namespace for node-derived identities.
// Changing it renames every derived identity, so it only changes with a new
// scheme.
const identityNamespace = "\x6d\x6f\x6b\x6b\x61\x2d\x6e\x6f\x64\x65\x2d\x69\x64\x30\x30\x31"

// ApplyNodeIdentity rewrites the identities cfg hard-codes so that no two nodes
// running the same profile report the same one. It is a pure function of the
// node name, the profile and the topology, so every process that loads the
// profile, the node agent and the engine alike, derives the same values without
// coordinating, and a restart changes nothing.
//
// topo is optional. A node the topology places in a domain also gets the
// domain's chassis serial and a tray and slot from its position in the domain;
// without one those fields stay as the profile has them, because a chassis is
// shared by the nodes of a rack and nothing says which nodes those are.
func ApplyNodeIdentity(cfg *YAMLConfig, nodeName string, topo *TopologyDocument) error {
	if nodeName == "" {
		return errors.New("deriving node identity needs the node name (NODE_NAME)")
	}
	switch cfg.System.IdentityNode {
	case nodeName:
		return nil
	case "":
	default:
		return fmt.Errorf("config already carries the identities of node %q, and cannot be derived again for %q",
			cfg.System.IdentityNode, nodeName)
	}
	cfg.System.IdentityNode = nodeName

	for i := range cfg.Devices {
		dev := &cfg.Devices[i]
		if dev.UUID != "" {
			dev.UUID = derivedUUID(nodeName, dev.UUID)
		}
		if dev.Serial != "" {
			dev.Serial = derivedSerial(nodeName, dev.Serial)
		}
	}
	if cfg.DeviceDefaults.Serial != "" {
		cfg.DeviceDefaults.Serial = derivedSerial(nodeName, cfg.DeviceDefaults.Serial)
	}

	applyPlatformPosition(cfg, nodeName, topo)
	return nil
}

// applyPlatformPosition places the node in its rack: one chassis per topology
// domain, one tray per node of the domain.
func applyPlatformPosition(cfg *YAMLConfig, nodeName string, topo *TopologyDocument) {
	platform := cfg.DeviceDefaults.Platform
	if topo == nil || platform == nil {
		return
	}
	domain, position, ok := topo.PositionOf(nodeName)
	if !ok {
		return
	}
	if platform.ChassisSerialNumber != "" {
		platform.ChassisSerialNumber = derivedSerial(domain.UUID, platform.ChassisSerialNumber)
	}
	if platform.TrayIndex == 0 && platform.SlotNumber == 0 {
		return
	}
	if position > nvl72ComputeTrays {
		warnLog("[CONFIG] identity: node %s is node %d of domain %q, past the %d compute trays of a rack; keeping the profile's slot and tray\n",
			nodeName, position, domain.Name, nvl72ComputeTrays)
		return
	}
	// The slot keeps the offset the profile was captured with. Slots count the
	// switch trays that sit between compute trays, and that gap differs between
	// racks (the captured VR NVL72 tray 3 is in slot 4, the GB200 profile's tray
	// 11 in slot 21), so the offset is data rather than a layout this code could
	// get wrong. Slots stay unique and the node at the captured tray reports
	// exactly the captured slot; trays below the switch trays are off by the gap.
	offset := int(platform.SlotNumber) - int(platform.TrayIndex)
	platform.SlotNumber = uint8(max(position+offset, 1)) //nolint:gosec // position <= 18, offset is a uint8 difference
	platform.TrayIndex = uint8(position)
}

// PositionOf returns the domain holding node and its 1-based position among
// the domain's nodes, counting cliques in the order the document lists them.
func (t *TopologyDocument) PositionOf(node string) (TopologyDomain, int, bool) {
	for _, domain := range t.Domains {
		position := 0
		for _, clique := range domain.Cliques {
			for _, n := range clique.Nodes {
				position++
				if n == node {
					return domain, position, true
				}
			}
		}
	}
	return TopologyDomain{}, 0, false
}

// derivedUUID keeps the GPU-prefixed UUID shape NVML reports. The original is
// part of the hashed name, so the GPUs of one node stay distinct from each
// other and two profiles on one node do not collide.
func derivedUUID(nodeName, original string) string {
	prefix, _, found := strings.Cut(original, "-")
	if !found {
		prefix = "GPU"
	}
	id := identityHash("uuid", nodeName, original)
	return fmt.Sprintf("%s-%08x-%04x-%04x-%04x-%012x", prefix,
		binary.BigEndian.Uint32(id[0:4]), binary.BigEndian.Uint16(id[4:6]),
		binary.BigEndian.Uint16(id[6:8]), binary.BigEndian.Uint16(id[8:10]), id[10:16])
}

// derivedSerial replaces the last digits of original. Boards that share a
// serial in the profile, such as the two GPUs of a Grace superchip, still
// share one afterwards, because the original is the only varying input.
func derivedSerial(scope, original string) string {
	keep := max(len(original)-serialDigits, 0)
	sum := identityHash("serial", scope, original)
	value := binary.BigEndian.Uint64(sum[:8])
	tail := make([]byte, len(original)-keep)
	for i := len(tail) - 1; i >= 0; i-- {
		tail[i] = byte('0' + value%10)
		value /= 10
	}
	return original[:keep] + string(tail)
}

func identityHash(kind, scope, original string) [16]byte {
	h := sha1.New()
	_, _ = h.Write([]byte(identityNamespace)) // hash.Hash.Write never returns an error
	for _, part := range []string{kind, scope, original} {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(part)))
		_, _ = h.Write(n[:])
		_, _ = h.Write([]byte(part))
	}
	var id [16]byte
	copy(id[:], h.Sum(nil))
	return id
}

// applyIdentityOverlay derives the node's identities when MOKKA_IDENTITY_DERIVE
// asks for it. It mirrors applyTopologyOverlay: the node comes from NODE_NAME
// and the rack from the same topology document. A failure is logged and leaves
// the profile's identities in place, because a process that cannot name its
// node should still report GPUs.
func applyIdentityOverlay(yamlConfig *YAMLConfig, configPath string) {
	switch mode := os.Getenv(EnvIdentityDerive); mode {
	case "":
		return
	case IdentityDeriveNode:
	default:
		warnLog("[CONFIG] %s=%q is not %q; keeping the profile's identities\n", EnvIdentityDerive, mode, IdentityDeriveNode)
		return
	}

	var topo *TopologyDocument
	if path := topologyPath(configPath); path != "" {
		data, err := os.ReadFile(path)
		if err == nil {
			topo = &TopologyDocument{}
			err = yaml.Unmarshal(data, topo)
		}
		if err != nil {
			warnLog("[CONFIG] identity: cannot use topology %s, deriving without a rack position: %v\n", path, err)
			topo = nil
		}
	}
	if err := ApplyNodeIdentity(yamlConfig, os.Getenv("NODE_NAME"), topo); err != nil {
		warnLog("[CONFIG] identity: %v; keeping the profile's identities\n", err)
	}
}
