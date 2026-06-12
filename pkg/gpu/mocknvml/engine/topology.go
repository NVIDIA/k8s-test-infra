// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package engine

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// NodeFabric is the single, immutable node-level topology model. It is
// built once at config load and shared (read-only) by every
// ConfigurableDevice, which holds only its own device index. Every NVLink,
// topology, and affinity surface is *derived* from this model so the
// matrix nvidia-smi prints cannot drift between getters.
//
// NodeFabric is safe for concurrent reads: after BuildNodeFabric returns,
// nothing mutates it, and the NVLink counters are computed as a pure
// function of the immutable per-link rate and an injectable clock.
type NodeFabric struct {
	numDevices int

	links     [][]ResolvedLink          // [deviceIndex] -> resolved links
	nvCount   [][]int                   // [i][j] NVLink count between GPU i and j
	pcieLevel [][]nvml.GpuTopologyLevel // [i][j] pairwise PCIe topology level
	numaOf    []int                     // [deviceIndex] -> NUMA node
	rootOf    []string                  // [deviceIndex] -> root complex id
	cpusOf    [][]int                   // [deviceIndex] -> CPU id list

	switches []NVSwitch

	// hasPCIe is true when a pcie_topology block supplied root-complex /
	// NUMA facts. When false, pairwise topology and affinity fall back to
	// per-device defaults (preserving legacy behavior).
	hasPCIe bool
	version uint32

	// epoch anchors the deterministic NVLink counter accrual. It is
	// process-independent so counters grow across separate nvidia-smi
	// invocations. now is injectable for tests.
	epoch time.Time
	now   func() time.Time

	warnings []string
}

// RemoteKind classifies the far end of an NVLink.
type RemoteKind uint8

const (
	RemoteNone RemoteKind = iota
	RemoteGPU
	RemoteSwitch
	RemoteCPU
)

// ResolvedLink is the derived per-(device,link) view used by all NVLink
// getters. It is computed once from the adjacency configuration.
type ResolvedLink struct {
	Link       int
	Active     bool
	RemoteKind RemoteKind
	RemoteBDF  string
	RemotePeer int // device index when RemoteKind==RemoteGPU, else -1
	Version    uint32
	Caps       uint32 // bitmap indexed by nvml.NvLinkCapability

	bwBytesSec uint64
	seed       uint64
	rate       float64 // utilization counter accrual (units/sec)
	errSeed    uint64
	errRate    float64 // error counter accrual (errors/sec)
}

// NVSwitch is an NVLink remote endpoint. NVSwitches exist in the model
// (they are what fake-fabricmanager manages) but are intentionally not
// surfaced through the nvmlUnit* API, matching real GPU nodes.
type NVSwitch struct {
	Index int
	BDF   string
	UUID  string
}

const (
	// DefaultCoresPerNUMA synthesizes a CPU affinity set when a profile
	// declares NUMA nodes but no explicit cpu_affinity range.
	DefaultCoresPerNUMA = 64

	// DefaultNVLinkDutyCycle is the fraction of line rate accrued into the
	// utilization counters when the profile does not set one. A small
	// positive value makes counters visibly grow between samples.
	DefaultNVLinkDutyCycle = 0.05

	// nvLinkMaxLinks bounds the per-device link index. Indices outside
	// [0, nvLinkMaxLinks) are rejected as invalid arguments.
	nvLinkMaxLinks = 18
)

// defaultNVLinkCaps is the capability bitmap reported for active GPU/switch
// links: P2P + sysmem access + atomics + SLI + valid. This mirrors what a
// real HGX/NVL GPU reports for `nvidia-smi nvlink -c` (every cap "true").
var defaultNVLinkCaps = capBit(nvml.NVLINK_CAP_P2P_SUPPORTED) |
	capBit(nvml.NVLINK_CAP_SYSMEM_ACCESS) |
	capBit(nvml.NVLINK_CAP_P2P_ATOMICS) |
	capBit(nvml.NVLINK_CAP_SYSMEM_ATOMICS) |
	capBit(nvml.NVLINK_CAP_SLI_BRIDGE) |
	capBit(nvml.NVLINK_CAP_VALID)

