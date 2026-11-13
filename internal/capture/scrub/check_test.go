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
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const smiCapture = `<nvidia_smi_log>
<gpu id="1"><uuid>GPU-f662ab51-7dd5-545c-be52-5aa9606316be</uuid><serial>1234567890123</serial>
<chassis_serial_number>1234567899999</chassis_serial_number><pdi>0xabcdef0011223344</pdi>
<fabric><clusterUuid>11111111-2222-3333-4444-555555555555</clusterUuid><gpu_fabric_guid>0xabcdef0011223355</gpu_fabric_guid></fabric></gpu>
<gpu id="2"><uuid>GPU-401dc5ce-9ad3-58f4-a530-58634995b5ea</uuid><serial>N/A</serial>
<chassis_serial_number></chassis_serial_number><pdi>0x0000000000000000</pdi>
<fabric><clusterUuid>00000000-0000-0000-0000-000000000000</clusterUuid><gpu_fabric_guid>N/A</gpu_fabric_guid></fabric></gpu>
</nvidia_smi_log>`

func TestCheckFlagsEveryUnscrubbedElement(t *testing.T) {
	require.Len(t, check(smiCapture), 7, "both GPU UUIDs and the five other identifiers")
}

// The scrubber's output for an nvidia-smi capture passes the check the
// repository's hardware captures are held to.
func TestScrubbedCapturePassesCheck(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "raw", "node")
	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "smi-q.xml"), []byte(smiCapture), 0o600))
	out := filepath.Join(dir, "out")
	require.Equal(t, 0, runCLI([]string{"-out", out, src}, io.Discard, io.Discard))

	scrubbed := filepath.Join(out, "node", "smi-q.xml")
	got, err := os.ReadFile(scrubbed)
	require.NoError(t, err)
	require.Empty(t, check(string(got)))

	raw := filepath.Join(src, "smi-q.xml")
	require.Equal(t, 1, runCLI([]string{"-check", raw}, io.Discard, io.Discard))
	require.Equal(t, 0, runCLI([]string{"-check", scrubbed}, io.Discard, io.Discard))
}
