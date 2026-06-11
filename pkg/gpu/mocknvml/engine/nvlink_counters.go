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
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

// processStart is the last-resort counter epoch. Because nvidia-smi
// dlopens a fresh copy of the mock on every invocation, a process-local
// epoch would reset each call and NVLink counters would never grow across
// separate nvidia-smi runs. The real epoch therefore comes from
// MOCK_NVML_EPOCH or /proc/stat btime, both process-independent; this only
// fires when neither is available (e.g. a unit-test host).
var processStart = time.Now()

// resolveCounterEpoch picks the process-independent anchor for the
// deterministic NVLink counter accrual:
//
//  1. MOCK_NVML_EPOCH (unix seconds), written by setup.sh at container
//     start — gives pod-deterministic, monotonically growing counters.
//  2. /proc/stat btime (system boot time) — stable across all nvidia-smi
//     runs on the node.
//  3. process start time — last resort so unit tests still work off-Linux.
func resolveCounterEpoch() time.Time {
	if s := strings.TrimSpace(os.Getenv("MOCK_NVML_EPOCH")); s != "" {
		if secs, err := strconv.ParseInt(s, 10, 64); err == nil {
			return time.Unix(secs, 0)
		}
	}
	if bt, ok := procStatBtime("/proc/stat"); ok {
		return time.Unix(bt, 0)
	}
	return processStart
}

// procStatBtime reads the "btime <seconds>" line from a /proc/stat-style
// file. Returns false when the file or field is absent.
func procStatBtime(path string) (int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "btime" {
			if v, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
				return v, true
			}
		}
	}
	return 0, false
}