func capBit(c nvml.NvLinkCapability) uint32 {
	return uint32(1) << uint(c)
}

// BuildNodeFabric constructs the immutable node fabric from the loaded
// configuration. It never fails: misconfiguration is recorded as warnings
// (see Validate) rather than blocking startup, matching the project's
// tolerant style.
func BuildNodeFabric(cfg *Config) *NodeFabric {
	n := 0
	if cfg != nil {
		n = cfg.NumDevices
	}
	if n > MaxDevices {
		n = MaxDevices
	}
	if n < 0 {
		n = 0
	}

	f := &NodeFabric{
		numDevices: n,
		links:      make([][]ResolvedLink, n),
		nvCount:    make([][]int, n),
		pcieLevel:  make([][]nvml.GpuTopologyLevel, n),
		numaOf:     make([]int, n),
		rootOf:     make([]string, n),
		cpusOf:     make([][]int, n),
		now:        time.Now,
		epoch:      resolveCounterEpoch(),
	}
	for i := 0; i < n; i++ {
		f.nvCount[i] = make([]int, n)
		f.pcieLevel[i] = make([]nvml.GpuTopologyLevel, n)
	}

	var yc *YAMLConfig
	if cfg != nil {
		yc = cfg.YAMLConfig
	}

	// Resolve per-device BDFs (the join key into the topology blocks).
	bdfOfDev := make([]string, n)
	bdfToIndex := make(map[string]int, n)
	for i := 0; i < n; i++ {
		bdf := ""
		if cfg != nil {
			bdf = strings.ToLower(cfg.GetDevicePCIBusID(i))
		}
		bdfOfDev[i] = bdf
		if bdf != "" {
			bdfToIndex[bdf] = i
		}
	}

	f.resolveAffinity(yc, bdfOfDev, bdfToIndex)
	switchBDFs := f.resolveSwitches(yc)
	f.resolveLinks(yc, n, bdfOfDev, bdfToIndex, switchBDFs)

	f.computeNVCounts()
	f.computePCIeLevels()
	return f
}

func (f *NodeFabric) resolveAffinity(yc *YAMLConfig, bdfOfDev []string, bdfToIndex map[string]int) {
	coresPerNUMA := 0
	cpuByBDF := map[string][]int{}

	if yc != nil && yc.PCIeTopology != nil && len(yc.PCIeTopology.RootComplexes) > 0 {
		f.hasPCIe = true
		coresPerNUMA = yc.PCIeTopology.CoresPerNUMA
		if coresPerNUMA <= 0 {
			coresPerNUMA = DefaultCoresPerNUMA
		}
		for _, rc := range yc.PCIeTopology.RootComplexes {
			var cpus []int
			if rc.CPUAffinity != "" {
				cpus = parseCPURange(rc.CPUAffinity)
			} else {
				start := rc.NUMANode * coresPerNUMA
				for c := 0; c < coresPerNUMA; c++ {
					cpus = append(cpus, start+c)
				}
			}
			for _, bdf := range rc.Devices {
				lb := strings.ToLower(bdf)
				cpuByBDF[lb] = cpus
				if idx, ok := bdfToIndex[lb]; ok {
					f.numaOf[idx] = rc.NUMANode
					f.rootOf[idx] = rc.ID
				}
			}
		}
	}

	for i := 0; i < f.numDevices; i++ {
		if cpus, ok := cpuByBDF[bdfOfDev[i]]; ok {
			f.cpusOf[i] = cpus
		}
	}
}

func (f *NodeFabric) resolveSwitches(yc *YAMLConfig) map[string]bool {
	switchBDFs := map[string]bool{}
	if yc == nil || yc.NVLink == nil {
		return switchBDFs
	}
	f.version = uint32(yc.NVLink.Version)
	for si, sw := range yc.NVLink.Switches {
		f.switches = append(f.switches, NVSwitch{Index: si, BDF: sw.BDF, UUID: sw.UUID})
		if bdf := strings.ToLower(strings.TrimSpace(sw.BDF)); bdf != "" {
			switchBDFs[bdf] = true
		}
	}
	return switchBDFs
}

