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

package scrub

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGPUUUIDsKeepOnePlaceholderAcrossText(t *testing.T) {
	s := newScrubber(nil)
	out := s.scrub("GPU 0: x (UUID: GPU-f662ab51-7dd5-545c-be52-5aa9606316be)\n" +
		"k8s.device-plugin.nvidia.com/gpu=GPU-f662ab51-7dd5-545c-be52-5aa9606316be\n" +
		"GPU-e88c50aa-7e38-5b26-8005-fabdba9d7fec\n")

	require.Equal(t, "GPU 0: x (UUID: GPU-00000000-0000-0000-0000-000000000000)\n"+
		"k8s.device-plugin.nvidia.com/gpu=GPU-00000000-0000-0000-0000-000000000000\n"+
		"GPU-00000001-0000-0000-0000-000000000001\n", out)
}

func TestSeedNumbersGPUsInFileOrder(t *testing.T) {
	s := newScrubber(nil)
	s.seedGPUs("GPU 0: (UUID: GPU-aaaaaaaa-0000-0000-0000-000000000001)\nGPU 1: (UUID: GPU-bbbbbbbb-0000-0000-0000-000000000002)\n")

	// Seen in the other order elsewhere, the placeholders still follow the index.
	out := s.scrub("GPU-bbbbbbbb-0000-0000-0000-000000000002 GPU-aaaaaaaa-0000-0000-0000-000000000001")
	require.Equal(t, "GPU-00000001-0000-0000-0000-000000000001 GPU-00000000-0000-0000-0000-000000000000", out)
}

func TestMACAndDerivedInterfaceNameShareAPlaceholder(t *testing.T) {
	s := newScrubber(nil)
	out := s.scrub("2: enx742554731960: link/ether 74:25:54:73:19:60 brd ff:ff:ff:ff:ff:ff")

	require.Equal(t, "2: enx020000000001: link/ether 02:00:00:00:00:01 brd ff:ff:ff:ff:ff:ff", out)
}

func TestGUIDForms(t *testing.T) {
	s := newScrubber(nil)
	out := s.scrub("node_guid cc30:8903:00cb:be0e\nport 0xcc30890300cbbe0e\nsys 0xCC30890300CBBE0E\n" +
		"other 0xce3089fffecbbe0e\ngid fe80:0000:0000:0000:ce30:89ff:fecb:be0e\nsubnet_prefix fe80:0000:0000:0000\n")

	want := "node_guid 0000:0000:0000:0001\nport 0x0000000000000001\nsys 0x0000000000000001\n" +
		"other 0x0000000000000002\ngid fe80:0000:0000:0000:0000:0000:0000:0001\nsubnet_prefix fe80:0000:0000:0000\n"
	require.Equal(t, want, out, "one ID keeps one number in every notation; a link-local prefix is not an identifier")
}

func TestSmallHexValuesStay(t *testing.T) {
	in := "clocks_event_reasons.supported: 0x00000000000007ff,0x00000000000007ff\n" +
		"clocks_event_reasons.active: 0x0000000000000001\n" +
		"ACPI: SSDT 0x00000000FFFF0000 000725\n" +
		"kernel 0xffff800080000000\n" +
		"timestamp_mask 0x7fffffffffffffff\n" +
		"<pdi>0x00000000ffffffff</pdi>\n" +
		"pdi 0x0000000100000000\n"
	want := "clocks_event_reasons.supported: 0x00000000000007ff,0x00000000000007ff\n" +
		"clocks_event_reasons.active: 0x0000000000000001\n" +
		"ACPI: SSDT 0x00000000FFFF0000 000725\n" +
		"kernel 0xffff800080000000\n" +
		"timestamp_mask 0x7fffffffffffffff\n" +
		"<pdi>0x00000000ffffffff</pdi>\n" +
		"pdi 0x0000000000000001\n"
	require.Equal(t, want, newScrubber(nil).scrub(in), "values below 2^32, all-ones masks and kernel addresses stay; 2^32 is replaced")
}

