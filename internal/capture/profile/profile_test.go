// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package profile

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

func ok(v any) map[string]any { return map[string]any{"ret": "SUCCESS", "value": v} }

func failed(ret string) map[string]any { return map[string]any{"ret": ret} }

// writeCapture writes a capture node directory holding an NVML dump with one
// map of calls per GPU, plus any other files.
func writeCapture(t *testing.T, gpus []map[string]any, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "board", "node-a")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	devices := make([]any, len(gpus))
	for i, calls := range gpus {
		devices[i] = map[string]any{"index": i, "calls": calls}
	}
	dump, err := json.Marshal(map[string]any{
		"system": map[string]any{
			"SystemGetDriverVersion":     ok("615.71.09"),
			"SystemGetNVMLVersion":       ok("13.615.71.09"),
			"SystemGetCudaDriverVersion": ok(13040),
			"DeviceGetCount":             ok(len(gpus)),
		},
		"devices": devices,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, dumpFilePlain), dump, 0o644))
	for name, text := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644))
	}
	return dir
}

func generateFrom(t *testing.T, dir string, base *baseProfile) (*generator, string) {
	t.Helper()
	c, err := loadCapture(dir)
	require.NoError(t, err)
	g, err := generate(c, base)
	require.NoError(t, err)
	profile, _ := g.documents("p.yaml")
	// Comments line up in a column, so assertions compare with runs of spaces
	// collapsed to one.
	return g, spaces.ReplaceAllString(profile, " ")
}

var spaces = regexp.MustCompile(` {2,}`)

func lineOf(t *testing.T, profile, key string) string {
	t.Helper()
	for _, line := range strings.Split(profile, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), key+":") {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("no %q in\n%s", key, profile)
	return ""
}

// signedField is a GetFieldValues answer holding a signed int.
func signedField(v int32) map[string]any {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint32(b, uint32(v))
	return ok(map[string]any{"valueType": 5, "value": hex.EncodeToString(b)})
}

func TestThermalThresholdsFollowTheTLimitReference(t *testing.T) {
	t.Parallel()
	gpu := func(temp, margin int) map[string]any {
		return map[string]any{
			"GetTemperature(0)":          ok(temp),
			"GetMarginTemperature":       ok(map[string]any{"MarginTemperature": margin}),
			"GetTemperatureThreshold(0)": ok(93),
			"GetTemperatureThreshold(1)": ok(88),
			"GetTemperatureThreshold(3)": ok(90),
			"GetFieldValues(id=193)":     signedField(-5),
			"GetFieldValues(id=194)":     signedField(-2),
			"GetFieldValues(id=196)":     signedField(0),
		}
	}
	// Every thermal value is GPU 0's reading, so the mock's answers add up:
	// its margin is 88 - 34 = 54, where GPU 0 reads 53.
	dir := writeCapture(t, []map[string]any{gpu(34, 53), gpu(33, 54), gpu(33, 53), gpu(34, 53)}, nil)
	_, profile := generateFrom(t, dir, nil)
	require.Equal(t,
		"reference_c: 88 # GetTemperatureThreshold(1) on GPU 0",
		lineOf(t, profile, "reference_c"))
	require.Equal(t,
		"slowdown_threshold_c: 88 # tlimit.reference_c = 88; GetTemperatureThreshold(1) reads 88 on every GPU",
		lineOf(t, profile, "slowdown_threshold_c"))
	require.Equal(t,
		"shutdown_threshold_c: 93 # tlimit.reference_c - tlimit.shutdown_c = 88 - (-5); GetTemperatureThreshold(0) reads 93 on every GPU",
		lineOf(t, profile, "shutdown_threshold_c"))
	require.Contains(t, profile,
		"max_operating_c: 90 # tlimit.reference_c - tlimit.slowdown_c = 88 - (-2); GetTemperatureThreshold(3) reads 90 on every GPU")
	require.Contains(t, profile, "max_operating_c: 0 # GetFieldValues(id=196) on GPU 0")
	require.Equal(t,
		"temperature_gpu_c: 34 # GetTemperature(0) on GPU 0, idle reading; GPUs read 34 33 33 34",
		lineOf(t, profile, "temperature_gpu_c"))
	require.Contains(t, profile,
		"# margin: the mock answers GetMarginTemperature as tlimit.reference_c - temperature_gpu_c = 88 - 34 = 54; GPU 0 reads 53")
}