// orderedSwitchBDFs returns the lowercased, non-empty switch BDFs in
// declaration order, used to fan auto-expanded links across switches
// deterministically.
func orderedSwitchBDFs(switches []NVSwitchConfig) []string {
	var out []string
	for _, sw := range switches {
		if bdf := strings.ToLower(strings.TrimSpace(sw.BDF)); bdf != "" {
			out = append(out, bdf)
		}
	}
	return out
}

func (f *NodeFabric) resolveLinks(yc *YAMLConfig, n int, bdfOfDev []string, bdfToIndex map[string]int, switchBDFs map[string]bool) {
	if yc == nil || yc.NVLink == nil {
		return
	}
	nv := yc.NVLink

	var defaults NVLinkDefaults
	if nv.Defaults != nil {
		defaults = *nv.Defaults
	}

	// bwBytesSec drives NvLinkSpeedMbps (= bw/1e6). Prefer the precise Mbps
	// field when set so non-integer GB/s rates (NVLink5 = 53.125 GB/s =
	// 53125 Mbps) render exactly; fall back to the whole-GB/s field.
	var bw uint64
	switch {
	case nv.BandwidthPerLinkMbps > 0:
		bw = uint64(nv.BandwidthPerLinkMbps) * 1_000_000
	case nv.BandwidthPerLinkGBPS > 0:
		bw = uint64(nv.BandwidthPerLinkGBPS) * 1_000_000_000
	}
	dutyCycle := defaults.DutyCycle
	if dutyCycle <= 0 {
		dutyCycle = DefaultNVLinkDutyCycle
	}
	rate := float64(bw) * dutyCycle

	// Build the per-device source link lists.
	deviceLinkCfgs := make([][]NVLinkLinkConfig, n)
	for i := range nv.DeviceLinks {
		dl := nv.DeviceLinks[i]
		if dl.Index >= 0 && dl.Index < n {
			deviceLinkCfgs[dl.Index] = dl.Links
		}
	}
	// Legacy flat list maps to device index 0 (back-compat) unless device 0
	// already has an explicit device_links entry.
	if len(nv.Links) > 0 && n > 0 && deviceLinkCfgs[0] == nil {
		deviceLinkCfgs[0] = nv.Links
	}

	// Auto-expand switch-attached links so the NV# matrix is fully populated
	// for HGX/NVSwitch profiles without hand-authoring N*links_per_gpu YAML
	// entries. A device with no explicit link list, on a profile that
	// declares NVSwitches and links_per_gpu, gets links_per_gpu active links
	// fanned round-robin across the declared switches. Each switch-attached
	// link reaches every other GPU through the shared fabric (computeNVCounts),
	// so this yields NV{links_per_gpu} between every pair (GB200 -> NV18).
	switchOrder := orderedSwitchBDFs(nv.Switches)
	if len(switchOrder) > 0 && nv.LinksPerGPU > 0 {
		want := nv.LinksPerGPU
		if want > nvLinkMaxLinks {
			want = nvLinkMaxLinks
		}
		for i := 0; i < n; i++ {
			if deviceLinkCfgs[i] != nil {
				continue
			}
			synth := make([]NVLinkLinkConfig, want)
			for k := 0; k < want; k++ {
				synth[k] = NVLinkLinkConfig{
					Link:             k,
					State:            defaults.State,
					RemoteDeviceType: "switch",
					RemotePCIBusID:   switchOrder[k%len(switchOrder)],
				}
			}
			deviceLinkCfgs[i] = synth
		}
	}

	for i := 0; i < n; i++ {
		for _, lc := range deviceLinkCfgs[i] {
			rl := resolveLink(lc, defaults, uint32(nv.Version), bw, rate, defaults.CounterSeed, defaults.ErrorRate, bdfOfDev, bdfToIndex, switchBDFs)
			f.links[i] = append(f.links[i], rl)
			if rl.Active && rl.RemoteBDF != "" && rl.RemoteKind != RemoteCPU &&
				rl.RemoteKind != RemoteSwitch && rl.RemotePeer < 0 {
				f.warnings = append(f.warnings, fmt.Sprintf(
					"device %d link %d: remote_pci_bus_id %q resolves to no known device or switch",
					i, rl.Link, rl.RemoteBDF))
			}
		}
	}
}

