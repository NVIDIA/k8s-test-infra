// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package profile

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// Rules that combine several capture values, or values of several GPUs or
// links, into one key. Each states its working in the key's comment.

// Profile spellings of NVML enums, as the mock engine parses them.
var (
	brandNames = map[int64]string{
		int64(nvml.BRAND_NVIDIA): "nvidia", int64(nvml.BRAND_TESLA): "tesla", int64(nvml.BRAND_QUADRO): "quadro",
		int64(nvml.BRAND_GEFORCE): "geforce", int64(nvml.BRAND_TITAN): "titan", int64(nvml.BRAND_NVIDIA_RTX): "nvidia_rtx",
	}
	fabricStates = map[int64]string{
		nvml.GPU_FABRIC_STATE_NOT_SUPPORTED: "not_supported", nvml.GPU_FABRIC_STATE_NOT_STARTED: "not_started",
		nvml.GPU_FABRIC_STATE_IN_PROGRESS: "in_progress", nvml.GPU_FABRIC_STATE_COMPLETED: "completed",
	}
	peerTypes = map[int64]string{0: "direct_connected", peerTypeSwitchConnected: "switch_connected"}
	// fabricCliqueTypes are the clique types a profile can list. The
	// unicast-pointer clique has no entry: it is fabric.clique_id.
	fabricCliqueTypes = map[int64]string{
		nvml.GPU_FABRIC_CLIQUE_TYPE_MULTICAST_POINTER:          "multicast_pointer",
		nvml.GPU_FABRIC_CLIQUE_TYPE_UNICAST_LOGICAL_ENDPOINT:   "unicast_logical_endpoint",
		nvml.GPU_FABRIC_CLIQUE_TYPE_MULTICAST_LOGICAL_ENDPOINT: "multicast_logical_endpoint",
	}
	powerMizerModes = map[int64]string{
		nvml.POWER_MIZER_MODE_ADAPTIVE: "adaptive", nvml.POWER_MIZER_MODE_PREFER_MAXIMUM_PERFORMANCE: "prefer_maximum_performance",
		nvml.POWER_MIZER_MODE_AUTO: "auto", nvml.POWER_MIZER_MODE_PREFER_CONSISTENT_PERFORMANCE: "prefer_consistent_performance",
	}
	computeModes = map[int64]string{
		int64(nvml.COMPUTEMODE_DEFAULT): "default", int64(nvml.COMPUTEMODE_EXCLUSIVE_THREAD): "exclusive_thread",
		int64(nvml.COMPUTEMODE_PROHIBITED): "prohibited", int64(nvml.COMPUTEMODE_EXCLUSIVE_PROCESS): "exclusive_process",
	}
	addressingModes = map[int64]string{
		int64(nvml.DEVICE_ADDRESSING_MODE_NONE): "none", int64(nvml.DEVICE_ADDRESSING_MODE_HMM): "hmm",
		int64(nvml.DEVICE_ADDRESSING_MODE_ATS): "ats",
	}
	gomNames = map[int64]string{
		int64(nvml.GOM_ALL_ON): "all_on", int64(nvml.GOM_COMPUTE): "compute", int64(nvml.GOM_LOW_DP): "low_dp",
	}
	virtualizationModes = map[int64]string{
		int64(nvml.GPU_VIRTUALIZATION_MODE_NONE): "none", int64(nvml.GPU_VIRTUALIZATION_MODE_PASSTHROUGH): "passthrough",
		int64(nvml.GPU_VIRTUALIZATION_MODE_VGPU): "vgpu", int64(nvml.GPU_VIRTUALIZATION_MODE_HOST_VGPU): "host_vgpu",
	}
	hostVGPUModes = map[int64]string{
		int64(nvml.HOST_VGPU_MODE_NON_SRIOV): "non_sriov", int64(nvml.HOST_VGPU_MODE_SRIOV): "sriov",
	}
	// migProfileNames are NVML's GPU instance profile indexes, the first
	// argument of GetGpuInstanceProfileInfo, by the profile's nvml_profile
	// spelling.
	migProfileNames = map[int]string{
		nvml.GPU_INSTANCE_PROFILE_1_SLICE: "1_SLICE", nvml.GPU_INSTANCE_PROFILE_2_SLICE: "2_SLICE",
		nvml.GPU_INSTANCE_PROFILE_3_SLICE: "3_SLICE", nvml.GPU_INSTANCE_PROFILE_4_SLICE: "4_SLICE",
		nvml.GPU_INSTANCE_PROFILE_7_SLICE: "7_SLICE", nvml.GPU_INSTANCE_PROFILE_8_SLICE: "8_SLICE",
		nvml.GPU_INSTANCE_PROFILE_6_SLICE: "6_SLICE", nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV1: "1_SLICE_REV1",
		nvml.GPU_INSTANCE_PROFILE_2_SLICE_REV1: "2_SLICE_REV1", nvml.GPU_INSTANCE_PROFILE_1_SLICE_REV2: "1_SLICE_REV2",
		nvml.GPU_INSTANCE_PROFILE_1_SLICE_GFX: "1_SLICE_GFX", nvml.GPU_INSTANCE_PROFILE_2_SLICE_GFX: "2_SLICE_GFX",
		nvml.GPU_INSTANCE_PROFILE_4_SLICE_GFX: "4_SLICE_GFX", nvml.GPU_INSTANCE_PROFILE_1_SLICE_NO_ME: "1_SLICE_NO_ME",
		nvml.GPU_INSTANCE_PROFILE_2_SLICE_NO_ME: "2_SLICE_NO_ME", nvml.GPU_INSTANCE_PROFILE_1_SLICE_ALL_ME: "1_SLICE_ALL_ME",
		nvml.GPU_INSTANCE_PROFILE_2_SLICE_ALL_ME: "2_SLICE_ALL_ME", nvml.GPU_INSTANCE_PROFILE_3_SLICE_GFX: "3_SLICE_GFX",
	}
)