func TestThermalThresholdsAreTheDriversWithoutATLimitView(t *testing.T) {
	t.Parallel()
	dir := writeCapture(t, []map[string]any{{
		"GetTemperature(0)":          ok(34),
		"GetMarginTemperature":       failed("ERROR_NOT_SUPPORTED"),
		"GetFieldValues(id=193)":     failed("ERROR_NOT_SUPPORTED"),
		"GetFieldValues(id=194)":     failed("ERROR_NOT_SUPPORTED"),
		"GetTemperatureThreshold(0)": ok(95),
		"GetTemperatureThreshold(1)": ok(90),
		"GetTemperatureThreshold(3)": ok(85),
	}}, nil)
	_, profile := generateFrom(t, dir, nil)
	require.Equal(t, "shutdown_threshold_c: 95 # GetTemperatureThreshold(0)", lineOf(t, profile, "shutdown_threshold_c"))
	require.Equal(t, "slowdown_threshold_c: 90 # GetTemperatureThreshold(1)", lineOf(t, profile, "slowdown_threshold_c"))
	const why = "GetFieldValues(id=193) on GPU 0 = ERROR_NOT_SUPPORTED; GetFieldValues(id=194) on GPU 0 = ERROR_NOT_SUPPORTED"
	require.Contains(t, profile, "# the thresholds above are the driver's, not derived from T.Limit: "+why)
	require.Contains(t, profile, "# tlimit.reference_c: left out, "+why)
}

// gpuMemory is a GPU whose memory differs from its siblings' only in what the
// driver reserves.
func gpuMemory(reserved int, vbios string, enforcedMW int) map[string]any {
	return map[string]any{
		"GetMemoryInfo_v2":               ok(map[string]any{"Total": 1000, "Reserved": reserved, "Used": 0, "Free": 1000 - reserved}),
		"GetMemoryBusWidth":              ok(5120),
		"GetVbiosVersion":                ok(vbios),
		"GetPowerManagementDefaultLimit": ok(700000),
		"GetEnforcedPowerLimit":          ok(enforcedMW),
	}
}

func TestPropertyThatDiffersPerGPUIsSetPerDevice(t *testing.T) {
	t.Parallel()
	dir := writeCapture(t, []map[string]any{gpuMemory(100, "97.00.01", 700000), gpuMemory(200, "97.00.02", 600000)}, nil)
	g, profile := generateFrom(t, dir, nil)
	require.Contains(t, profile, "# vbios_version: differs per GPU, set under devices: GetVbiosVersion")
	require.Contains(t, profile, "# reserved_bytes: differs per GPU, set under devices: GetMemoryInfo_v2.Reserved")
	require.Contains(t, profile, "# free_bytes: differs per GPU, set under devices: total_bytes - reserved_bytes - used_bytes")
	require.Equal(t, scalar{`"97.00.02"`}, g.devices[1].find("vbios_version").value)

	// The engine replaces a devices[] memory block whole, so GPU 1's carries
	// the values both GPUs share as well as its own.
	memory := g.devices[1].find("memory").value.(*mapping)
	keys := make([]string, 0, len(memory.entries))
	for _, e := range memory.entries {
		keys = append(keys, e.key)
	}
	require.Equal(t, []string{"total_bytes", "reserved_bytes", "used_bytes", "free_bytes", "memory_bus_width"}, keys)
	require.Equal(t, scalar{"800"}, memory.find("free_bytes").value)
	require.Contains(t, profile, "free_bytes: 800 # total_bytes - reserved_bytes - used_bytes = 1000 - 200 - 0; GetMemoryInfo_v2.Free reads 800")
	power := g.devices[1].find("power").value.(*mapping)
	require.Equal(t, scalar{"700000"}, power.find("default_limit_mw").value, "the shared limit is repeated")
	require.Equal(t, scalar{"600000"}, power.find("enforced_limit_mw").value)
}

// Every value a generated profile sets per device must reach that device, and
// every value the GPUs share must reach each of them: read back through the
// engine's own merge of devices[] over device_defaults.
func TestPerDeviceValuesReachTheEngine(t *testing.T) {
	cfg, _ := loadInEngine(t, writeCapture(t, []map[string]any{gpuMemory(100, "97.00.01", 700000), gpuMemory(200, "97.00.02", 600000)}, nil))
	for gpu, want := range []struct {
		reserved, free uint64
		vbios          string
		enforcedMW     uint32
	}{{100, 900, "97.00.01", 700000}, {200, 800, "97.00.02", 600000}} {
		d := cfg.GetDeviceConfig(gpu)
		require.NotNil(t, d.Memory, "GPU %d", gpu)
		require.Equal(t, uint64(1000), d.Memory.TotalBytes, "GPU %d", gpu)
		require.Equal(t, want.reserved, d.Memory.ReservedBytes, "GPU %d", gpu)
		require.Equal(t, want.free, d.Memory.FreeBytes, "GPU %d", gpu)
		require.Equal(t, uint32(5120), d.Memory.MemoryBusWidth, "GPU %d", gpu)
		require.Equal(t, want.vbios, d.VBIOSVersion, "GPU %d", gpu)
		require.NotNil(t, d.Power, "GPU %d", gpu)
		require.Equal(t, uint32(700000), d.Power.DefaultLimitMW, "GPU %d", gpu)
		require.Equal(t, want.enforcedMW, d.Power.EnforcedLimitMW, "GPU %d", gpu)
	}
}