func resolveLink(lc NVLinkLinkConfig, defaults NVLinkDefaults, version uint32, bw uint64, rate float64, seed uint64, errRate float64, bdfOfDev []string, bdfToIndex map[string]int, switchBDFs map[string]bool) ResolvedLink {
	state := lc.State
	if state == "" {
		state = defaults.State
	}
	// A declared link with no explicit state defaults to active.
	active := state == "" || nvlinkStateActive(state)

	kind := parseRemoteKind(lc.RemoteDeviceType)
	remoteBDF := strings.TrimSpace(lc.RemotePCIBusID)
	if strings.EqualFold(remoteBDF, "n/a") {
		remoteBDF = ""
	}
	peer := -1

	switch {
	case lc.RemoteIndex != nil:
		peer = *lc.RemoteIndex
		if kind == RemoteNone {
			kind = RemoteGPU
		}
		if remoteBDF == "" && peer >= 0 && peer < len(bdfOfDev) {
			remoteBDF = bdfOfDev[peer]
		}
	case remoteBDF != "":
		lb := strings.ToLower(remoteBDF)
		if idx, ok := bdfToIndex[lb]; ok {
			peer = idx
			if kind == RemoteNone {
				kind = RemoteGPU
			}
		} else if switchBDFs[lb] {
			kind = RemoteSwitch
		}
	}

	caps := uint32(0)
	if active {
		switch kind {
		case RemoteCPU:
			caps = capBit(nvml.NVLINK_CAP_SYSMEM_ACCESS) | capBit(nvml.NVLINK_CAP_VALID)
		default:
			caps = defaultNVLinkCaps
		}
	}

	return ResolvedLink{
		Link:       lc.Link,
		Active:     active,
		RemoteKind: kind,
		RemoteBDF:  remoteBDF,
		RemotePeer: peer,
		Version:    version,
		Caps:       caps,
		bwBytesSec: bw,
		seed:       seed,
		rate:       rate,
		errSeed:    0,
		errRate:    errRate,
	}
}

func (f *NodeFabric) computeNVCounts() {
	switchLinks := make([]int, f.numDevices)
	for i := 0; i < f.numDevices; i++ {
		for _, l := range f.links[i] {
			if !l.Active {
				continue
			}
			switch l.RemoteKind {
			case RemoteGPU:
				if l.RemotePeer >= 0 && l.RemotePeer < f.numDevices && l.RemotePeer != i {
					f.nvCount[i][l.RemotePeer]++
				}
			case RemoteSwitch:
				switchLinks[i]++
			}
		}
	}
	// Switch-attached links reach every other GPU through the shared
	// NVSwitch fabric, so each peer sees the full switch link count
	// (this is what yields GB200's NV18 across every GPU pair).
	for i := 0; i < f.numDevices; i++ {
		if switchLinks[i] == 0 {
			continue
		}
		for j := 0; j < f.numDevices; j++ {
			if j != i {
				f.nvCount[i][j] += switchLinks[i]
			}
		}
	}
}

func (f *NodeFabric) computePCIeLevels() {
	for i := 0; i < f.numDevices; i++ {
		for j := 0; j < f.numDevices; j++ {
			switch {
			case i == j:
				f.pcieLevel[i][j] = nvml.TOPOLOGY_INTERNAL
			case !f.hasPCIe:
				f.pcieLevel[i][j] = nvml.TOPOLOGY_SINGLE
			case f.numaOf[i] != f.numaOf[j]:
				f.pcieLevel[i][j] = nvml.TOPOLOGY_SYSTEM
			case f.rootOf[i] != "" && f.rootOf[i] == f.rootOf[j]:
				f.pcieLevel[i][j] = nvml.TOPOLOGY_SINGLE
			default:
				f.pcieLevel[i][j] = nvml.TOPOLOGY_HOSTBRIDGE
			}
		}
	}
}