func TestIPv4LeavesVersionsMasksAndLoopback(t *testing.T) {
	s := newScrubber(nil)
	out := s.scrub("addr 10.71.140.107 and 10.71.140.99 and 10.71.140.107\n" +
		"vbios 99.00.16.55.13 driver 615.71.09 mask 255.255.255.0 lo 127.0.0.1 any 0.0.0.0\n" +
		"masks 255.255.252.0 128.0.0.0 255.255.255.255\n" +
		"node 128.128.128.128 and 255.0.255.0 and 192.168.0.1\n")

	require.Equal(t, "addr 192.0.2.1 and 192.0.2.2 and 192.0.2.1\n"+
		"vbios 99.00.16.55.13 driver 615.71.09 mask 255.255.255.0 lo 127.0.0.1 any 0.0.0.0\n"+
		"masks 255.255.252.0 128.0.0.0 255.255.255.255\n"+
		"node 192.0.2.3 and 192.0.2.4 and 192.0.2.5\n", out,
		"only a run of ones then zeros is a netmask")
}

func TestSerialsInXMLAndTextAndAbsentValuesStay(t *testing.T) {
	s := newScrubber(nil)
	out := s.scrub("<serial>1234567890123</serial><serial>N/A</serial><pdi>0xabcdef0011223344</pdi>" +
		"<gpu_fabric_guid>0xabcdef0011223344</gpu_fabric_guid><chassis_serial_number>9999</chassis_serial_number>\n" +
		"    Serial Number                                      : 1234567890123\n" +
		"    Serial Number                                      : N/A\n")

	require.Contains(t, out, "<serial>1650000000001</serial><serial>N/A</serial>")
	require.Contains(t, out, "<pdi>0x0000000000000001</pdi><gpu_fabric_guid>0x0000000000000001</gpu_fabric_guid>",
		"one value gets one placeholder")
	require.Contains(t, out, "<chassis_serial_number>1820000000001</chassis_serial_number>")
	require.Contains(t, out, ": 1650000000001\n")
	require.Contains(t, out, ": N/A\n")
}

func TestUUIDsAndMachineIDs(t *testing.T) {
	s := newScrubber(nil)
	out := s.scrub("uid: 52751ea7-b651-4cce-b2af-e41e06ee9621 again 52751ea7-b651-4cce-b2af-e41e06ee9621 " +
		"machine 968e60b5fb53f1118000cc3089cbbe0e zero 00000000-0000-0000-0000-000000000000")

	require.Contains(t, out, "uid: 00000001-0000-0000-0000-000000000001 again 00000001-0000-0000-0000-000000000001")
	require.Contains(t, out, "machine 00000000000000000000000000000001")
	require.Contains(t, out, "zero 00000000-0000-0000-0000-000000000000")
}

func TestLiteralReplacementsLongestFirst(t *testing.T) {
	s := newScrubber([][2]string{{"tenant-1", "cluster"}, {"tenant-1-w-8825", "node-a"}})
	require.Equal(t, "node-a on cluster", s.scrub("tenant-1-w-8825 on tenant-1"))
}

func TestScrubbingItsOwnOutputChangesNothing(t *testing.T) {
	s := newScrubber(nil)
	once := s.scrub("GPU-f662ab51-7dd5-545c-be52-5aa9606316be uid 52751ea7-b651-4cce-b2af-e41e06ee9621 " +
		"mac 74:25:54:73:19:60 ip 10.1.2.3 guid cc30:8903:00cb:be0e 0xce3089fffecbbe0e " +
		"gid fe80:0000:0000:0000:ce30:89ff:fecb:be0e m 968e60b5fb53f1118000cc3089cbbe0e")

	want := "GPU-00000000-0000-0000-0000-000000000000 uid 00000001-0000-0000-0000-000000000001 " +
		"mac 02:00:00:00:00:01 ip 192.0.2.1 guid 0000:0000:0000:0001 0x0000000000000002 " +
		"gid fe80:0000:0000:0000:0000:0000:0000:0001 m 00000000000000000000000000000001"
	require.Equal(t, want, once)
	require.Equal(t, want, newScrubber(nil).scrub(once), "scrubbing the output again changes nothing")
}

func TestRunWritesACopySkipsBinariesAndExclusions(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "node-a"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "node-a", "smi-L.txt"),
		[]byte("GPU 0: x (UUID: GPU-f662ab51-7dd5-545c-be52-5aa9606316be)\nhost worker-7\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(src, "node-a", "errors.log"), []byte("secret worker-7"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(src, "node-a", "lib.so"), []byte("ELF"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(src, "node-a", "blob.bin"), []byte{0, 1, 2}, 0o600))

	dst := filepath.Join(t.TempDir(), "out")
	var stdout, stderr bytes.Buffer
	code := run([]string{"-out", dst, "-replace", "worker-7=node-a", "-seed", filepath.Join(src, "node-a", "smi-L.txt"),
		filepath.Join(src, "node-a")}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())

	got, err := os.ReadFile(filepath.Join(dst, "node-a", "smi-L.txt"))
	require.NoError(t, err)
	require.Equal(t, "GPU 0: x (UUID: GPU-00000000-0000-0000-0000-000000000000)\nhost node-a\n", string(got))

	for _, skipped := range []string{"errors.log", "lib.so", "blob.bin"} {
		require.NoFileExists(t, filepath.Join(dst, "node-a", skipped))
	}
}

