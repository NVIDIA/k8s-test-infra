// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package fabric models the mock InfiniBand subnet for SMP synthesis.
package fabric

import (
	"fmt"
	"sort"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/registry"
)

// Port is one HCA port in the fabric graph.
type Port struct {
	PortGUID string
	NodeGUID string
	LID      uint16
	CAName   string
	NodeName string
	PodIP    string
	Local    bool
}

// Graph holds all known ports and full-mesh neighbor rules.
type Graph struct {
	ports   []Port
	byLID   map[uint16]Port
	byGUID  map[string]Port
}

// Build merges local sysfs ports and registered peer ports.
func Build(local []protocol.PortAdvert, peers map[string]registry.Peer) *Graph {
	var ports []Port
	for _, p := range local {
		ports = append(ports, Port{
			PortGUID: p.PortGUID,
			NodeGUID: coalesceGUID(p.NodeGUID, p.PortGUID),
			LID:      p.LID,
			CAName:   p.CAName,
			Local:    true,
		})
	}
	for guid, peer := range peers {
		ports = append(ports, Port{
			PortGUID: guid,
			NodeGUID: nodeGUIDFromPortGUID(guid),
			LID:      peer.LID,
			CAName:   peer.CAName,
			NodeName: peer.NodeName,
			PodIP:    peer.PodIP,
			Local:    false,
		})
	}
	sort.Slice(ports, func(i, j int) bool {
		return ports[i].PortGUID < ports[j].PortGUID
	})
	g := &Graph{
		ports:  ports,
		byLID:  make(map[uint16]Port, len(ports)),
		byGUID: make(map[string]Port, len(ports)),
	}
	for _, p := range ports {
		g.byLID[p.LID] = p
		g.byGUID[p.PortGUID] = p
	}
	return g
}

// Ports returns all ports in the graph.
func (g *Graph) Ports() []Port {
	return append([]Port(nil), g.ports...)
}

// ByLID looks up a port by LID.
func (g *Graph) ByLID(lid uint16) (Port, bool) {
	p, ok := g.byLID[lid]
	return p, ok
}

// ByCAName returns the local port for an HCA name (e.g. mlx5_0).
func (g *Graph) ByCAName(caName string) (Port, bool) {
	for _, p := range g.ports {
		if p.CAName == caName && p.Local {
			return p, true
		}
	}
	return Port{}, false
}

// OutboundNeighbor picks one stable remote neighbor for local port P (lowest GUID).
func (g *Graph) OutboundNeighbor(p Port) (Port, bool) {
	for _, cand := range g.ports {
		if cand.PortGUID == p.PortGUID {
			continue
		}
		return cand, true
	}
	return Port{}, false
}

// InboundNeighborFor returns A as neighbor when synthesizing PORT_INFO for port B
// so ibnd sees B connected to A (full mesh via inbound edges).
func (g *Graph) InboundNeighborFor(target Port) (Port, bool) {
	for _, cand := range g.ports {
		if cand.PortGUID == target.PortGUID {
			continue
		}
		return cand, true
	}
	return Port{}, false
}

func coalesceGUID(nodeGUID, portGUID string) string {
	if nodeGUID != "" {
		return nodeGUID
	}
	return nodeGUIDFromPortGUID(portGUID)
}

// nodeGUIDFromPortGUID derives CA node_guid from port_guid (clear port U/L bit).
func nodeGUIDFromPortGUID(portGUID string) string {
	n := registry.NormalizePortGUID(portGUID)
	parts := strings.Split(n, ":")
	if len(parts) != 4 {
		return n
	}
	var last uint64
	if _, err := fmt.Sscanf(parts[3], "%x", &last); err != nil {
		return n
	}
	last &^= 1
	parts[3] = fmt.Sprintf("%04x", last&0xffff)
	return strings.Join(parts, ":")
}