// peerTypeSwitchConnected is NVML's platform-info peer type for GPUs that
// reach their peers through NVSwitch trays.
const peerTypeSwitchConnected = 1

// placeholder sets an identifier the capture scrubbed: -base's value, else the
// placeholder the capture holds.
func (s *section) placeholder(key string, src source) {
	s.baseFirst(key, src, "placeholder, scrubbed in the capture")
}

// preferBase sets a key whose profile value describes the mock's behaviour
// more than the board: -base's value, else what the capture reports.
func (s *section) preferBase(key string, src source) {
	s.baseFirst(key, src, "")
}

func (s *section) baseFirst(key string, src source, note string) {
	if v, ok := s.g.base.scalar(s.path(key)); ok {
		s.m.set(key, v, "from -base")
		s.trace(key, src.from, nil, v.text, ruleBase)
		return
	}
	s.prop(key, src)
	if e := s.m.find(key); e != nil && note != "" {
		e.comment = note + ": " + e.comment
	}
}

func (g *generator) maxGPUInstances(gpu int) (int64, string, string) {
	best := int64(-1)
	for idx := range migProfileNames {
		v, why := recorded(g.c, gpu, call("GetGpuInstanceProfileInfo", idx), []any{"InstanceCount"})
		if why != "" {
			continue
		}
		if n, ok := asInt(v); ok && n > best {
			best = n
		}
	}
	if best < 0 {
		return 0, "", "no GPU instance profile answers"
	}
	return best, strconv.FormatInt(best, 10), ""
}

// c2c reports whether the GPUs reach their CPU over NVLink-C2C.
func (g *generator) c2c() bool {
	for _, gpu := range g.c.gpus {
		if n := gpu.find("c2c_mode"); n != nil && n.text() == "Enabled" {
			return true
		}
	}
	return false
}

