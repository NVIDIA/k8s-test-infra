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

package smidiff

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const realXML = `<nvidia_smi_log>
  <driver_version>615.71.09</driver_version>
  <gpu id="a"><product_name>Board</product_name><temperature><gpu_temp>31 C</gpu_temp></temperature><nvlinks><link>1</link><link>2</link></nvlinks><only_real>x</only_real></gpu>
  <gpu id="b"><product_name>Board</product_name></gpu>
</nvidia_smi_log>`

const mockXML = `<nvidia_smi_log>
  <driver_version>580.1</driver_version>
  <gpu id="a"><product_name>Board</product_name><temperature><gpu_temp>90 C</gpu_temp></temperature><nvlinks><link>1</link></nvlinks><only_mock>y</only_mock></gpu>
  <gpu id="b"><product_name>Board</product_name></gpu>
</nvidia_smi_log>`

func TestFlattenIndexesRepeatedSiblings(t *testing.T) {
	got, err := flatten(strings.NewReader(realXML))
	require.NoError(t, err)
	require.Equal(t, "Board", got["nvidia_smi_log[0]/gpu[0]/product_name[0]"])
	require.Equal(t, "2", got["nvidia_smi_log[0]/gpu[0]/nvlinks[0]/link[1]"])
	require.Equal(t, "615.71.09", got["nvidia_smi_log[0]/driver_version[0]"])
}

func TestCompareClassifiesPaths(t *testing.T) {
	hw, err := flatten(strings.NewReader(realXML))
	require.NoError(t, err)
	mock, err := flatten(strings.NewReader(mockXML))
	require.NoError(t, err)

	r := compare(hw, mock, regexp.MustCompile(defaultIgnore))
	require.Contains(t, r.onlyReal, "nvidia_smi_log[0]/gpu[0]/only_real[0]")
	require.Contains(t, r.onlyReal, "nvidia_smi_log[0]/gpu[0]/nvlinks[0]/link[1]")
	require.Equal(t, []string{"nvidia_smi_log[0]/gpu[0]/only_mock[0]"}, r.onlyMock)
	require.Equal(t, 1, r.ignored, "a temperature is matched on presence only")
	require.Len(t, r.differs, 1, "the driver version differs")
	require.Contains(t, r.differs[0], "driver_version")
}

func TestRunReportsRatios(t *testing.T) {
	dir := t.TempDir()
	a, b := dir+"/a.xml", dir+"/b.xml"
	writeFile(t, a, realXML)
	writeFile(t, b, mockXML)
	var out, errb bytes.Buffer
	require.Equal(t, 0, run([]string{a, b}, &out, &errb), errb.String())
	require.Contains(t, out.String(), "present in the mock:")
	require.Contains(t, out.String(), "value differs (1)")
}

func TestRunRejectsBadUsage(t *testing.T) {
	var out, errb bytes.Buffer
	require.Equal(t, 2, run([]string{"only-one"}, &out, &errb))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
