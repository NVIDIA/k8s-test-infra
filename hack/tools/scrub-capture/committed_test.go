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
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NVIDIA/k8s-test-infra/hack/tools/internal/redact"
)

// capturesDir is where real-node captures are committed.
const capturesDir = "../../../tests/hardware-captures"

// TestCommittedCapturesAreScrubbed runs the scrubber's own rules over every file
// committed under tests/hardware-captures. A scrubbed file is a fixed point of
// the scrubber: scrubbing it again changes nothing, because every identifier it
// would replace (a GPU, MIG or fabric UUID, a serial number, a PDI or GUID, a MAC
// or IPv6 address, a machine ID) is already a placeholder. A file that still
// carries a real identifier changes, and the test names it. It is the counterpart
// of TestHardwareCapturesAreScrubbed in the nvidiasmi assertions, which covers the
// `nvidia-smi -q -x` fixtures only.
//
// The check is a floor, not a review: it knows the shapes the scrubber knows. Names
// of tenants, sites and users have no shape, so the reviewer of a capture still
// reads it for those.
func TestCommittedCapturesAreScrubbed(t *testing.T) {
	if _, err := os.Stat(capturesDir); err != nil {
		t.Skipf("no committed captures: %v", err)
	}

	checked := 0
	err := filepath.WalkDir(capturesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		name := d.Name()
		rel, _ := filepath.Rel(capturesDir, path)

		// Raw-capture leftovers that must never be committed: the driver's library
		// is NVIDIA's binary, and errors.log names the machine that produced it.
		require.NotContains(t, name, ".so", "%s: do not commit a shared library", rel)
		require.NotEqual(t, "errors.log", name, "%s: errors.log is not committed", rel)

		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		if strings.HasSuffix(name, ".gz") {
			zr, err := gzip.NewReader(bytes.NewReader(raw))
			require.NoError(t, err, rel)
			raw, err = io.ReadAll(zr)
			require.NoError(t, err, rel)
		}
		require.False(t, isBinary(raw), "%s: binary file", rel)

		checked++
		if isDump(name) {
			// Identifiers in a recording are byte arrays and integers: judged by
			// field name, not shape. A scrubbed recording is a fixed point of the
			// redaction.
			again, err := redact.New().JSON(raw)
			require.NoError(t, err, rel)
			want, err := redact.Canonical(raw)
			require.NoError(t, err, rel)
			got, err := redact.Canonical(again)
			require.NoError(t, err, rel)
			if !bytes.Equal(want, got) {
				t.Errorf("%s still carries identifiers (UUIDs, serials, PDIs or GUIDs); run scrub-capture over the capture again", rel)
			}
		}
		text := string(raw)
		s := newScrubber(nil)
		s.seedGPUs(text)
		if got := s.scrub(text); got != text {
			t.Errorf("%s still carries identifiers the scrubber replaces; run scrub-capture over the capture again:\n%s",
				rel, firstDifference(text, got))
		}
		return nil
	})
	require.NoError(t, err)
	require.Positive(t, checked, "the captures directory holds no files")
}

// firstDifference shows where a file and its rescrubbed copy part, without
// printing the whole file.
func firstDifference(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range al {
		if i >= len(bl) || al[i] != bl[i] {
			got := ""
			if i < len(bl) {
				got = bl[i]
			}
			return "  line " + strconv.Itoa(i+1) + ": " + clip(al[i]) + "\n  would become: " + clip(got)
		}
	}
	return "  (trailing difference)"
}

func clip(s string) string {
	if len(s) > 160 {
		return s[:157] + "..."
	}
	return s
}