// cpu describes the CPU the GPUs reach over NVLink-C2C, which is part of the
// board. A GPU on PCIe can sit behind any CPU, so its profile makes no claim
// about one. The values are per CPU, and the profile has one CPU block: lscpu's
// cores per socket, and the memory of the first NUMA node with CPUs, as other
// values are GPU 0's.
func (g *generator) cpu(s *section) {
	if len(g.c.gpus) == 0 {
		s.m.note("cpu: left out, " + smiXMLFile + " not captured, so whether the GPUs reach a CPU over NVLink-C2C is unknown")
		return
	}
	if !g.c2c() {
		s.m.note("cpu: left out, the GPUs reach the CPU over PCIe (" + smiXMLFile + " c2c_mode), so it is not part of the board")
		return
	}
	cpu := s.sub("cpu")
	info := colonValues(g.c.files[lscpuFile])
	switch model := info["BIOS Model name"]; {
	case model == "":
		cpu.m.note("type: left out, " + lscpuFile + " has no BIOS Model name")
	default:
		cpu.value("type", quote(strings.ToLower(strings.Fields(model)[0])),
			lscpuFile+" BIOS Model name = "+model+": the family is its first word", ruleSame)
	}
	if cores, err := strconv.Atoi(info["Core(s) per socket"]); err == nil {
		cpu.value("cores", integer(cores), lscpuFile+" Core(s) per socket, of "+info["Socket(s)"]+" sockets", ruleSame)
	} else {
		cpu.m.note("cores: left out, " + lscpuFile + " has no Core(s) per socket")
	}
	if nodes := cpuNodes(g.c.files[numactlFile]); len(nodes) > 0 {
		first := nodes[0]
		gib := int64(math.Round(float64(first.mib) / 1024))
		from := fmt.Sprintf("%s node %s size, the first CPU's: %d MiB = %d GiB", numactlFile, first.id, first.mib, gib)
		var others []string
		for _, n := range nodes[1:] {
			others = append(others, fmt.Sprintf("node %s %d MiB", n.id, n.mib))
		}
		if len(others) > 0 {
			from += "; " + strings.Join(others, ", ")
		}
		cpu.value("memory_gb", integer(gib), from, "the first CPU's")
	} else {
		cpu.m.note("memory_gb: left out, " + numactlFile + " lists no NUMA node with CPUs")
	}
	cpu.value("coherent_memory", boolean(true), smiXMLFile+" c2c_mode = Enabled: NVLink-C2C memory is coherent", ruleSame)
}

// gpuNUMANode is the NUMA node sysfs reports for the GPU's PCI function.
func gpuNUMANode() source {
	return source{from: pciNvidiaFile + " numa of the GPU's bus ID", read: func(c *capture, gpu int) (any, string) {
		bus, why := busID(c, gpu)
		if why != "" {
			return nil, why
		}
		n, ok := pciNUMA(c.files[pciNvidiaFile])[strings.ToLower(bus)]
		if !ok {
			return nil, "no numa for " + bus
		}
		return n, ""
	}, format: func(raw any) (string, bool) { return strconv.Itoa(raw.(int)), true }}
}

func busID(c *capture, gpu int) (string, string) {
	v, why := recorded(c, gpu, "GetPciInfo", []any{"BusIdLegacy"})
	if why != "" {
		return "", "GetPciInfo = " + why
	}
	s, ok := cString(v)
	if !ok {
		return "", "unreadable bus ID"
	}
	return s, ""
}

// supportedClocks copies the supported clock table from smi-q.xml: the dump
// cannot list it, since the getters take a buffer size.
func (g *generator) supportedClocks(s *section) {
	why := "the dump has no table"
	if r, ok := g.c.call(0, "GetSupportedMemoryClocks"); ok {
		why = "GetSupportedMemoryClocks = " + r.Ret
	}
	if len(g.c.gpus) == 0 {
		s.m.note("supported_clocks: left out, " + smiXMLFile + " not captured and " + why)
		return
	}
	tables := make([]string, len(g.c.gpus))
	for i, gpu := range g.c.gpus {
		tables[i] = render(nil, &mapping{entries: []*entry{{key: "t", value: clockTable(gpu)}}})
	}
	if !allEqual(tables) {
		s.m.note("supported_clocks: left out, the GPUs list different tables in " + smiXMLFile)
		return
	}
	first := clockTable(g.c.gpus[0])
	if len(first.items) == 0 {
		s.m.note("supported_clocks: left out, " + smiXMLFile + " lists none")
		return
	}
	s.value("supported_clocks", &mapping{entries: []*entry{{key: "memory_clocks", value: first}}},
		smiXMLFile+" supported_clocks, the same on every GPU ("+why+")", ruleSame)
}

