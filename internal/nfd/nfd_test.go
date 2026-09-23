// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nfd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncode_SortedLines(t *testing.T) {
	t.Parallel()

	data, err := Features{
		"pci-10de.present":         "true",
		"nvidia.com/mock.enabled":  "true",
		"gpu.count":                "8",
		"example.com/empty-string": "",
	}.Encode()
	require.NoError(t, err)
	require.Equal(t,
		"example.com/empty-string=\n"+
			"gpu.count=8\n"+
			"nvidia.com/mock.enabled=true\n"+
			"pci-10de.present=true\n",
		string(data))
}

func TestEncode_Empty(t *testing.T) {
	t.Parallel()

	data, err := Features{}.Encode()
	require.NoError(t, err)
	require.Empty(t, data)
}

func TestEncode_RejectsWhatNFDWouldMisread(t *testing.T) {
	t.Parallel()

	for name, f := range map[string]Features{
		"empty name":          {"": "true"},
		"name with equals":    {"a=b": "true"},
		"name with newline":   {"a\nb": "true"},
		"comment-like name":   {"#a": "true"},
		"removal-like name":   {"-a": "true"},
		"value with newline":  {"a": "x\nb=true"},
		"value with space":    {"a": "x y"},
		"value over 63 chars": {"a": strings.Repeat("x", 64)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := f.Encode()
			require.Error(t, err)
		})
	}
}

func TestWrite_CreatesFileWithEncodedFeatures(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), FeaturesDir, "mock.features")

	require.NoError(t, Write(path, Features{"pci-10de.present": "true"}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "pci-10de.present=true\n", string(data))
}

func TestDelete_RemovesFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "mock.features")
	require.NoError(t, Write(path, Features{"a": "true"}))

	require.NoError(t, Delete(path))

	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestDelete_AbsentFileIsNotAnError(t *testing.T) {
	t.Parallel()

	require.NoError(t, Delete(filepath.Join(t.TempDir(), "missing.features")))
}

func TestWrite_InvalidFeaturesLeaveFileUntouched(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "mock.features")
	require.NoError(t, Write(path, Features{"a": "true"}))

	require.Error(t, Write(path, Features{"a=b": "true"}))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "a=true\n", string(data))
}
