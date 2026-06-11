// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package fabric

import "sort"

// NeighborsExcept returns all ports other than p in stable graph order.
func (g *Graph) NeighborsExcept(p Port) []Port {
	var out []Port
	for _, c := range g.ports {
		if c.PortGUID != p.PortGUID {
			out = append(out, c)
		}
	}
	return out
}

// PeerAtOutbound selects the remote port at the end of one DR hop in the full mesh.
// outPort is the outbound port number from the SMP direct-route path (1-based).
func (g *Graph) PeerAtOutbound(from Port, outPort uint8, hopIndex int) (Port, bool) {
	if hopIndex == 0 && from.Local {
		return g.peerFirstHop(from, outPort)
	}
	return g.peerNextHop(from, outPort, hopIndex)
}

func (g *Graph) peerFirstHop(from Port, outPort uint8) (Port, bool) {
	remotes := g.remotePorts(from.CAName)
	if len(remotes) == 0 {
		return Port{}, false
	}
	idx := int(outPort) - 1
	if idx < 0 {
		idx = 0
	}
	idx %= len(remotes)
	return remotes[idx], true
}

func (g *Graph) peerNextHop(from Port, outPort uint8, hopIndex int) (Port, bool) {
	var candidates []Port
	for _, c := range g.ports {
		if c.Local {
			continue
		}
		if c.PortGUID == from.PortGUID {
			continue
		}
		if from.PodIP != "" && c.PodIP == from.PodIP {
			continue
		}
		if from.CAName != "" && c.CAName != from.CAName {
			continue
		}
		candidates = append(candidates, c)
	}
	if len(candidates) == 0 {
		return Port{}, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].PortGUID < candidates[j].PortGUID
	})
	idx := int(outPort) - 1 + hopIndex
	if idx < 0 {
		idx = 0
	}
	idx %= len(candidates)
	return candidates[idx], true
}

func (g *Graph) remotePorts(caName string) []Port {
	var remotes []Port
	for _, c := range g.ports {
		if c.Local {
			continue
		}
		if caName != "" && c.CAName != caName {
			continue
		}
		remotes = append(remotes, c)
	}
	sort.Slice(remotes, func(i, j int) bool {
		return remotes[i].PortGUID < remotes[j].PortGUID
	})
	return remotes
}