// clockTable reads one GPU's supported_clocks element.
func clockTable(gpu *xmlNode) *sequence {
	seq := &sequence{}
	table := gpu.find("supported_clocks")
	if table == nil {
		return seq
	}
	for _, mem := range table.all("supported_mem_clock") {
		freq := mem.find("value")
		if freq == nil {
			continue
		}
		m := &mapping{}
		m.set("freq_mhz", scalar{mhz(freq.text())}, "")
		graphics := &sequence{}
		for _, gc := range mem.all("supported_graphics_clock") {
			graphics.items = append(graphics.items, scalar{mhz(gc.text())})
		}
		m.set("graphics_clocks", graphics, "")
		seq.items = append(seq.items, m)
	}
	return seq
}

func mhz(text string) string { return strings.TrimSuffix(text, " MHz") }

// processes writes an empty process list when no GPU runs one; a capture
// of a busy node holds processes no profile should copy.
func (g *generator) processes(s *section) {
	for i := 0; i < g.c.gpuCount(); i++ {
		v, why := recorded(g.c, i, "GetComputeRunningProcesses", nil)
		if why != "" {
			s.fromBase("processes", "GetComputeRunningProcesses = "+why)
			return
		}
		if l, _ := v.([]any); len(l) > 0 {
			s.fromBase("processes", "GPU "+itoa(i)+" was running processes")
			return
		}
	}
	s.value("processes", &sequence{}, "GetComputeRunningProcesses = [] on every GPU, idle reading", ruleReading)
}

// nvlinkLinks counts the links each GPU answers GetNvLinkState for.
func (g *generator) nvlinkLinks(s *section) {
	counts := make([]string, g.c.gpuCount())
	for i := range counts {
		n := 0
		for link := 0; ; link++ {
			r, ok := g.c.call(i, call("GetNvLinkState", link))
			if !ok {
				break
			}
			if r.Ret == "SUCCESS" {
				n++
			}
		}
		counts[i] = itoa(n)
	}
	if !allEqual(counts) {
		g.errs = append(g.errs, fmt.Errorf("nvlink.links_per_gpu: the GPUs answer for different link counts (%s)", strings.Join(counts, ", ")))
		return
	}
	if counts[0] == "0" {
		s.m.note("links_per_gpu: left out, no GPU answers GetNvLinkState")
		return
	}
	s.value("links_per_gpu", scalar{counts[0]}, "links GetNvLinkState(link) answers on every GPU", ruleSame)
}

// nvlinkVersion requires every active link of every GPU to report the same
// version.
func (g *generator) nvlinkVersion(s *section) {
	seen := map[string]int{}
	for i := 0; i < g.c.gpuCount(); i++ {
		for link := 0; ; link++ {
			r, ok := g.c.call(i, call("GetNvLinkVersion", link))
			if !ok {
				break
			}
			if r.Ret == "SUCCESS" {
				seen[show(r.Value)]++
			}
		}
	}
	if len(seen) == 0 {
		s.m.note("version: left out, no GPU answers GetNvLinkVersion")
		return
	}
	if len(seen) > 1 {
		g.errs = append(g.errs, fmt.Errorf("nvlink.version: the links report different versions %v", seen))
		return
	}
	for raw, n := range seen {
		gen, ok := nvlinkGeneration(stringNumber(raw))
		if !ok {
			g.errs = append(g.errs, fmt.Errorf("nvlink.version: NVML version %s has no generation", raw))
			return
		}
		s.value("version", scalar{gen}, fmt.Sprintf("GetNvLinkVersion(link) = %s on all %d links (NVML's enum; the key is the generation)", raw, n), ruleSame)
	}
}