func TestRunRefusesANonEmptyOutputDirectory(t *testing.T) {
	dst := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dst, "x"), nil, 0o600))

	var stdout, stderr bytes.Buffer
	require.Equal(t, 1, run([]string{"-out", dst, t.TempDir()}, &stdout, &stderr))
}

func TestRunRefusesAnOutputDirectoryInsideASource(t *testing.T) {
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "a.txt"), []byte("x\n"), 0o600))

	for _, out := range []string{src, filepath.Join(src, "scrubbed"), filepath.Join(src, "a", "b")} {
		var stdout, stderr bytes.Buffer
		require.Equal(t, 1, run([]string{"-out", out, t.TempDir(), src}, &stdout, &stderr), out)
		require.Equal(t, out+" is inside "+src+"; the walk would copy its own output\n", stderr.String())
		entries, err := os.ReadDir(src)
		require.NoError(t, err)
		require.Len(t, entries, 1, "nothing is written into the source")
	}
}

func TestRunRefusesTwoSourcesWithOneOutputDirectory(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"capture-a/node-a", "capture-b/node-a", "worker-7", "node-b"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, dir, "a.txt"), []byte("x\n"), 0o600))
	}
	cases := []struct {
		name string
		args []string
		dup  [2]string
	}{
		{"same name", []string{"capture-a/node-a", "capture-b/node-a"}, [2]string{"capture-a/node-a", "capture-b/node-a"}},
		{"same name after -replace", []string{"-replace", "worker-7=node-b", "worker-7", "node-b"}, [2]string{"worker-7", "node-b"}},
	}
	for _, tc := range cases {
		out := filepath.Join(t.TempDir(), "out")
		args := []string{"-out", out}
		for _, a := range tc.args {
			if strings.Contains(a, "/") || a == "worker-7" || a == "node-b" {
				a = filepath.Join(root, a)
			}
			args = append(args, a)
		}
		var stdout, stderr bytes.Buffer
		require.Equal(t, 1, run(args, &stdout, &stderr), tc.name)
		want := filepath.Join(root, tc.dup[0]) + " and " + filepath.Join(root, tc.dup[1]) +
			" would both be written to " + filepath.Join(out, filepath.Base(tc.dup[1])) + "\n"
		require.Equal(t, want, stderr.String(), tc.name)
		_, err := os.Stat(out)
		require.True(t, os.IsNotExist(err), "%s: nothing is written", tc.name)
	}
}

func TestRunAcceptsASiblingWithASourcesNameAsPrefix(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "raw")
	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "a.txt"), []byte("x\n"), 0o600))

	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, run([]string{"-out", filepath.Join(root, "raw-scrubbed"), src}, &stdout, &stderr), stderr.String())
	got, err := os.ReadFile(filepath.Join(root, "raw-scrubbed", "raw", "a.txt"))
	require.NoError(t, err)
	require.Equal(t, "x\n", string(got))
}

func TestLearnedSerialsAreNumberedInOrderOfAppearance(t *testing.T) {
	// The learned values live in a map; run often enough that a numbering that
	// followed map order would show.
	for range 50 {
		s := newScrubber(nil)
		s.learn("    Serial Number : 1642226000999\n    Serial Number : 1642226000111\n")
		require.Equal(t, "NVRM: 1650000000001 then 1650000000002 again 1650000000001",
			s.scrub("NVRM: 1642226000999 then 1642226000111 again 1642226000999"))
	}
}

func TestLearnedSerialsAreScrubbedWhereNothingNamesThem(t *testing.T) {
	s := newScrubber(nil)
	s.learn("    Serial Number                                      : 1642226000232\n" +
		"platform.chassis_serial_number: 1821526A74003,1821526A74003\n" +
		"\t\t\t[SN] Serial number: MT2619602QYD\n")

	out := s.scrub("NVRM: GPU Board Serial Number: 1642226000232\n" +
		`{"GetFoo":{"value":"1642226000232"}} chassis 1821526A74003 nic MT2619602QYD` + "\n")

	require.NotContains(t, out, "1642226000232")
	require.NotContains(t, out, "1821526A74003")
	require.NotContains(t, out, "MT2619602QYD")
	require.Contains(t, out, "chassis 1820000000001")
}