// loadInEngine generates the profile of a capture and loads it as the mock
// engine does, returning the engine's config and the profile text.
func loadInEngine(t *testing.T, dir string) (*engine.Config, string) {
	t.Helper()
	// LoadYAMLConfig resolves the MIG table beside the profile unless this
	// variable names another, so it must be empty here.
	t.Setenv(engine.EnvMIGProfilesConfig, "")
	c, err := loadCapture(dir)
	require.NoError(t, err)
	g, err := generate(c, nil)
	require.NoError(t, err)
	profile, _ := g.documents("p.yaml")
	path := filepath.Join(t.TempDir(), "p.yaml")
	require.NoError(t, os.WriteFile(path, []byte(profile), 0o644))
	yc, err := engine.LoadYAMLConfig(path)
	require.NoError(t, err)
	return &engine.Config{YAMLConfig: yc}, profile
}

// The used reading is written once, the lower median of the GPUs'. A GPU that
// reserves more than its siblings may not have room for it: its used memory
// is lowered to the room it has, so its free memory is not negative and the
// engine still loads the profile.
func TestFreeMemoryNeverComesOutNegative(t *testing.T) {
	gpu := func(reserved, used int) map[string]any {
		return map[string]any{"GetMemoryInfo_v2": ok(map[string]any{"Total": 1000, "Reserved": reserved, "Used": used, "Free": 1000 - reserved - used})}
	}
	cfg, profile := loadInEngine(t, writeCapture(t, []map[string]any{gpu(200, 0), gpu(100, 850), gpu(100, 850)}, nil))
	for i, want := range []struct{ used, free uint64 }{{800, 0}, {850, 50}, {850, 50}} {
		d := cfg.GetDeviceConfig(i)
		require.NotNil(t, d.Memory, "GPU %d", i)
		require.Equal(t, uint64(1000), d.Memory.TotalBytes, "GPU %d", i)
		require.Equal(t, want.used, d.Memory.UsedBytes, "GPU %d", i)
		require.Equal(t, want.free, d.Memory.FreeBytes, "GPU %d", i)
	}
	require.Contains(t, spaces.ReplaceAllString(profile, " "),
		"used_bytes: 800 # total_bytes - reserved_bytes = 800: the reading written for every GPU, 850, does not fit this one")
}

// The engine reads no devices[] pcie block, so GPUs that disagree there would
// lose their values silently; the generator stops instead.
func TestPropertyTheEngineCannotTakePerDeviceFails(t *testing.T) {
	t.Parallel()
	c, err := loadCapture(writeCapture(t, []map[string]any{
		{"GetMaxPcieLinkWidth": ok(16)},
		{"GetMaxPcieLinkWidth": ok(8)},
	}, nil))
	require.NoError(t, err)
	_, err = generate(c, nil)
	require.ErrorContains(t, err, "device_defaults.pcie.max_link_width: the GPUs disagree (16, 8) and the key has no per-device form")
}

// Fault counts and the link's current state are readings: a GPU that has had
// faults neither stops the generator nor gets devices[] entries of its own,
// and the profile takes the lower median, here the healthy GPUs' values.
func TestFaultCountsAreReadings(t *testing.T) {
	t.Parallel()
	gpu := func(replays, linkGen, remappedRows, eccErrors int) map[string]any {
		return map[string]any{
			"GetPcieReplayCounter":      ok(replays),
			"GetCurrPcieLinkGeneration": ok(linkGen),
			"GetRemappedRows":           ok([]any{remappedRows, 0, false, false}),
			"GetTotalEccErrors(0,0)":    ok(eccErrors),
		}
	}
	g, profile := generateFrom(t, writeCapture(t, []map[string]any{gpu(0, 6, 0, 0), gpu(3, 1, 1, 2), gpu(0, 6, 0, 0)}, nil), nil)
	for _, block := range []string{"pcie", "ecc", "remapped_rows"} {
		require.Nil(t, g.devices[1].find(block), "GPU 1 has a devices[] %s block", block)
	}
	for _, line := range []string{
		"replay_counter: 0 # GetPcieReplayCounter = 0 3 0, idle reading: lower median of the GPUs",
		"current_link_gen: 6 # GetCurrPcieLinkGeneration = 6 1 6, idle reading: lower median of the GPUs",
		"correctable: 0 # GetRemappedRows[0] = 0 1 0, idle reading: lower median of the GPUs",
		"total: 0 # GetTotalEccErrors(0,0) = 0 2 0, idle reading: lower median of the GPUs",
	} {
		require.Contains(t, profile, line)
	}
}