var gbPerSecond = regexp.MustCompile(`^(\d+(?:\.\d+)?) GB/s$`)

// nvlinkBandwidth reads `nvidia-smi nvlink -s`; NVML records no per-link
// speed this driver answers for every link.
func (g *generator) nvlinkBandwidth(s *section) {
	speeds := nvlinkSpeedsByGPU(g.c.files[nvlinkSpeeds])
	seen := map[string]int{}
	for _, links := range speeds {
		for _, sp := range links {
			if gbPerSecond.MatchString(sp) {
				seen[sp]++
			}
		}
	}
	if len(seen) != 1 {
		why := fmt.Sprintf("%s lists %d distinct link speeds", nvlinkSpeeds, len(seen))
		if _, captured := g.c.files[nvlinkSpeeds]; !captured {
			why = nvlinkSpeeds + " not captured"
		}
		s.m.note("bandwidth_per_link_mbps: left out, " + why)
		return
	}
	for sp, n := range seen {
		gb, _ := strconv.ParseFloat(gbPerSecond.FindStringSubmatch(sp)[1], 64)
		s.value("bandwidth_per_link_mbps", integer(int64(gb*1000)), fmt.Sprintf("%s: %s on all %d links", nvlinkSpeeds, sp, n), ruleSame)
	}
}

func (g *generator) nvlinkBwMode(s *section) {
	a := g.answers(getter("GetNvlinkBwMode", "BwMode"))
	for _, x := range a {
		if x.missing != "" {
			// A board whose mode is system-wide answers only the system
			// getter, which the dump does not record.
			s.fromBase("bw_mode", "GetNvlinkBwMode = "+x.missing+" and the dump has no system-wide getter")
			return
		}
	}
	bw := s.sub("bw_mode")
	bw.value("scope", quote("device"), "GetNvlinkBwMode answers per device", ruleSame)
	modes := make([]string, g.c.gpuCount())
	var first *sequence
	for i := range modes {
		seq, why := supportedBwModes(g.c, i)
		if why != "" {
			bw.m.note("supported: left out, GetNvlinkSupportedBwModes = " + why)
			return
		}
		t, _ := scalars(seq)
		modes[i] = strings.Join(t, ",")
		if first == nil {
			first = seq
		}
	}
	if !allEqual(modes) {
		bw.m.note("supported: left out, the GPUs list different modes")
		return
	}
	bw.value("supported", first, "GetNvlinkSupportedBwModes.BwModes[:TotalBwModes]", ruleSame)
	// The current mode is written only when the driver did not choose it
	// itself; a block without a mode answers the best one.
	if best, _ := recorded(g.c, 0, "GetNvlinkBwMode", []any{"BIsBest"}); show(best) == "0" {
		bw.prop("mode", getter("GetNvlinkBwMode", "BwMode"))
	}
}

// fabricCliquesRule lists the cliques of the types whose id is not clique_id.
// The engine gives every type it reports clique_id, so the others need no key.
func (g *generator) fabricCliquesRule(s *section) {
	lists := make([]string, g.c.gpuCount())
	var first *sequence
	for i := range lists {
		seq, why := fabricCliques(g.c, i)
		if why != "" {
			s.m.note("cliques: left out, GetGpuFabricInfo_v4 = " + why)
			return
		}
		lists[i] = flowNode(seq)
		if first == nil {
			first = seq
		}
	}
	if !allEqual(lists) {
		s.m.note("cliques: left out, the GPUs report different cliques")
		return
	}
	if len(first.items) > 0 {
		s.value("cliques", first, "GetGpuFabricInfo_v4.Cliques[:NumCliques], the types whose id is not clique_id", ruleSame)
	}
}

// fabricClique is one entry of a GetGpuFabricInfo_v4 answer.
type fabricClique struct{ typ, id int64 }

