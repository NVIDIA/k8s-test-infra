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
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// checkedElements are the identifiers of an `nvidia-smi -q -x` capture, with
// the placeholders tests/e2e/go/assertions/nvidiasmi/testdata/hardware/README.md
// documents. They are the patterns TestHardwareCapturesAreScrubbed applies.
var checkedElements = []struct {
	element string
	pattern *regexp.Regexp
}{
	{"serial", regexp.MustCompile(`^1650000000\d{3}$`)},
	{"chassis_serial_number", regexp.MustCompile(`^1820000000\d{3}$`)},
	{"uuid", regexp.MustCompile(`^GPU-000000[0-9a-f]{2}-0000-0000-0000-0000000000[0-9a-f]{2}$`)},
	{"clusterUuid", regexp.MustCompile(`^0{7}[0-9a-f]-0000-0000-0000-0{11}[0-9a-f]$`)},
	{"pdi", regexp.MustCompile(`^0x0{14}[0-9a-f]{2}$`)},
	{"gpu_fabric_guid", regexp.MustCompile(`^0x0{14}[0-9a-f]{2}$`)},
}

// check returns one message per identifier element whose value is neither
// absent nor a placeholder.
func check(src string) []string {
	var problems []string
	for _, c := range checkedElements {
		re := regexp.MustCompile(`<` + c.element + `>([^<]*)</` + c.element + `>`)
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			v := strings.TrimSpace(m[1])
			if isAbsent(v) || c.pattern.MatchString(v) {
				continue
			}
			problems = append(problems, fmt.Sprintf("<%s> is not a placeholder", c.element))
		}
	}
	return problems
}

// runCheck checks each file and returns 1 if any still holds an identifier.
func runCheck(files []string, stdout, stderr io.Writer) int {
	status := 0
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			status = 1
			continue
		}
		problems := check(string(raw))
		for _, p := range problems {
			_, _ = fmt.Fprintf(stderr, "%s: %s\n", f, p)
		}
		if len(problems) > 0 {
			status = 1
		}
	}
	if status == 0 {
		_, _ = fmt.Fprintln(stdout, "ok: every identifier is a placeholder or absent")
	}
	return status
}
