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

func TestResolveNVMLSources_ExplicitPathsBypassTheModuleCache(t *testing.T) {
	input, header, err := resolveNVMLSources("/tmp/nvml.go", "/tmp/nvml.h")
	require.NoError(t, err)
	require.Equal(t, "/tmp/nvml.go", input)
	require.Equal(t, "/tmp/nvml.h", header)
}

func TestResolveNVMLSources_EmptyPathsComeFromTheModuleCache(t *testing.T) {
	input, header, err := resolveNVMLSources("", "")
	require.NoError(t, err)

	// The generator is useless if it resolves paths that do not exist, so
	// assert the files are readable rather than just string-matching.
	require.FileExists(t, input)
	require.FileExists(t, header)
	require.Equal(t, "nvml.go", filepath.Base(input))
	require.Equal(t, "nvml.h", filepath.Base(header))

	// Both must come out of the same module directory; a mismatch would pair
	// a header with a wrapper from a different version.
	require.Equal(t, filepath.Dir(input), filepath.Dir(header))
}

func TestResolveNVMLSources_OneEmptyPathIsFilledIn(t *testing.T) {
	input, header, err := resolveNVMLSources("", "/tmp/nvml.h")
	require.NoError(t, err)
	require.FileExists(t, input)
	require.Equal(t, "/tmp/nvml.h", header)
}

func TestModuleDir_UnknownModuleReportsAnError(t *testing.T) {
	_, err := moduleDir("example.com/definitely/not/a/dependency")
	require.Error(t, err)
}

func TestModuleDir_ReturnsAReadableDirectory(t *testing.T) {
	dir, err := moduleDir(nvmlSourceModule)
	require.NoError(t, err)
	require.NotEmpty(t, dir)

	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
}