// fabricCliques keeps, of the cliques a GPU reports, the types that have a
// clique other than the unicast-pointer one, which is what clique_id holds.
// Such a type is kept whole: the profile's list replaces the engine's default
// for it.
func fabricCliques(c *capture, gpu int) (*sequence, string) {
	cliques, why := recordedFabricCliques(c, gpu)
	if why != "" {
		return nil, why
	}
	pointer := slices.IndexFunc(cliques, func(q fabricClique) bool {
		return q.typ == nvml.GPU_FABRIC_CLIQUE_TYPE_UNICAST_POINTER
	})
	if pointer < 0 {
		return nil, "no unicast-pointer clique"
	}
	differs := map[int64]bool{}
	for _, q := range cliques {
		if q.id != cliques[pointer].id {
			differs[q.typ] = true
		}
	}
	seq := &sequence{}
	for _, q := range cliques {
		if !differs[q.typ] {
			continue
		}
		name, ok := fabricCliqueTypes[q.typ]
		if !ok {
			return nil, fmt.Sprintf("a clique of type %d, which a profile cannot set", q.typ)
		}
		m := &mapping{}
		m.set("type", quote(name), "")
		m.set("id", integer(q.id), "")
		seq.items = append(seq.items, m)
	}
	return seq, ""
}

// recordedFabricCliques reads the first NumCliques entries of a GPU's
// GetGpuFabricInfo_v4 answer.
func recordedFabricCliques(c *capture, gpu int) ([]fabricClique, string) {
	v, why := recorded(c, gpu, "GetGpuFabricInfo_v4", nil)
	if why != "" {
		return nil, why
	}
	total, _ := at(v, "NumCliques")
	list, _ := at(v, "Cliques")
	n, okN := asInt(total)
	l, okL := list.([]any)
	if !okN || !okL || int(n) > len(l) {
		return nil, "unreadable answer"
	}
	cliques := make([]fabricClique, n)
	for i, entry := range l[:n] {
		typ, _ := at(entry, "Type")
		id, _ := at(entry, "Id")
		t, okT := asInt(typ)
		d, okD := asInt(id)
		if !okT || !okD {
			return nil, "unreadable answer"
		}
		cliques[i] = fabricClique{t, d}
	}
	return cliques, ""
}

// supportedBwModes reads the first TotalBwModes entries of a GPU's
// GetNvlinkSupportedBwModes answer.
func supportedBwModes(c *capture, gpu int) (*sequence, string) {
	v, why := recorded(c, gpu, "GetNvlinkSupportedBwModes", nil)
	if why != "" {
		return nil, why
	}
	total, _ := at(v, "TotalBwModes")
	list, _ := at(v, "BwModes")
	n, okN := asInt(total)
	l, okL := list.([]any)
	if !okN || !okL || int(n) > len(l) {
		return nil, "unreadable answer"
	}
	seq := &sequence{}
	for _, m := range l[:n] {
		seq.items = append(seq.items, scalar{show(m)})
	}
	return seq, ""
}

// pcieTopologyRules groups the GPUs by the root complex sysfs shows them
// under, each with that function's NUMA node.
func (g *generator) pcieTopologyRules(s *section) {
	roots := pciRoots(g.c.files[pciTreeFile])
	numa := pciNUMA(g.c.files[pciNvidiaFile])
	type rootComplex struct {
		id      string
		numa    int
		devices []string
	}
	var order []*rootComplex
	byID := map[string]*rootComplex{}
	for i := 0; i < g.c.gpuCount(); i++ {
		bus, why := busID(g.c, i)
		if why != "" {
			s.m.note("left out: " + why)
			return
		}
		root, okR := roots[strings.ToLower(bus)]
		node, okN := numa[strings.ToLower(bus)]
		if !okR || !okN {
			s.m.note("left out: " + pciTreeFile + " or " + pciNvidiaFile + " has no entry for " + bus)
			return
		}
		c := byID[root]
		if c == nil {
			c = &rootComplex{id: root, numa: node}
			byID[root] = c
			order = append(order, c)
		}
		c.devices = append(c.devices, bus)
	}
	seq := &sequence{}
	for _, c := range order {
		m := &mapping{}
		m.set("id", quote(c.id), "")
		m.set("numa_node", integer(c.numa), "")
		devices := &sequence{}
		for _, d := range c.devices {
			devices.items = append(devices.items, quote(d))
		}
		m.set("devices", devices, "")
		seq.items = append(seq.items, m)
	}
	s.value("root_complexes", seq, pciTreeFile+" root of each GPU; numa_node from "+pciNvidiaFile, ruleSame)
}

