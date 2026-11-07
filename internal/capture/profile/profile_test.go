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

package profile

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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

func TestTLimitReferenceIsTemperaturePlusMarginPerGPU(t *testing.T) {
	t.Parallel()
	gpu := func(temp, margin int) map[string]any {
		return map[string]any{
			"GetTemperature(0)":    ok(temp),
			"GetMarginTemperature": ok(map[string]any{"MarginTemperature": margin}),
		}
	}
	// Sums 87, 87, 86, 87: rounding moves one GPU down a degree.
	dir := writeCapture(t, []map[string]any{gpu(34, 53), gpu(33, 54), gpu(33, 53), gpu(34, 53)}, nil)
	_, profile := generateFrom(t, dir, nil)
	require.Equal(t,
		"reference_c: 87 # GetTemperature(0)+GetMarginTemperature.MarginTemperature per GPU: 34+53=87 33+54=87 33+53=86 34+53=87; lower median of the GPUs",
		lineOf(t, profile, "reference_c"))
	// The temperature itself is a load reading.
	require.Equal(t,
		"temperature_gpu_c: 33 # GetTemperature(0) = 34 33 33 34, idle reading: lower median of the GPUs",
		lineOf(t, profile, "temperature_gpu_c"))
}

func TestTLimitReferenceIsLeftOutWithoutAMargin(t *testing.T) {
	t.Parallel()
	dir := writeCapture(t, []map[string]any{{"GetTemperature(0)": ok(34), "GetMarginTemperature": failed("ERROR_NOT_SUPPORTED")}}, nil)
	_, profile := generateFrom(t, dir, nil)
	require.Contains(t, profile, "# tlimit.reference_c: left out, GetTemperature(0)+GetMarginTemperature.MarginTemperature = GetMarginTemperature = ERROR_NOT_SUPPORTED")
}

func TestPropertyThatDiffersPerGPUIsSetPerDevice(t *testing.T) {
	t.Parallel()
	dir := writeCapture(t, []map[string]any{
		{"GetMaxPcieLinkWidth": ok(16), "GetMemoryBusWidth": ok(5120)},
		{"GetMaxPcieLinkWidth": ok(8), "GetMemoryBusWidth": ok(5120)},
	}, nil)
	g, profile := generateFrom(t, dir, nil)
	require.Contains(t, profile, "memory_bus_width: 5120")
	require.Contains(t, profile, "# pcie.max_link_width: differs per GPU, set under devices: GetMaxPcieLinkWidth")
	require.Equal(t, scalar{"16"}, g.devices[0].find("pcie").value.(*mapping).find("max_link_width").value)
	require.Equal(t, scalar{"8"}, g.devices[1].find("pcie").value.(*mapping).find("max_link_width").value)
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

func TestBaseSuppliesReadingsIdentifiersAndMockBlocks(t *testing.T) {
	t.Parallel()
	basePath := filepath.Join(t.TempDir(), "base.yaml")
	require.NoError(t, os.WriteFile(basePath, []byte(baseYAML), 0o644))
	base, err := loadBase(basePath)
	require.NoError(t, err)
	dir := writeCapture(t, []map[string]any{{
		"GetTemperature(0)": ok(41),
		"GetUUID":           ok("GPU-00000000-0000-0000-0000-000000000000"),
		"GetGpuFabricInfo":  ok(map[string]any{"State": 3, "CliqueId": 7}),
	}}, nil)
	_, profile := generateFrom(t, dir, base)
	require.Equal(t, "temperature_gpu_c: 32 # from -base; load reading, not in the capture", lineOf(t, profile, "temperature_gpu_c"))
	require.Equal(t, `state: "auto" # from -base`, lineOf(t, profile, "state"))
	require.Contains(t, profile, `uuid: "GPU-aaaaaaaa-0000-0000-0000-000000000000" # from -base; scrubbed in the capture`)
	require.Contains(t, profile, "fp8_support: true")
	// Properties of the board always come from the capture.
	require.Equal(t, "clique_id: 7 # GetGpuFabricInfo.CliqueId", lineOf(t, profile, "clique_id"))
}

func TestWithoutBaseTheCaptureReadingAndPlaceholderAreUsed(t *testing.T) {
	t.Parallel()
	dir := writeCapture(t, []map[string]any{{
		"GetTemperature(0)": ok(41),
		"GetUUID":           ok("GPU-00000000-0000-0000-0000-000000000000"),
	}}, nil)
	_, profile := generateFrom(t, dir, nil)
	require.Equal(t, "temperature_gpu_c: 41 # GetTemperature(0), idle reading", lineOf(t, profile, "temperature_gpu_c"))
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

func TestNICsTakeTheMostCommonType(t *testing.T) {
	t.Parallel()
	sysfs := `== /sys/class/infiniband/mlx5_0
fw_ver=1.0
hca_type=MT4133
board_id=MT_1
port 1 state=4: ACTIVE phys=5: LinkUp rate=200 Gb/sec (1X XDR) layer=Ethernet
== /sys/class/infiniband/mlx5_1
fw_ver=2.0
hca_type=MT41695
board_id=MT_2
port 1 state=4: ACTIVE phys=5: LinkUp rate=400 Gb/sec (4X NDR) layer=Ethernet
== /sys/class/infiniband/mlx5_2
fw_ver=1.0
hca_type=MT4133
board_id=MT_1
port 1 state=4: ACTIVE phys=5: LinkUp rate=200 Gb/sec (1X XDR) layer=Ethernet
`
	ibstat := "CA 'mlx5_0'\n\tHardware version: 0\nCA 'mlx5_2'\n\tHardware version: 0\n"
	dir := writeCapture(t, []map[string]any{{}}, map[string]string{ibSysfsFile: sysfs, ibstatFile: ibstat})
	_, profile := generateFrom(t, dir, nil)
	require.Contains(t, profile, `hca_type: "MT4133" # host-ib-sysfs.txt hca_type of 2 devices of 3; the rest: MT41695 x1`)
	require.Contains(t, profile, `rate_gbps: 200`)
	require.Contains(t, profile, `hw_rev: "0"`)
	require.Contains(t, profile, `hcas_per_gpu: 3`)
}

func TestHostParsers(t *testing.T) {
	t.Parallel()
	roots := pciRoots("/sys/devices/pci0002:80\n/sys/devices/pci0002:80/0002:80:00.0/0002:81:00.0\n")
	require.Equal(t, "pci0002:80", roots["0002:81:00.0"])

	numa := pciNUMA("/sys/bus/pci/devices/000a:81:00.0 device=0x3041 numa=1 link=64.0 GT/s PCIex2\n")
	require.Equal(t, 1, numa["000a:81:00.0"])

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
	require.Regexp(t, `device_defaults\.thermal\.tlimit\.reference_c\s+87\s+lower median of the GPUs\s+GetTemperature\(0\)\+GetMarginTemperature\.MarginTemperature\s+34\+53=87 \| 33\+54=87 \| 33\+53=86 \| 34\+53=87`, stdout.String())
}