func TestUnsupportedGetterIsNotedNotInvented(t *testing.T) {
	t.Parallel()
	dir := writeCapture(t, []map[string]any{{
		"GetInforomImageVersion":       ok("G1"),
		"GetInforomVersion(2)":         failed("ERROR_NOT_SUPPORTED"),
		"GetFanSpeed":                  failed("ERROR_NOT_SUPPORTED"),
		"GetRetiredPagesPendingStatus": failed("ERROR_NOT_SUPPORTED"),
	}}, nil)
	_, profile := generateFrom(t, dir, nil)
	require.Contains(t, profile, "# pwr_object: left out, GetInforomVersion(2) = ERROR_NOT_SUPPORTED")
	require.Contains(t, profile, `speed_percent: "N/A" # GetFanSpeed = ERROR_NOT_SUPPORTED`)
	require.NotContains(t, profile, "retired_pages:", "a block with no answers is a comment, not an empty mapping")
	// A call the dump did not record is not the driver saying "not supported".
	require.Contains(t, profile, "# target_speed_percent: left out, GetTargetFanSpeed(0) = not recorded")
}

const baseYAML = `
device_defaults:
  pci:
    device_id: 0x304110DE
  thermal:
    temperature_gpu_c: 32
  fabric:
    state: "auto"
  features:
    fp8_support: true
devices:
  - index: 0
    uuid: "GPU-aaaaaaaa-0000-0000-0000-000000000000"
`

// rubinPCI is GetPciInfo of the GPU baseYAML is a profile of.
var rubinPCI = ok(map[string]any{"PciDeviceId": 0x304110DE})

func loadTestBase(t *testing.T, text string) *baseProfile {
	t.Helper()
	path := filepath.Join(t.TempDir(), "base.yaml")
	require.NoError(t, os.WriteFile(path, []byte(text), 0o644))
	base, err := loadBase(path)
	require.NoError(t, err)
	return base
}

func TestBaseSuppliesIdentifiersAndMockBlocks(t *testing.T) {
	t.Parallel()
	base := loadTestBase(t, baseYAML)
	dir := writeCapture(t, []map[string]any{{
		"GetPciInfo":        rubinPCI,
		"GetTemperature(0)": ok(41),
		"GetUUID":           ok("GPU-00000000-0000-0000-0000-000000000000"),
		"GetGpuFabricInfo":  ok(map[string]any{"State": 3, "CliqueId": 7}),
	}}, nil)
	_, profile := generateFrom(t, dir, base)
	require.Equal(t, `state: "auto" # from -base`, lineOf(t, profile, "state"))
	require.Contains(t, profile, `uuid: "GPU-aaaaaaaa-0000-0000-0000-000000000000" # from -base; scrubbed in the capture`)
	require.Contains(t, profile, "fp8_support: true")
	// Properties of the board and its readings always come from the capture.
	require.Equal(t, "clique_id: 7 # GetGpuFabricInfo.CliqueId", lineOf(t, profile, "clique_id"))
	require.Equal(t, "temperature_gpu_c: 41 # GetTemperature(0) on GPU 0, idle reading", lineOf(t, profile, "temperature_gpu_c"))
}

// A base of another GPU would hand its features and NVSwitches to this one.
func TestBaseOfAnotherGPUIsRefused(t *testing.T) {
	t.Parallel()
	c, err := loadCapture(writeCapture(t, []map[string]any{{"GetPciInfo": rubinPCI}}, nil))
	require.NoError(t, err)

	_, err = generate(c, loadTestBase(t, strings.Replace(baseYAML, "0x304110DE", "0x31C210DE", 1)))
	require.ErrorContains(t, err, "is a profile of PCI device 0x31C210DE, but GPU 0 of the capture is 0x304110DE")

	_, err = generate(c, loadTestBase(t, "device_defaults:\n  features:\n    fp8_support: true\n"))
	require.ErrorContains(t, err, "has no device_defaults.pci.device_id")
}