// migTable builds the MIG profile table, the profile's sibling .mig.yaml:
// sizes from GetGpuInstanceProfileInfo, names and placements from
// nvidia-smi, which lists them while MIG is disabled.
func (g *generator) migTable() {
	names := migNames(g.c.files[migProfiles])
	if len(names) == 0 {
		return
	}
	byID, err := g.migProfileIndexes()
	if err != nil {
		g.errs = append(g.errs, err)
		return
	}
	placements := migPlacementsByProfile(g.c.files[migPlacements])
	profiles := &sequence{}
	for _, name := range names {
		idx, ok := byID[name.id]
		if !ok {
			g.errs = append(g.errs, fmt.Errorf("MIG table: %s lists profile %d (%s), which no GetGpuInstanceProfileInfo answer has", migProfiles, name.id, name.name))
			return
		}
		r, _ := g.c.call(0, call("GetGpuInstanceProfileInfo", idx))
		profiles.items = append(profiles.items, migRow(name, idx, r.Value, placements[name.id]))
	}
	g.mig = &mapping{}
	g.mig.set("version", integer(1), "")
	g.mig.set("supported_profiles", profiles, "")
}

// migProfileIndexes maps each profile ID to the NVML profile index that
// answers with it, requiring every GPU to answer the same.
func (g *generator) migProfileIndexes() (map[int]int, error) {
	byID := map[int]int{}
	indexes := make([]int, 0, len(migProfileNames))
	for idx := range migProfileNames {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	for _, idx := range indexes {
		key := call("GetGpuInstanceProfileInfo", idx)
		texts := make([]string, g.c.gpuCount())
		for i := range texts {
			texts[i] = "-"
			if r, ok := g.c.call(i, key); ok && r.Ret == "SUCCESS" {
				texts[i] = show(r.Value)
			}
		}
		if texts[0] == "-" {
			continue
		}
		if !allEqual(texts) {
			return nil, fmt.Errorf("MIG table: %s differs between GPUs", key)
		}
		r, _ := g.c.call(0, key)
		id, _ := at(r.Value, "Id")
		n, _ := asInt(id)
		byID[int(n)] = idx
	}
	return byID, nil
}

var migEngines = []struct{ key, field string }{
	{"decoders", "DecoderCount"}, {"encoders", "EncoderCount"}, {"jpeg", "JpegCount"}, {"ofa", "OfaCount"},
}

// migRow is one GPU instance profile of the table.
func migRow(name migName, idx int, info any, pl migPlacement) *mapping {
	get := func(f string) int64 {
		v, _ := at(info, f)
		n, _ := asInt(v)
		return n
	}
	p := &mapping{}
	p.set("name", quote(name.name), fmt.Sprintf("%s; GetGpuInstanceProfileInfo(%d)", migProfiles, idx))
	p.set("nvml_profile", quote(migProfileNames[idx]), "")
	p.set("slices", integer(get("SliceCount")), "")
	p.set("profile_id", integer(get("Id")), "")
	p.set("instances", integer(get("InstanceCount")), "")
	p.set("memory_mb", integer(get("MemorySizeMB")), "")
	p.set("multiprocessors", integer(get("MultiprocessorCount")), "")
	p.set("copy_engines", integer(get("CopyEngineCount")), "")
	for _, e := range migEngines {
		if v := get(e.field); v != 0 {
			p.set(e.key, integer(v), "")
		}
	}
	if len(pl.starts) > 0 {
		seq := &sequence{}
		for _, start := range pl.starts {
			seq.items = append(seq.items, &mapping{flow: true, entries: []*entry{
				{key: "start", value: integer(start)}, {key: "size", value: integer(pl.size)},
			}})
		}
		p.set("placements", seq, migPlacements)
	}
	ci := &mapping{}
	ci.set("nvml_profile", quote(fmt.Sprintf("%d_SLICE", get("SliceCount"))), "")
	ci.set("slices", integer(get("SliceCount")), "")
	ci.set("instances", integer(1), "")
	ci.set("multiprocessors", integer(get("MultiprocessorCount")), "")
	ci.set("shared_copy_engines", integer(get("CopyEngineCount")), "")
	for _, e := range migEngines {
		if v := get(e.field); v != 0 {
			ci.set(e.key, integer(v), "")
		}
	}
	p.set("compute_instances", &sequence{items: []node{ci}}, "assumed: one compute instance spanning the GPU instance")
	return p
}

func allEqual(s []string) bool {
	for _, x := range s[1:] {
		if x != s[0] {
			return false
		}
	}
	return true
}

func itoa(n int) string { return strconv.Itoa(n) }

func countOf(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return itoa(n) + " " + what + "s"
}

func stringNumber(s string) any { return json.Number(s) }

// physicalFunctions drops the SR-IOV virtual functions from the RDMA devices
// and counts them: rdma-sysfs.txt gives each device's PCI function, and
// pci-nic-functions.txt says which functions are virtual. Without either it
// says why the devices cannot be told apart.
func (g *generator) physicalFunctions(devs []rdmaDevice) ([]rdmaDevice, int, string) {
	for _, f := range []string{rdmaSysfsFile, nicFunctions} {
		if _, ok := g.c.files[f]; !ok {
			return nil, 0, f + " not captured, so virtual functions cannot be told from NICs"
		}
	}
	bus := rdmaBusIDs(g.c.files[rdmaSysfsFile])
	virtual := virtualFunctions(g.c.files[nicFunctions])
	var out []rdmaDevice
	vfs := 0
	for _, d := range devs {
		b, ok := bus[d.name]
		if !ok {
			return nil, 0, rdmaSysfsFile + " has no devpath for " + d.name
		}
		vf, ok := virtual[b]
		if !ok {
			return nil, 0, nicFunctions + " has no entry for " + b + ", the function of " + d.name
		}
		if vf {
			vfs++
			continue
		}
		out = append(out, d)
	}
	return out, vfs, ""
}

// majority returns the most common key, how many devices have it, and a note
// listing the others. A tie for the most common is an error: the profile has
// room for one value, and picking one by name would hide the other.
func majority(devs []rdmaDevice, key func(rdmaDevice) string) (string, int, string, error) {
	counts := map[string]int{}
	for _, d := range devs {
		counts[key(d)]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > 1 && counts[keys[0]] == counts[keys[1]] {
		var tied []string
		for _, k := range keys {
			if counts[k] == counts[keys[0]] {
				tied = append(tied, fmt.Sprintf("%q", k))
			}
		}
		return "", 0, "", fmt.Errorf("%s tie with %s each", strings.Join(tied, " and "), countOf(counts[keys[0]], "device"))
	}
	others := make([]string, 0, len(keys)-1)
	for _, k := range keys[1:] {
		others = append(others, fmt.Sprintf("%s x%d", k, counts[k]))
	}
	note := ""
	if len(others) > 0 {
		note = "; the rest: " + strings.Join(others, ", ")
	}
	return keys[0], counts[keys[0]], note, nil
}

func filter(devs []rdmaDevice, keep func(rdmaDevice) bool) []rdmaDevice {
	var out []rdmaDevice
	for _, d := range devs {
		if keep(d) {
			out = append(out, d)
		}
	}
	return out
}
