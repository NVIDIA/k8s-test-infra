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

// Package nvmldump records what an NVML library answers, and compares two
// recordings. It backs `mokka-capture dump` and `mokka-capture diff nvml`.
//
// The dump calls every argument-free getter on every device, by reflection
// over go-nvml's device handle, so a getter added in a newer go-nvml is picked
// up without editing this file. For each call it records the NVML return code
// and the value. A getter the library answers with NOT_SUPPORTED is recorded
// as such: on real hardware that is the answer the mock has to give too.
//
// A getter whose arguments are integers or enums is called over a fixed range
// of values (clock types, NVLink indexes, ECC counter kinds and so on) and
// recorded as "Name(a,b)"; a value past the end of an enum answers
// INVALID_ARGUMENT, which is recorded too. GetFieldValues is probed one field
// ID at a time, since the IDs a driver defines differ between releases. A getter
// with any other kind of argument (a struct, a handle, a string) is listed under
// "skipped" rather than called, so the coverage of a dump is visible in the
// dump itself.
//
// A dump of real hardware contains identifiers (the GPU UUID, the serial
// number, fabric UUIDs). Keep it out of the repository until it has been
// reviewed.
package nvmldump

import "io"

// Dump writes a recording of the NVML library on the loader path to stdout.
func Dump(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		_, _ = io.WriteString(stderr, "usage: mokka-capture dump\n")
		return 2
	}
	return runDump(stdout, stderr)
}

// Diff compares two recordings.
func Diff(args []string, stdout, stderr io.Writer) int {
	return runDiff(args, stdout, stderr)
}