// The CPU an NVLink-C2C board carries is described per CPU from the host's
// lscpu and NUMA nodes; memory-only NUMA nodes do not count.
func TestCPUComesFromTheHost(t *testing.T) {
	t.Parallel()
	lscpu := "Vendor ID:                               NVIDIA\n" +
		"BIOS Model name:                         Vera A1Z 699-2G558-0201-TS5 CPU @ 3.3GHz\n" +
		"Core(s) per socket:                      88\n" +
		"Socket(s):                               2\n"
	numactl := "available: 3 nodes (0-2)\nnode 0 cpus: 0 1\nnode 0 size: 732098 MB\nnode 0 free: 1 MB\n" +
		"node 1 cpus: 2 3\nnode 1 size: 729051 MB\nnode 1 free: 1 MB\nnode 2 cpus:\nnode 2 size: 0 MB\nnode 2 free: 0 MB\n"
	smiQ := func(c2c string) string {
		return "<nvidia_smi_log><gpu><c2c_mode>" + c2c + "</c2c_mode></gpu></nvidia_smi_log>"
	}
	files := map[string]string{lscpuFile: lscpu, numactlFile: numactl, smiXMLFile: smiQ("Enabled")}
	_, profile := generateFrom(t, writeCapture(t, []map[string]any{{}}, files), nil)
	require.Equal(t, `type: "vera" # host-lscpu.txt BIOS Model name = Vera A1Z 699-2G558-0201-TS5 CPU @ 3.3GHz: the family is its first word`,
		lineOf(t, profile, "type"))
	require.Equal(t, "cores: 88 # host-lscpu.txt Core(s) per socket, of 2 sockets", lineOf(t, profile, "cores"))
	require.Equal(t, "memory_gb: 715 # host-numactl.txt node 0 size, the first CPU's: 732098 MiB = 715 GiB; node 1 729051 MiB",
		lineOf(t, profile, "memory_gb"))
	require.Contains(t, profile, "coherent_memory: true")

	files[smiXMLFile] = smiQ("Disabled")
	_, profile = generateFrom(t, writeCapture(t, []map[string]any{{}}, files), nil)
	require.Contains(t, profile, "# device_defaults.cpu: left out, the GPUs reach the CPU over PCIe (smi-q.xml c2c_mode), so it is not part of the board")
}

// Free memory is the total less the reserved and used memory written, so the
// mock's answers add up even when the used reading is one GPU's median.
func TestFreeMemoryIsWhatTheOtherReadingsLeave(t *testing.T) {
	t.Parallel()
	gpu := func(used, bar1Used int) map[string]any {
		return map[string]any{
			"GetMemoryInfo_v2":  ok(map[string]any{"Total": 1000, "Reserved": 100, "Used": used, "Free": 900 - used}),
			"GetBAR1MemoryInfo": ok(map[string]any{"Bar1Total": 500, "Bar1Used": bar1Used, "Bar1Free": 500 - bar1Used}),
		}
	}
	dir := writeCapture(t, []map[string]any{gpu(10, 1), gpu(30, 2), gpu(20, 3)}, nil)
	_, profile := generateFrom(t, dir, nil)
	require.Equal(t, "used_bytes: 20 # GetMemoryInfo_v2.Used = 10 30 20, idle reading: lower median of the GPUs",
		lineOf(t, profile, "used_bytes"))
	require.Contains(t, profile,
		"free_bytes: 880 # total_bytes - reserved_bytes - used_bytes = 1000 - 100 - 20; GetMemoryInfo_v2.Free reads 890 870 880")
	require.Contains(t, profile,
		"free_bytes: 498 # total_bytes - used_bytes = 500 - 2; GetBAR1MemoryInfo.Bar1Free reads 499 498 497")
	// Without a total there is nothing to take free memory from.
	_, profile = generateFrom(t, writeCapture(t, []map[string]any{{}}, nil), nil)
	require.Contains(t, profile, "# device_defaults.memory.free_bytes: left out, total_bytes has no value for GPU 0 to take it from")
}

func TestWithoutBaseTheCaptureReadingAndPlaceholderAreUsed(t *testing.T) {
	t.Parallel()
	dir := writeCapture(t, []map[string]any{{
		"GetTemperature(0)": ok(41),
		"GetUUID":           ok("GPU-00000000-0000-0000-0000-000000000000"),
	}}, nil)
	_, profile := generateFrom(t, dir, nil)
	require.Equal(t, "temperature_gpu_c: 41 # GetTemperature(0) on GPU 0, idle reading", lineOf(t, profile, "temperature_gpu_c"))
	require.Contains(t, profile, `uuid: "GPU-00000000-0000-0000-0000-000000000000" # placeholder: GetUUID`)
	require.Contains(t, profile, "# features: left out, marketing capabilities no getter reports; pass -base to copy it")
}

func TestNVLinkVersionIsTheGenerationNotNVMLsEnum(t *testing.T) {
	t.Parallel()
	gpu := map[string]any{}
	for link := range 2 {
		gpu[call("GetNvLinkVersion", link)] = ok(8)
		gpu[call("GetNvLinkState", link)] = ok(1)
	}
	gpu[call("GetNvLinkVersion", 2)] = failed("ERROR_INVALID_ARGUMENT")
	gpu[call("GetNvLinkState", 2)] = failed("ERROR_INVALID_ARGUMENT")
	_, profile := generateFrom(t, writeCapture(t, []map[string]any{gpu}, nil), nil)
	require.Contains(t, profile, "version: 6 # GetNvLinkVersion(link) = 8 on all 2 links (NVML's enum; the key is the generation)")
	require.Contains(t, profile, "links_per_gpu: 2")

	for nvmlVersion, generation := range map[int64]string{1: "1", 3: "2", 5: "3", 6: "4", 7: "5", 8: "6"} {
		got, known := nvlinkGeneration(json.Number(itoa(int(nvmlVersion))))
		require.True(t, known)
		require.Equal(t, generation, got)
	}
}