// Validate returns human-readable warnings for unresolved NVLink
// endpoints. Runtime callers warn-and-continue; the built-in profile test
// asserts this is empty for shipped profiles (decision D-b).
func (f *NodeFabric) Validate() []string {
	return f.warnings
}

// NumDevices returns the number of devices modeled by the fabric.
func (f *NodeFabric) NumDevices() int { return f.numDevices }

// HasPCIeTopology reports whether root-complex / NUMA facts were supplied.
func (f *NodeFabric) HasPCIeTopology() bool { return f.hasPCIe }

// Switches returns the NVSwitch endpoint list.
func (f *NodeFabric) Switches() []NVSwitch { return f.switches }

// Link returns the resolved link for (dev, link) and whether it exists.
func (f *NodeFabric) Link(dev, link int) (ResolvedLink, bool) {
	if dev < 0 || dev >= f.numDevices {
		return ResolvedLink{}, false
	}
	for _, l := range f.links[dev] {
		if l.Link == link {
			return l, true
		}
	}
	return ResolvedLink{}, false
}

// NumLinks returns the number of resolved links for a device.
func (f *NodeFabric) NumLinks(dev int) int {
	if dev < 0 || dev >= f.numDevices {
		return 0
	}
	return len(f.links[dev])
}

// NVLinkCount returns the number of NVLinks between GPU a and GPU b.
func (f *NodeFabric) NVLinkCount(a, b int) int {
	if a < 0 || a >= f.numDevices || b < 0 || b >= f.numDevices {
		return 0
	}
	return f.nvCount[a][b]
}

// TopoLevel returns the pairwise PCIe topology level between a and b.
func (f *NodeFabric) TopoLevel(a, b int) nvml.GpuTopologyLevel {
	if a < 0 || a >= f.numDevices || b < 0 || b >= f.numDevices {
		return nvml.TOPOLOGY_SINGLE
	}
	return f.pcieLevel[a][b]
}

// NumaNode returns the NUMA node for a device (-1 when unknown).
func (f *NodeFabric) NumaNode(dev int) int {
	if dev < 0 || dev >= f.numDevices || !f.hasPCIe {
		return -1
	}
	return f.numaOf[dev]
}

// CPUs returns the CPU id list affined to a device.
func (f *NodeFabric) CPUs(dev int) []int {
	if dev < 0 || dev >= f.numDevices {
		return nil
	}
	return f.cpusOf[dev]
}

// ActiveLinkCount returns the number of active NVLinks on a device. This
// backs NVML_FI_DEV_NVLINK_LINK_COUNT, which nvidia-smi queries first to
// decide how many links to enumerate for `nvlink -s/-c/-e`.
func (f *NodeFabric) ActiveLinkCount(dev int) int {
	if dev < 0 || dev >= f.numDevices {
		return 0
	}
	n := 0
	for _, l := range f.links[dev] {
		if l.Active {
			n++
		}
	}
	return n
}

// NVSwitchConnectedLinkCount returns the number of a device's active NVLinks
// whose remote endpoint is an NVSwitch. This backs
// NVML_FI_DEV_NVSWITCH_CONNECTED_LINK_COUNT (field 147), which the 580
// nvidia-smi reads per GPU in `topo -m` to detect an NVSwitch fabric and draw
// NV<count> between every switch-connected GPU pair. Returns 0 for direct or
// non-NVLink topologies (no switch endpoints), so those render no NV#.
func (f *NodeFabric) NVSwitchConnectedLinkCount(dev int) int {
	if dev < 0 || dev >= f.numDevices {
		return 0
	}
	n := 0
	for _, l := range f.links[dev] {
		if l.Active && l.RemoteKind == RemoteSwitch {
			n++
		}
	}
	return n
}

// NvLinkSpeedMbps returns the per-link speed in MB/s (NVML reports NVLink
// speed in MBps) and whether the link is active. Derived from the
// configured per-link bandwidth.
func (f *NodeFabric) NvLinkSpeedMbps(dev, link int) (uint64, bool) {
	l, ok := f.Link(dev, link)
	if !ok || !l.Active {
		return 0, false
	}
	return l.bwBytesSec / 1_000_000, true
}

