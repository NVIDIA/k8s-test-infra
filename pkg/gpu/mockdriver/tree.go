// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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

package mockdriver

import (
	"fmt"
	"path/filepath"
)

// FileSpec describes a file to be created: either a regular file with
// content or a symlink.
type FileSpec struct {
	Path      string
	SymlinkTo string
	Content   string
	Mode      uint32
	IsCharDev bool
	DevMajor  uint32
	DevMinor  uint32
	EmptyFile bool // If true, create an empty file (for nvcdi testing)
}

// DefaultFiles returns the mock driver/toolkit tree files under the
// specified root directory. Following nvidia-container-toolkit's testdata
// convention, we create empty files for libraries to work with nvcdi's
// library discovery when __NVCT_TESTING_DEVICES_ARE_FILES=true.
func DefaultFiles(root string) []FileSpec {
	// Match dgxa100 mock driver version
	driverVer := "550.54.15"

	files := []FileSpec{
		// Libraries with versioned naming (empty files like testdata)
		{Path: filepath.Join(root, "lib64", "libcuda.so."+driverVer),
			Mode: 0644, EmptyFile: true},
		{Path: filepath.Join(root, "lib64", "libcuda.so.1"),
			SymlinkTo: "libcuda.so." + driverVer},
		{Path: filepath.Join(root, "lib64", "libcuda.so"),
			SymlinkTo: "libcuda.so.1"},

		{Path: filepath.Join(root, "lib64", "libnvidia-ml.so."+driverVer),
			Mode: 0644, EmptyFile: true},
		{Path: filepath.Join(root, "lib64", "libnvidia-ml.so.1"),
			SymlinkTo: "libnvidia-ml.so." + driverVer},
		{Path: filepath.Join(root, "lib64", "libnvidia-ml.so"),
			SymlinkTo: "libnvidia-ml.so.1"},

		{Path: filepath.Join(root, "lib64",
			"libnvidia-encode.so."+driverVer), Mode: 0644, EmptyFile: true},
		{Path: filepath.Join(root, "lib64", "libnvidia-encode.so.1"),
			SymlinkTo: "libnvidia-encode.so." + driverVer},
		{Path: filepath.Join(root, "lib64", "libnvidia-encode.so"),
			SymlinkTo: "libnvidia-encode.so.1"},

		{Path: filepath.Join(root, "lib64", "libnvcuvid.so."+driverVer),
			Mode: 0644, EmptyFile: true},
		{Path: filepath.Join(root, "lib64", "libnvcuvid.so.1"),
			SymlinkTo: "libnvcuvid.so." + driverVer},
		{Path: filepath.Join(root, "lib64", "libnvcuvid.so"),
			SymlinkTo: "libnvcuvid.so.1"},

		{Path: filepath.Join(root, "lib64",
			"libnvidia-ptxjitcompiler.so."+driverVer), Mode: 0644,
			EmptyFile: true},
		{Path: filepath.Join(root, "lib64", "libnvidia-ptxjitcompiler.so.1"),
			SymlinkTo: "libnvidia-ptxjitcompiler.so." + driverVer},
		{Path: filepath.Join(root, "lib64", "libnvidia-ptxjitcompiler.so"),
			SymlinkTo: "libnvidia-ptxjitcompiler.so.1"},

		{Path: filepath.Join(root, "lib64",
			"libnvidia-fatbinaryloader.so."+driverVer), Mode: 0644,
			EmptyFile: true},
		{Path: filepath.Join(root, "lib64",
			"libnvidia-fatbinaryloader.so.1"),
			SymlinkTo: "libnvidia-fatbinaryloader.so." + driverVer},
		{Path: filepath.Join(root, "lib64", "libnvidia-fatbinaryloader.so"),
			SymlinkTo: "libnvidia-fatbinaryloader.so.1"},

		// Binaries (shell scripts)
		{Path: filepath.Join(root, "bin", "nvidia-smi"), Mode: 0755,
			Content: "#!/bin/sh\necho 'NVIDIA-SMI (mock)'\n"},
		{Path: filepath.Join(root, "bin", "nvidia-debugdump"), Mode: 0755,
			Content: "#!/bin/sh\necho 'nvidia-debugdump (mock)'\n"},
		{Path: filepath.Join(root, "bin", "nvidia-persistenced"),
			Mode:    0755,
			Content: "#!/bin/sh\necho 'nvidia-persistenced (mock)'\n"},
		{Path: filepath.Join(root, "bin", "nvidia-modprobe"), Mode: 0755,
			Content: "#!/bin/sh\necho 'nvidia-modprobe (mock)'\n"},

		// Config
		{Path: filepath.Join(root, "etc", "nvidia-container-runtime",
			"config.toml"), Mode: 0644,
			Content: "# mock nvidia-container-runtime config\n"},
	}

	return files
}

// DeviceNodes returns device node specs. When
// __NVCT_TESTING_DEVICES_ARE_FILES=true, these will be created as empty
// files instead of character devices (following nvidia-container-toolkit's
// testdata convention).
func DeviceNodes(root string, gpuCount int, withDRI bool) []FileSpec {
	specs := []FileSpec{
		// When __NVCT_TESTING_DEVICES_ARE_FILES=true, use empty files
		{Path: filepath.Join(root, "dev", "nvidiactl"), Mode: 0666,
			EmptyFile: true},
		{Path: filepath.Join(root, "dev", "nvidia-uvm"), Mode: 0666,
			EmptyFile: true},
		{Path: filepath.Join(root, "dev", "nvidia-uvm-tools"), Mode: 0666,
			EmptyFile: true},
	}

	for i := 0; i < gpuCount; i++ {
		specs = append(specs, FileSpec{
			Path:      filepath.Join(root, "dev", "nvidia"+itoa(i)),
			Mode:      0666,
			EmptyFile: true,
		})
	}

	if withDRI {
		specs = append(specs, FileSpec{
			Path:      filepath.Join(root, "dev", "dri", "renderD128"),
			Mode:      0666,
			EmptyFile: true,
		})
	}

	return specs
}

func itoa(i int) string {
	return fmt.Sprintf("%d", i)
}