// The supported PowerMizer modes are a mask with one bit per mode value; the
// VR NVL72 tray answers 7.
func TestSupportedPowerMizerModesNameTheMasksBits(t *testing.T) {
	t.Parallel()
	modes := namedBits(powerMizerModes)
	got, ok := modes(json.Number("7"))
	require.True(t, ok)
	require.Equal(t, `["adaptive", "prefer_maximum_performance", "auto"]`, got)
	_, ok = modes(json.Number("16"))
	require.False(t, ok, "bit 4 is no mode NVML defines")
	_, ok = modes(json.Number("0"))
	require.False(t, ok, "a board that accepts no mode has no profile spelling")
}

func TestFieldValuesDecodeByValueType(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		typ  int64
		hex  string
		want string
	}{
		{5, "fbffffff00000000", "-5"},         // signed int
		{1, "fbffffff00000000", "4294967291"}, // unsigned int
		{3, "0100000000000000", "1"},          // unsigned long long
		{0, "000000000000f83f", "1.5"},        // double
	} {
		got, ok := decodeField(tc.typ, tc.hex)
		require.True(t, ok)
		require.Equal(t, tc.want, string(got))
	}
	_, ok := decodeField(5, "fbff")
	require.False(t, ok)
}

// nic is one RDMA device of a test capture: its type, its PCI function, and
// the physical function it belongs to when it is a virtual one.
type nic struct {
	name, hcaType, bus, physfn string
	rate                       int
}

// nicFiles writes the files a capture describes its RDMA devices with.
func nicFiles(nics ...nic) map[string]string {
	var sysfs, rdma, functions, ibstat strings.Builder
	for _, n := range nics {
		fmt.Fprintf(&sysfs, "== /sys/class/infiniband/%s\nfw_ver=1.0\nhca_type=%s\nboard_id=MT_%s\n"+
			"port 1 state=4: ACTIVE phys=5: LinkUp rate=%d Gb/sec (1X XDR) layer=Ethernet\n", n.name, n.hcaType, n.hcaType, n.rate)
		fmt.Fprintf(&rdma, "== %s\ndevpath=/sys/devices/pci0001:00/0001:00:00.0/%s\n", n.name, n.bus)
		physfn := ""
		if n.physfn != "" {
			physfn = "../" + n.physfn
		}
		fmt.Fprintf(&functions, "%s subsys=0x15b3:0x0671 sriov_totalvfs=cat: '/sys/bus/pci/devices/%s/sriov_totalvfs': No such file or directory physfn=%s virtfn=0\n", n.bus, n.bus, physfn)
		fmt.Fprintf(&ibstat, "CA '%s'\n\tHardware version: 0\n", n.name)
	}
	return map[string]string{ibSysfsFile: sysfs.String(), rdmaSysfsFile: rdma.String(), nicFunctions: functions.String(), ibstatFile: ibstat.String()}
}

func TestNICsTakeTheMostCommonType(t *testing.T) {
	t.Parallel()
	dir := writeCapture(t, []map[string]any{{}}, nicFiles(
		nic{name: "mlx5_0", hcaType: "MT4133", bus: "0001:03:00.0", rate: 200},
		nic{name: "mlx5_1", hcaType: "MT41695", bus: "0005:41:00.0", rate: 400},
		nic{name: "mlx5_2", hcaType: "MT4133", bus: "0003:01:00.0", rate: 200},
	))
	_, profile := generateFrom(t, dir, nil)
	require.Contains(t, profile, `enabled: true # 3 RDMA devices in host-ib-sysfs.txt; no virtual functions in pci-nic-functions.txt`)
	require.Contains(t, profile, `hca_type: "MT4133" # host-ib-sysfs.txt hca_type of 2 devices of 3; the rest: MT41695 x1`)
	require.Contains(t, profile, `rate_gbps: 200`)
	require.Contains(t, profile, `hw_rev: "0"`)
	require.Contains(t, profile, `hcas_per_gpu: 3`)
}

