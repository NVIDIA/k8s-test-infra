// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package sysattr

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWrite_OneLinePerFileAsTheKernelShowsIt(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "id")

	require.NoError(t, Write(dir, Attributes{
		"sys_vendor": "NVIDIA",
		"bios_date":  "",
	}, 0o444))

	vendor, err := os.ReadFile(filepath.Join(dir, "sys_vendor"))
	require.NoError(t, err)
	require.Equal(t, "NVIDIA\n", string(vendor))

	date, err := os.ReadFile(filepath.Join(dir, "bios_date"))
	require.NoError(t, err)
	require.Equal(t, "\n", string(date), "an empty attribute still shows its newline")
}

func TestWrite_AppliesPerm(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	require.NoError(t, Write(dir, Attributes{"sys_vendor": "NVIDIA"}, 0o444))

	info, err := os.Stat(filepath.Join(dir, "sys_vendor"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o444), info.Mode().Perm())
}

func TestWrite_RejectsWhatReadersWouldMisread(t *testing.T) {
	t.Parallel()

	// Each set pairs a valid attribute with the invalid one, so a partial
	// write would show up as a file left behind.
	for name, a := range map[string]Attributes{
		"empty name":         {"sys_vendor": "NVIDIA", "": "x"},
		"nested name":        {"sys_vendor": "NVIDIA", "id/product_name": "x"},
		"dot name":           {"sys_vendor": "NVIDIA", ".": "x"},
		"dot-dot name":       {"sys_vendor": "NVIDIA", "..": "x"},
		"value with newline": {"sys_vendor": "NVIDIA", "product_name": "x\ny"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()

			require.Error(t, Write(dir, a, 0o444))

			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			require.Empty(t, entries, "an invalid attribute must leave dir untouched")
		})
	}
}

func TestRead_TrimsTheNewlineWriteAdds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := Attributes{"product_name": "NVIDIA GB300 NVL", "bios_date": ""}
	require.NoError(t, Write(dir, want, 0o444))

	got, err := Read(dir)

	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestRead_MissingDir(t *testing.T) {
	t.Parallel()

	_, err := Read(filepath.Join(t.TempDir(), "absent"))

	require.ErrorIs(t, err, os.ErrNotExist)
}
