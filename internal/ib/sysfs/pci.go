// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysfs

import (
	"fmt"
	"strconv"
)

// numaNodeUnknown is what Linux writes to numa_node for a device it has no
// proximity information for.
const numaNodeUnknown = -1

// pciBus names one bus of one PCI domain.
type pciBus struct{ domain, bus int }

// parsePCIBus reads the domain and bus out of a DDDD:BB:DD.F address.
func parsePCIBus(addr string) (pciBus, error) {
	invalid := fmt.Errorf("invalid PCI address %q: want DDDD:BB:DD.F", addr)
	if len(addr) != len("dddd:bb:dd.f") || addr[4] != ':' || addr[7] != ':' || addr[10] != '.' {
		return pciBus{}, invalid
	}
	domain, err := strconv.ParseUint(addr[0:4], 16, 16)
	if err != nil {
		return pciBus{}, invalid
	}
	bus, err := strconv.ParseUint(addr[5:7], 16, 8)
	if err != nil {
		return pciBus{}, invalid
	}
	return pciBus{domain: int(domain), bus: int(bus)}, nil
}

// address names function 0 of device 0 on b: every HCA is a card of its own.
func (b pciBus) address() string {
	return fmt.Sprintf("%04x:%02x:00.0", b.domain, b.bus)
}

// netdevName is the name systemd's predictable naming gives an mlx5 Ethernet
// port on b: "en", the domain when it is not 0, the bus and slot in decimal,
// then "np0" for the port's phys_port_name "p0".
func (b pciBus) netdevName() string {
	domain := ""
	if b.domain != 0 {
		domain = "P" + strconv.Itoa(b.domain)
	}
	return fmt.Sprintf("en%sp%ds0np0", domain, b.bus)
}

// placeHCAs gives each of hcaCount HCAs a bus of its own and a NUMA node. HCA i
// pairs with GPU i*len(gpus)/hcaCount, so HCAs spread evenly across the GPUs
// whether there are more of them or fewer, and takes the first free bus after
// that GPU's. Bus 0 is never handed out: it holds the root complex itself.
func placeHCAs(hcaCount int, gpus []PCIFunction, occupied []string) ([]PCIFunction, error) {
	taken := make(map[pciBus]bool, len(gpus)+len(occupied)+hcaCount)
	gpuBuses := make([]pciBus, len(gpus))

	for i, g := range gpus {
		b, err := parsePCIBus(g.Address)
		if err != nil {
			return nil, fmt.Errorf("gpu %d: %w", i, err)
		}
		gpuBuses[i] = b
		taken[b] = true
	}
	for _, addr := range occupied {
		b, err := parsePCIBus(addr)
		if err != nil {
			return nil, err
		}
		taken[b] = true
	}

	out := make([]PCIFunction, hcaCount)
	for i := range out {
		after, numa := pciBus{bus: 0}, numaNodeUnknown
		if len(gpus) > 0 {
			g := i * len(gpus) / hcaCount
			after, numa = gpuBuses[g], gpus[g].NUMANode
		}

		b, ok := nextFreeBus(after, taken)
		if !ok {
			return nil, fmt.Errorf("mlx5_%d: no free PCI bus in domain %04x", i, after.domain)
		}
		taken[b] = true
		out[i] = PCIFunction{Address: b.address(), NUMANode: numa}
	}
	return out, nil
}

// nextFreeBus scans the domain of after for the first bus past it nothing
// holds, wrapping around.
func nextFreeBus(after pciBus, taken map[pciBus]bool) (pciBus, bool) {
	const buses = 0x100
	for off := 1; off < buses; off++ {
		b := pciBus{domain: after.domain, bus: (after.bus + off) % buses}
		if b.bus != 0 && !taken[b] {
			return b, true
		}
	}
	return pciBus{}, false
}