// SR-IOV virtual functions are slices of NICs already counted: they neither
// count as NICs nor outvote them, even when they outnumber them.
func TestNICsLeaveOutVirtualFunctions(t *testing.T) {
	t.Parallel()
	dir := writeCapture(t, []map[string]any{{}}, nicFiles(
		nic{name: "mlx5_0", hcaType: "MT4133", bus: "0001:03:00.0", rate: 200},
		nic{name: "mlx5_1", hcaType: "MT4133", bus: "0003:01:00.0", rate: 200},
		nic{name: "mlx5_2", hcaType: "MT41695", bus: "0005:41:00.0", rate: 400},
		nic{name: "mlx5_3", hcaType: "MT4126", bus: "0001:03:00.1", physfn: "0001:03:00.0", rate: 200},
		nic{name: "mlx5_4", hcaType: "MT4126", bus: "0001:03:00.2", physfn: "0001:03:00.0", rate: 200},
		nic{name: "mlx5_5", hcaType: "MT4126", bus: "0003:01:00.1", physfn: "0003:01:00.0", rate: 200},
	))
	_, profile := generateFrom(t, dir, nil)
	require.Contains(t, profile, "enabled: true # 3 RDMA devices in host-ib-sysfs.txt, not counting 3 virtual functions (physfn in pci-nic-functions.txt)")
	require.Contains(t, profile, `hca_type: "MT4133" # host-ib-sysfs.txt hca_type of 2 devices of 3; the rest: MT41695 x1`)
	require.NotContains(t, profile, "MT4126")
	require.Contains(t, profile, "hcas_per_gpu: 3")
}

// A node whose RDMA devices are all virtual functions shows no NIC to model.
func TestNICsThatAreAllVirtualFunctionsAreLeftOut(t *testing.T) {
	t.Parallel()
	dir := writeCapture(t, []map[string]any{{}}, nicFiles(
		nic{name: "mlx5_0", hcaType: "MT4126", bus: "0001:03:00.1", physfn: "0001:03:00.0", rate: 200},
		nic{name: "mlx5_1", hcaType: "MT4126", bus: "0001:03:00.2", physfn: "0001:03:00.0", rate: 200},
	))
	_, profile := generateFrom(t, dir, nil)
	require.Contains(t, profile, "# infiniband: left out: all 2 RDMA devices in host-ib-sysfs.txt are virtual functions (physfn in pci-nic-functions.txt), so the capture shows no NIC")
	require.NotContains(t, profile, "hca_type")
}

func TestNICsTiedForMostCommonTypeFail(t *testing.T) {
	t.Parallel()
	dir := writeCapture(t, []map[string]any{{}}, nicFiles(
		nic{name: "mlx5_0", hcaType: "MT4133", bus: "0001:03:00.0", rate: 200},
		nic{name: "mlx5_1", hcaType: "MT41695", bus: "0005:41:00.0", rate: 400},
	))
	c, err := loadCapture(dir)
	require.NoError(t, err)
	_, err = generate(c, nil)
	require.ErrorContains(t, err, `infiniband.hca_type: "MT4133" and "MT41695" tie with 1 device each`)
}

func TestNICsWithoutFunctionsAreLeftOut(t *testing.T) {
	t.Parallel()
	files := nicFiles(nic{name: "mlx5_0", hcaType: "MT4133", bus: "0001:03:00.0", rate: 200})
	delete(files, nicFunctions)
	_, profile := generateFrom(t, writeCapture(t, []map[string]any{{}}, files), nil)
	require.Contains(t, profile, "# infiniband: left out: pci-nic-functions.txt not captured, so virtual functions cannot be told from NICs")
	require.NotContains(t, profile, "hca_type")
}

func TestHostParsers(t *testing.T) {
	t.Parallel()
	roots := pciRoots("/sys/devices/pci0002:80\n/sys/devices/pci0002:80/0002:80:00.0/0002:81:00.0\n")
	require.Equal(t, "pci0002:80", roots["0002:81:00.0"])

	numa := pciNUMA("/sys/bus/pci/devices/000a:81:00.0 device=0x3041 numa=1 link=64.0 GT/s PCIex2\n")
	require.Equal(t, 1, numa["000a:81:00.0"])

	bus := rdmaBusIDs("== mlx5_0\nnode_type=1: CA\ndevpath=/sys/devices/pci0001:00/0001:00:00.0/0001:03:00.1\n-- port 1\n")
	require.Equal(t, map[string]string{"mlx5_0": "0001:03:00.1"}, bus)

	vfs := virtualFunctions("0001:03:00.0 subsys=0x15b3:0x0671 sriov_totalvfs=1 sriov_numvfs=1 physfn= virtfn=1\n" +
		"0001:03:00.1 subsys=0x15b3:0x0671 sriov_totalvfs=cat: 'x': No such file or directory physfn=../0001:03:00.0 virtfn=0\n")
	require.Equal(t, map[string]bool{"0001:03:00.0": false, "0001:03:00.1": true}, vfs)

	placements := migPlacementsByProfile("GPU  0 Profile ID 19 Placements: {0,1,2}:1\nGPU  0 Profile ID  0 Placement : {0}:8\nGPU  1 Profile ID 19 Placements: {0}:1\n")
	require.Equal(t, migPlacement{starts: []int{0, 1, 2}, size: 1}, placements[19])
	require.Equal(t, migPlacement{starts: []int{0}, size: 8}, placements[0])

	names := migNames("|   0  MIG 1g.35gb         19     7/7        33.50      No     20     1     0   |\n|   1  MIG 1g.35gb         19     7/7  |\n")
	require.Equal(t, []migName{{id: 19, name: "1g.35gb"}}, names)

	speeds := nvlinkSpeedsByGPU("GPU 0: x (UUID: y)\n\t Link 0: 41 GB/s\n\t Link 1: 41 GB/s\nGPU 1: x\n\t Link 0: <inactive>\n")
	require.Equal(t, []string{"41 GB/s", "41 GB/s"}, speeds[0])
	require.Equal(t, []string{"<inactive>"}, speeds[1])
}