// FirstActiveLink returns the index of the lowest-numbered active link on a
// device and whether one exists. Used for the "common" speed field.
func (f *NodeFabric) FirstActiveLink(dev int) (int, bool) {
	if dev < 0 || dev >= f.numDevices {
		return 0, false
	}
	best := -1
	for _, l := range f.links[dev] {
		if l.Active && (best < 0 || l.Link < best) {
			best = l.Link
		}
	}
	if best < 0 {
		return 0, false
	}
	return best, true
}

// LinkVersion returns the NVLink version for a link, falling back to the
// fabric-wide version when the link carries none.
func (f *NodeFabric) LinkVersion(dev, link int) uint32 {
	if l, ok := f.Link(dev, link); ok && l.Version != 0 {
		return l.Version
	}
	return f.version
}

// NvLinkCounters returns the deterministic (rx, tx) utilization counters
// for a link at the given time. Pure function of immutable config + clock.
func (f *NodeFabric) NvLinkCounters(dev, link int, now time.Time) (uint64, uint64) {
	l, ok := f.Link(dev, link)
	if !ok || !l.Active {
		return 0, 0
	}
	v := accrue(l.seed, l.rate, f.epoch, now)
	return v, v
}

// NvLinkErrorCount returns the deterministic error counter for a link.
func (f *NodeFabric) NvLinkErrorCount(dev, link int, now time.Time) uint64 {
	l, ok := f.Link(dev, link)
	if !ok || !l.Active {
		return 0
	}
	return accrue(l.errSeed, l.errRate, f.epoch, now)
}

// accrue is the monotonic wall-clock accrual: seed + floor((now-epoch)*rate).
func accrue(seed uint64, rate float64, epoch, now time.Time) uint64 {
	if rate <= 0 {
		return seed
	}
	dt := now.Sub(epoch).Seconds()
	if dt <= 0 {
		return seed
	}
	return seed + uint64(math.Floor(dt*rate))
}

// CPUAffinityMask packs the device's CPU set into a little-endian bitmask
// of `words` 64-bit words (the layout nvmlDeviceGetCpuAffinity expects).
func (f *NodeFabric) CPUAffinityMask(dev, words int) []uint64 {
	mask := make([]uint64, words)
	if words <= 0 {
		return mask
	}
	for _, cpu := range f.CPUs(dev) {
		if cpu < 0 {
			continue
		}
		w := cpu / 64
		if w >= words {
			continue
		}
		mask[w] |= uint64(1) << uint(cpu%64)
	}
	return mask
}

// MemoryAffinityMask packs the device's NUMA node into a little-endian
// bitmask of `words` 64-bit words.
func (f *NodeFabric) MemoryAffinityMask(dev, words int) []uint64 {
	mask := make([]uint64, words)
	if words <= 0 {
		return mask
	}
	node := f.NumaNode(dev)
	if node < 0 {
		return mask
	}
	w := node / 64
	if w < words {
		mask[w] |= uint64(1) << uint(node%64)
	}
	return mask
}

func parseRemoteKind(s string) RemoteKind {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "gpu":
		return RemoteGPU
	case "switch", "nvswitch":
		return RemoteSwitch
	case "cpu", "c2c":
		return RemoteCPU
	default:
		return RemoteNone
	}
}

// nvlinkStateActive normalizes the link state strings real configs use.
func nvlinkStateActive(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active", "up", "enabled":
		return true
	default:
		return false
	}
}

// parseCPURange parses "0-71", "0,2,4", or "0-3,8-11" into a CPU id list.
func parseCPURange(s string) []int {
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			start, err1 := strconv.Atoi(strings.TrimSpace(lo))
			end, err2 := strconv.Atoi(strings.TrimSpace(hi))
			if err1 != nil || err2 != nil || end < start {
				continue
			}
			for c := start; c <= end; c++ {
				out = append(out, c)
			}
			continue
		}
		if c, err := strconv.Atoi(part); err == nil {
			out = append(out, c)
		}
	}
	return out
}