func TestVitalProductDataUnitFieldsAreScrubbedAndPartNumbersStay(t *testing.T) {
	s := newScrubber(nil)
	out := s.scrub("\t\t[V2] Vendor specific: 900-9X9BE-00PB-TS5\n" +
		"\t\t[VU] Vendor specific: 968e60b5fb53f1118000cc3089cbbe0eMLNXS0D0F0\n" +
		"\t\t[V3] Vendor specific: 968e60b5fb53f1118000cc3089cbbe0e\n")

	require.Contains(t, out, "900-9X9BE-00PB-TS5")
	require.NotContains(t, out, "968e60b5")
	require.Contains(t, out, "MLNXS0D0F0")
}

func TestKernelAddressesAreNotGUIDs(t *testing.T) {
	s := newScrubber(nil)
	in := "nvidia 17235968 76 gdrdrv, Live 0xffffb9e0692d0000 (OE)"
	require.Equal(t, in, s.scrub(in))
}

func TestScrubDumpRedactsByFieldNameAndKeepsGzip(t *testing.T) {
	raw := []byte(`{"devices":[{"index":0,"calls":{"GetPdi":{"ret":"SUCCESS","value":{"Version":1,"Value":16078824392434643224}}}}]}`)

	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, err := zw.Write(raw)
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	out, err := scrubDump(newScrubber(nil), "nvml-dump.json.gz", zipped.Bytes(), nil)
	require.NoError(t, err)
	zr, err := gzip.NewReader(bytes.NewReader(out))
	require.NoError(t, err, "a gzipped recording stays gzipped")
	plain, err := io.ReadAll(zr)
	require.NoError(t, err)
	require.NotContains(t, string(plain), "16078824392434643224")
}

func TestCompressedIPv6AddressesAreScrubbedInEveryNotation(t *testing.T) {
	s := newScrubber(nil)
	out := s.scrub("InternalIP fd00::1234\nsame fd00:0:0:0:0:0:0:1234\n" +
		"link fe80::ce30:89ff:fecb:be0e%eth0\nmapped ::ffff:10.0.0.7\nprefix 2001:db8:42::/64\n" +
		"address:fd00::1234\nadded fd00::1234.\nfd00::1234: link up\nula fdaa:bbbb:cccc::\n")

	// One address gets one placeholder however it is written; punctuation
	// around it stays.
	want := "InternalIP fe80:0000:0000:0000:0000:0000:0000:0001\n" +
		"same fe80:0000:0000:0000:0000:0000:0000:0001\n" +
		"link fe80:0000:0000:0000:0000:0000:0000:0002%eth0\n" +
		"mapped fe80:0000:0000:0000:0000:0000:0000:0003\n" +
		"prefix fe80:0000:0000:0000:0000:0000:0000:0004/64\n" +
		"address:fe80:0000:0000:0000:0000:0000:0000:0001\n" +
		"added fe80:0000:0000:0000:0000:0000:0000:0001.\n" +
		"fe80:0000:0000:0000:0000:0000:0000:0001: link up\n" +
		"ula fe80:0000:0000:0000:0000:0000:0000:0005\n"
	require.Equal(t, want, out)
	require.Equal(t, want, s.scrub(out), "scrubbing the output again changes nothing")
}

func TestIPv6LookalikesStay(t *testing.T) {
	s := newScrubber(nil)
	in := "at 04:50:48.123 in std::vector, ns a::b, 1:: Field, prefix fe80:: loopback ::1 any :: all-nodes ff02::1"
	require.Equal(t, in, s.scrub(in))
}

func TestRunScrubsNestedFileAndDirectoryNames(t *testing.T) {
	src := filepath.Join(t.TempDir(), "capture")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "worker-7"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "worker-7", "worker-7.txt"), []byte("node worker-7\n"), 0o644))
	out := filepath.Join(t.TempDir(), "out")

	var stderr bytes.Buffer
	require.Equal(t, 0, run([]string{"-out", out, "-replace", "worker-7=node-a", src}, io.Discard, &stderr), stderr.String())

	got, err := os.ReadFile(filepath.Join(out, "capture", "node-a", "node-a.txt"))
	require.NoError(t, err)
	require.Equal(t, "node node-a\n", string(got))
}