func TestRun(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	require.Equal(t, 2, Run(nil, &bytes.Buffer{}, &stderr))
	require.Contains(t, stderr.String(), "usage: mokka-capture profile")
	require.Equal(t, 1, Run([]string{t.TempDir()}, &bytes.Buffer{}, &bytes.Buffer{}), "a directory without a dump")

	capture := goldens[0].capture
	var stdout bytes.Buffer
	stderr.Reset()
	require.Equal(t, 0, Run([]string{capture}, &stdout, &stderr))
	require.Contains(t, stdout.String(), "version: \"1.0\"")
	require.Contains(t, stderr.String(), "pass -o to write the table")

	out := filepath.Join(t.TempDir(), "mock-nvml-config-x.yaml")
	require.Equal(t, 0, Run([]string{"-o", out, capture}, &bytes.Buffer{}, &bytes.Buffer{}))
	require.FileExists(t, out)
	require.FileExists(t, filepath.Join(filepath.Dir(out), "mock-nvml-config-x.mig.yaml"))

	stdout.Reset()
	require.Equal(t, 0, Run([]string{"-explain", capture}, &stdout, &bytes.Buffer{}))
	require.Regexp(t, `device_defaults\.thermal\.max_operating_c\s+90\s+from the T\.Limit reference and offsets\s+tlimit\.reference_c - tlimit\.slowdown_c = 88 - \(-2\); GetTemperatureThreshold\(3\) reads 90 on every GPU`, stdout.String())

	stderr.Reset()
	gb300 := "../../../pkg/gpu/mocknvml/configs/mock-nvml-config-gb300.yaml"
	require.Equal(t, 1, Run([]string{"-base", gb300, capture}, &bytes.Buffer{}, &stderr), "a base of another GPU")
	require.Contains(t, stderr.String(), "is a profile of PCI device 0x31C210DE, but GPU 0 of the capture is 0x304110DE")
}

// The mock loads the MIG table beside a profile, so a table an earlier run
// left there would give this board another board's partitions.
func TestRunRemovesAStaleMIGTable(t *testing.T) {
	t.Parallel()
	dir := writeCapture(t, []map[string]any{{"GetPciInfo": rubinPCI}}, nil)
	out := filepath.Join(t.TempDir(), "mock-nvml-config-x.yaml")
	stale := migSibling(out)
	require.NoError(t, os.WriteFile(stale, []byte("version: 1\n"), 0o644))
	var stderr bytes.Buffer
	require.Equal(t, 0, Run([]string{"-o", out, dir}, &bytes.Buffer{}, &stderr))
	require.FileExists(t, out)
	require.NoFileExists(t, stale)
	require.Contains(t, stderr.String(), "removed "+stale)
}

// A -base block is copied whole, in whatever YAML style it is written.
func TestBaseBlocksAreCopiedWhole(t *testing.T) {
	t.Parallel()
	const base = `
device_defaults:
  pci:
    device_id: 0x304110DE
  dynamic_metrics: {utilization: {pattern: steady, gpu_min: 10}, power: {series: [1, 2]}}
  features:
    mixed:
      - plain
      - [1, 2]
      - {}
      - name: a
        size: 2
`
	c, err := loadCapture(writeCapture(t, []map[string]any{{"GetPciInfo": rubinPCI}}, nil))
	require.NoError(t, err)
	g, err := generate(c, loadTestBase(t, base))
	require.NoError(t, err)
	profile, _ := g.documents("p.yaml")
	var got, want map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(profile), &got))
	require.NoError(t, yaml.Unmarshal([]byte(base), &want))
	for _, key := range []string{"dynamic_metrics", "features"} {
		require.Equal(t, want["device_defaults"].(map[string]any)[key], got["device_defaults"].(map[string]any)[key], key)
	}
}
