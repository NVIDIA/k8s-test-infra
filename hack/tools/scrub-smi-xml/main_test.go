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

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const capture = `<nvidia_smi_log>
<gpu id="1"><uuid>GPU-f662ab51-7dd5-545c-be52-5aa9606316be</uuid><serial>1234567890123</serial>
<chassis_serial_number>9999</chassis_serial_number><pdi>0xabcdef0011223344</pdi>
<fabric><clusterUuid>11111111-2222-3333-4444-555555555555</clusterUuid><gpu_fabric_guid>0xabcdef0011223344</gpu_fabric_guid></fabric></gpu>
<gpu id="2"><uuid>GPU-e88c50aa-7e38-5b26-8005-fabdba9d7fec</uuid><serial>1234567890123</serial>
<chassis_serial_number>9999</chassis_serial_number><pdi>0x1122334455667788</pdi>
<fabric><clusterUuid>11111111-2222-3333-4444-555555555555</clusterUuid><gpu_fabric_guid>0x1122334455667788</gpu_fabric_guid></fabric></gpu>
<gpu id="3"><uuid>GPU-401dc5ce-9ad3-58f4-a530-58634995b5ea</uuid><serial>N/A</serial>
<chassis_serial_number></chassis_serial_number><pdi>0x0000000000000000</pdi>
<fabric><clusterUuid>00000000-0000-0000-0000-000000000000</clusterUuid><gpu_fabric_guid>N/A</gpu_fabric_guid></fabric></gpu>
</nvidia_smi_log>`

func TestScrubUsesOnePlaceholderPerDistinctValue(t *testing.T) {
	out, counts := scrub(capture)

	require.Contains(t, out, "<uuid>GPU-00000000-0000-0000-0000-000000000000</uuid>")
	require.Contains(t, out, "<uuid>GPU-00000001-0000-0000-0000-000000000001</uuid>")
	require.Contains(t, out, "<uuid>GPU-00000002-0000-0000-0000-000000000002</uuid>")

	// Two GPUs reported one serial, one chassis serial and one cluster: they
	// still share one placeholder.
	require.Equal(t, 2, countOf(out, "<serial>1650000000001</serial>"))
	require.Equal(t, 2, countOf(out, "<chassis_serial_number>1820000000001</chassis_serial_number>"))
	require.Equal(t, 2, countOf(out, "<clusterUuid>00000001-0000-0000-0000-000000000001</clusterUuid>"))

	require.Equal(t, 3, counts["uuid"])
	require.Equal(t, 1, counts["serial"])
	require.Equal(t, 1, counts["clusterUuid"])
}

func TestScrubSharesPdiAndFabricGUIDNumbering(t *testing.T) {
	out, _ := scrub(capture)

	require.Contains(t, out, "<pdi>0x0000000000000001</pdi>")
	require.Contains(t, out, "<gpu_fabric_guid>0x0000000000000001</gpu_fabric_guid>")
	require.Contains(t, out, "<pdi>0x0000000000000002</pdi>")
	require.Contains(t, out, "<gpu_fabric_guid>0x0000000000000002</gpu_fabric_guid>")
}

func TestScrubLeavesAbsentValuesAlone(t *testing.T) {
	out, _ := scrub(capture)

	require.Contains(t, out, "<serial>N/A</serial>")
	require.Contains(t, out, "<chassis_serial_number></chassis_serial_number>")
	require.Contains(t, out, "<pdi>0x0000000000000000</pdi>")
	require.Contains(t, out, "<clusterUuid>00000000-0000-0000-0000-000000000000</clusterUuid>")
	require.Contains(t, out, "<gpu_fabric_guid>N/A</gpu_fabric_guid>")
}

func TestScrubRemovesEveryOriginalIdentifier(t *testing.T) {
	out, _ := scrub(capture)

	for _, original := range []string{
		"f662ab51", "e88c50aa", "401dc5ce", "1234567890123", "9999",
		"abcdef0011223344", "1122334455667788", "11111111-2222-3333",
	} {
		require.NotContains(t, out, original)
	}
	require.Empty(t, check(out))
}

// scrubbed is capture with every identifier replaced by hand, following the
// placeholder rules in the tool's documentation.
const scrubbed = `<nvidia_smi_log>
<gpu id="1"><uuid>GPU-00000000-0000-0000-0000-000000000000</uuid><serial>1650000000001</serial>
<chassis_serial_number>1820000000001</chassis_serial_number><pdi>0x0000000000000001</pdi>
<fabric><clusterUuid>00000001-0000-0000-0000-000000000001</clusterUuid><gpu_fabric_guid>0x0000000000000001</gpu_fabric_guid></fabric></gpu>
<gpu id="2"><uuid>GPU-00000001-0000-0000-0000-000000000001</uuid><serial>1650000000001</serial>
<chassis_serial_number>1820000000001</chassis_serial_number><pdi>0x0000000000000002</pdi>
<fabric><clusterUuid>00000001-0000-0000-0000-000000000001</clusterUuid><gpu_fabric_guid>0x0000000000000002</gpu_fabric_guid></fabric></gpu>
<gpu id="3"><uuid>GPU-00000002-0000-0000-0000-000000000002</uuid><serial>N/A</serial>
<chassis_serial_number></chassis_serial_number><pdi>0x0000000000000000</pdi>
<fabric><clusterUuid>00000000-0000-0000-0000-000000000000</clusterUuid><gpu_fabric_guid>N/A</gpu_fabric_guid></fabric></gpu>
</nvidia_smi_log>`

func TestScrubGivesTheExpectedOutputAndIsIdempotent(t *testing.T) {
	once, _ := scrub(capture)
	require.Equal(t, scrubbed, once)
	twice, _ := scrub(once)
	require.Equal(t, scrubbed, twice, "scrubbing the output again changes nothing")
}

func TestCheckFlagsUnscrubbedCapture(t *testing.T) {
	require.NotEmpty(t, check(capture))
}

func TestRunCheckExitCodes(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "raw.xml")
	require.NoError(t, os.WriteFile(raw, []byte(capture), 0o600))

	var stdout, stderr discard
	require.Equal(t, 1, run([]string{"-check", raw}, &stdout, &stderr))

	scrubbed, _ := scrub(capture)
	clean := filepath.Join(dir, "clean.xml")
	require.NoError(t, os.WriteFile(clean, []byte(scrubbed), 0o600))
	require.Equal(t, 0, run([]string{"-check", clean}, &stdout, &stderr))
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func countOf(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}