// Two nodes scrubbed in one run: the second node's dump numbers its GPUs from 0,
// as the NVML dump does, but its text files continue from the first node's. After
// scrubbing, each GPU has the same UUID and serial in its dump and its XML.
func TestDumpPlaceholdersFollowTheNodesTextFiles(t *testing.T) {
	gpu := func(bus, uuid, serial string) string {
		return `<gpu id="` + bus + `"><serial>` + serial + `</serial><uuid>` + uuid + `</uuid>` +
			`<platformInfo><chassis_serial_number>1999000111</chassis_serial_number></platformInfo></gpu>`
	}
	busBytes := func(bus string) string {
		parts := make([]string, 0, 32)
		for i := 0; i < 32; i++ {
			c := 0
			if i < len(bus) {
				c = int(bus[i])
			}
			parts = append(parts, strconv.Itoa(c))
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	dev := func(i int, bus, uuid, serial string) string {
		return `{"index":` + strconv.Itoa(i) + `,"calls":{` +
			`"GetPciInfo":{"ret":"SUCCESS","value":{"BusId":` + busBytes(bus) + `}},` +
			`"GetPlatformInfo":{"ret":"SUCCESS","value":{"ChassisSerialNumber":[49,57,57,57,48,48,48,49,49,49,0,0,0,0,0,0]}},` +
			`"GetSerial":{"ret":"SUCCESS","value":"` + serial + `"},` +
			`"GetUUID":{"ret":"SUCCESS","value":"` + uuid + `"}}}`
	}
	nodes := map[string][2][3]string{
		"node-a": {{"00000002:81:00.0", "GPU-aaaaaaaa-1111-2222-3333-444444444444", "1321000000001"},
			{"00000002:C1:00.0", "GPU-bbbbbbbb-1111-2222-3333-444444444444", "1321000000002"}},
		"node-b": {{"00000002:81:00.0", "GPU-cccccccc-1111-2222-3333-444444444444", "1321000000003"},
			{"00000002:C1:00.0", "GPU-dddddddd-1111-2222-3333-444444444444", "1321000000004"}},
	}
	src := t.TempDir()
	srcs := make([]string, 0, 2)
	for _, node := range []string{"node-a", "node-b"} {
		dir := filepath.Join(src, node)
		srcs = append(srcs, dir)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		g := nodes[node]
		xmlDoc := "<nvidia_smi_log>" + gpu(g[0][0], g[0][1], g[0][2]) + gpu(g[1][0], g[1][1], g[1][2]) + "</nvidia_smi_log>"
		require.NoError(t, os.WriteFile(filepath.Join(dir, "smi-q.xml"), []byte(xmlDoc), 0o644))
		// Busses listed in reverse, so the pairing cannot rely on order.
		dump := `{"devices":[` + dev(0, strings.ToLower(g[1][0]), g[1][1], g[1][2]) + "," +
			dev(1, g[0][0], g[0][1], g[0][2]) + `],"system":{}}`
		require.NoError(t, os.WriteFile(filepath.Join(dir, "nvml-dump.json"), []byte(dump), 0o644))
	}
	out := filepath.Join(t.TempDir(), "out")
	var stderr bytes.Buffer
	require.Equal(t, 0, run(append([]string{"-out", out}, srcs...), io.Discard, &stderr), stderr.String())

	for _, node := range []string{"node-a", "node-b"} {
		xmlOut, err := os.ReadFile(filepath.Join(out, node, "smi-q.xml"))
		require.NoError(t, err)
		gpus := parseSMIGPUs(xmlOut)
		dumpOut, err := os.ReadFile(filepath.Join(out, node, "nvml-dump.json"))
		require.NoError(t, err)
		var d struct {
			Devices []struct {
				Calls map[string]struct {
					Value json.RawMessage `json:"value"`
				} `json:"calls"`
			} `json:"devices"`
		}
		require.NoError(t, json.Unmarshal(dumpOut, &d))
		for _, dv := range d.Devices {
			var pci struct {
				BusID []int `json:"BusId"`
			}
			require.NoError(t, json.Unmarshal(dv.Calls["GetPciInfo"].Value, &pci))
			bus := make([]byte, 0, len(pci.BusID))
			for _, c := range pci.BusID {
				if c == 0 {
					break
				}
				bus = append(bus, byte(c))
			}
			g, ok := gpus[normalizeBusID(string(bus))]
			require.True(t, ok, "%s: bus %s", node, bus)
			var uuid, serial string
			require.NoError(t, json.Unmarshal(dv.Calls["GetUUID"].Value, &uuid))
			require.NoError(t, json.Unmarshal(dv.Calls["GetSerial"].Value, &serial))
			require.Equal(t, g.UUID, uuid, node)
			require.Equal(t, g.Serial, serial, node)
			var platform struct{ ChassisSerialNumber []int }
			require.NoError(t, json.Unmarshal(dv.Calls["GetPlatformInfo"].Value, &platform))
			chassis := make([]byte, 0, len(platform.ChassisSerialNumber))
			for _, c := range platform.ChassisSerialNumber {
				if c == 0 {
					break
				}
				chassis = append(chassis, byte(c))
			}
			require.Equal(t, g.Chassis, string(chassis), node)
		}
	}
	all, err := os.ReadFile(filepath.Join(out, "node-b", "nvml-dump.json"))
	require.NoError(t, err)
	require.NotContains(t, string(all), "GPU-00000000-0000-0000-0000-000000000000",
		"node-b's GPUs do not reuse node-a's placeholders")
}

func TestDumpPDIAndFabricGUIDFollowTheTextFiles(t *testing.T) {
	const (
		bus    = "00000002:81:00.0"
		pdi    = "0x1122334455667788"
		fabric = "0x99aabbccddeeff00"
	)
	busBytes := make([]string, 32)
	for i := range busBytes {
		c := 0
		if i < len(bus) {
			c = int(bus[i])
		}
		busBytes[i] = strconv.Itoa(c)
	}
	dir := filepath.Join(t.TempDir(), "node-a")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	files := map[string]string{
		// Sorts first, so its GUID takes placeholder 1 and the dump's own
		// numbering (which also starts at 1) cannot pass by accident.
		"a.txt": "other 0x0102030405060708\n",
		"nvml-dump.json": `{"devices":[{"index":0,"calls":{` +
			`"GetPciInfo":{"ret":"SUCCESS","value":{"BusId":[` + strings.Join(busBytes, ",") + `]}},` +
			`"GetPdi":{"ret":"SUCCESS","value":{"Value":1234605616436508552,"Version":16777232}},` +
			`"GetPlatformInfo":{"ret":"SUCCESS","value":{"IbGuid":[0,255,238,221,204,187,170,153,0,0,0,0,0,0,0,0]}}}}],"system":{}}`,
		"smi-q.txt": "    GPU PDI : " + pdi + "\n        GPU Fabric GUID : " + fabric + "\n",
		"smi-q.xml": `<nvidia_smi_log><gpu id="` + bus + `"><pdi>` + pdi + `</pdi>` +
			`<platformInfo><gpu_fabric_guid>` + fabric + `</gpu_fabric_guid></platformInfo></gpu></nvidia_smi_log>`,
	}
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	out := filepath.Join(t.TempDir(), "out")
	var stderr bytes.Buffer
	require.Equal(t, 0, run([]string{"-out", out, dir}, io.Discard, &stderr), stderr.String())

	text, err := os.ReadFile(filepath.Join(out, "node-a", "smi-q.txt"))
	require.NoError(t, err)
	require.Equal(t, "    GPU PDI : 0x0000000000000002\n        GPU Fabric GUID : 0x0000000000000003\n", string(text))
	xmlOut, err := os.ReadFile(filepath.Join(out, "node-a", "smi-q.xml"))
	require.NoError(t, err)
	require.Contains(t, string(xmlOut), "<pdi>0x0000000000000002</pdi>")
	require.Contains(t, string(xmlOut), "<gpu_fabric_guid>0x0000000000000003</gpu_fabric_guid>")

	dumpOut, err := os.ReadFile(filepath.Join(out, "node-a", "nvml-dump.json"))
	require.NoError(t, err)
	var d struct {
		Devices []struct {
			Calls struct {
				GetPdi struct {
					Value struct{ Value uint64 } `json:"value"`
				}
				GetPlatformInfo struct {
					Value struct {
						IbGUID []int `json:"IbGuid"`
					} `json:"value"`
				}
			} `json:"calls"`
		} `json:"devices"`
	}
	require.NoError(t, json.Unmarshal(dumpOut, &d))
	require.Len(t, d.Devices, 1)
	require.Equal(t, uint64(2), d.Devices[0].Calls.GetPdi.Value.Value)
	require.Equal(t, []int{3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, d.Devices[0].Calls.GetPlatformInfo.Value.IbGUID,
		"the fabric GUID is the first 8 bytes of IbGuid, little-endian")
}
