// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpudriver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeNvidiaSMIImage lays out an image's bundled nvidia-smi binaries under a
// temp directory, each one's content naming its version, plus a pod PATH entry
// that starts out pointing at the oldest, as the Dockerfile leaves it.
func fakeNvidiaSMIImage(t *testing.T, versions ...string) nvidiaSMIImage {
	t.Helper()
	root := t.TempDir()
	img := nvidiaSMIImage{
		bundledDir: filepath.Join(root, "libexec", "nvidia-smi"),
		podPath:    filepath.Join(root, "bin", nvidiaSMIName),
	}
	for _, v := range versions {
		path := filepath.Join(img.bundledDir, v, nvidiaSMIName)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(v), 0o755))
	}
	if len(versions) > 0 {
		require.NoError(t, os.MkdirAll(filepath.Dir(img.podPath), 0o755))
		require.NoError(t, os.Symlink(filepath.Join(img.bundledDir, versions[0], nvidiaSMIName), img.podPath))
	}
	return img
}

// A node runs the nvidia-smi of its own driver branch where the image has one,
// and never a newer branch than its driver: NVML serves older callers only.
func TestNvidiaSMIImage_PicksTheDriversBranch(t *testing.T) {
	t.Parallel()
	img := fakeNvidiaSMIImage(t, "615.71.09", "580.65.06")
	// Neither is a bundled binary: one has no nvidia-smi, one is not a version.
	require.NoError(t, os.MkdirAll(filepath.Join(img.bundledDir, "620.10"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(img.bundledDir, "notes"), 0o755))

	for driver, want := range map[string]string{
		"580.65.06":  "580.65.06",
		"580.95.05":  "580.65.06",
		"595.91.07":  "580.65.06", // no 595 binary, and 615 would be newer than the driver
		"615.71.09":  "615.71.09",
		"615.20":     "615.71.09", // same branch
		"620.10.01":  "615.71.09",
		"550.163.01": "580.65.06", // older than every bundled branch: the oldest
		"":           "580.65.06",
	} {
		path, version, err := img.pick(driver)
		require.NoError(t, err, "driver %q", driver)
		require.Equal(t, want, version, "driver %q", driver)
		require.Equal(t, filepath.Join(img.bundledDir, want, nvidiaSMIName), path, "driver %q", driver)
	}
}

func TestNvidiaSMIImage_NoBundledBinary(t *testing.T) {
	t.Parallel()
	_, _, err := nvidiaSMIImage{bundledDir: filepath.Join(t.TempDir(), "absent")}.pick("615.71.09")
	require.ErrorIs(t, err, errNoBundledNvidiaSMI)

	_, _, err = fakeNvidiaSMIImage(t).pick("615.71.09")
	require.ErrorIs(t, err, errNoBundledNvidiaSMI, "an empty directory")
}

// Consumers get the staged copy through the driver root, and the node agent's
// own nvidia-smi, which every `kubectl exec ... nvidia-smi` runs, is that same
// copy.
func TestStageNvidiaSMI_StagesTheDriversBranch(t *testing.T) {
	t.Parallel()
	for driver, want := range map[string]string{
		"580.65.06": "580.65.06",
		"615.71.09": "615.71.09",
	} {
		t.Run(driver, func(t *testing.T) {
			t.Parallel()
			h := testHost(t)
			state := testState(t)
			state.Software.DriverVersion = driver
			img := fakeNvidiaSMIImage(t, "580.65.06", "615.71.09")

			require.NoError(t, stageNvidiaSMIFrom(t.Context(), h, state, img))

			staged := h.RootPath("driver/usr/bin", nvidiaSMIName)
			data, err := os.ReadFile(staged)
			require.NoError(t, err)
			require.Equal(t, want, string(data))
			target, err := os.Readlink(img.podPath)
			require.NoError(t, err)
			require.Equal(t, staged, target)
		})
	}
}

// Without a bundled binary the shell fallback is staged as before, and the
// pod's own PATH entry is left alone.
func TestStageNvidiaSMI_FallsBackWithoutABundledBinary(t *testing.T) {
	t.Parallel()
	h := testHost(t)
	img := fakeNvidiaSMIImage(t)

	require.NoError(t, stageNvidiaSMIFrom(t.Context(), h, testState(t), img))

	target, err := os.Readlink(h.RootPath("driver/usr/bin", nvidiaSMIName))
	require.NoError(t, err)
	require.Equal(t, "nvidia-smi.sh", target)
	_, err = os.Lstat(img.podPath)
	require.True(t, os.IsNotExist(err), "the pod's nvidia-smi is not touched")
}
