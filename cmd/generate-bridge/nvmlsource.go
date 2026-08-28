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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// nvmlSourceModule supplies both generator inputs: the Go wrapper scanned for
// NVML function names and the C header read for ABI-correct prototypes.
const nvmlSourceModule = "github.com/NVIDIA/go-nvml"

// resolveNVMLSources returns the nvml.go and nvml.h paths, filling in whichever
// argument is empty from the module cache.
//
// The defaults are resolved rather than written down as literals because the
// module cache escapes uppercase letters and embeds the version
// (github.com/!n!v!i!d!i!a/go-nvml@v0.13.3-1), so any literal path would break
// at the next dependency bump.
func resolveNVMLSources(input, header string) (string, string, error) {
	if input != "" && header != "" {
		return input, header, nil
	}

	dir, err := moduleDir(nvmlSourceModule)
	if err != nil {
		return "", "", err
	}

	if input == "" {
		input = filepath.Join(dir, "pkg", "nvml", "nvml.go")
	}
	if header == "" {
		header = filepath.Join(dir, "pkg", "nvml", "nvml.h")
	}
	return input, header, nil
}

// moduleDir reports where a module is unpacked in the local cache, downloading
// it first if necessary.
//
// `go mod download -json` rather than `go list -m -f '{{.Dir}}'`: the latter
// reports an empty Dir for a module that is not yet in the cache, which on a
// cold cache would silently yield a relative path of "pkg/nvml/nvml.go".
func moduleDir(module string) (string, error) {
	cmd := exec.Command("go", "mod", "download", "-json", module)
	cmd.Stderr = os.Stderr

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go mod download %s: %w", module, err)
	}

	var info struct {
		Dir   string
		Error string
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return "", fmt.Errorf("parsing go mod download output for %s: %w", module, err)
	}
	if info.Error != "" {
		return "", fmt.Errorf("go mod download %s: %s", module, info.Error)
	}
	if info.Dir == "" {
		return "", fmt.Errorf("go mod download %s reported no module directory", module)
	}
	return info.Dir, nil
}
