// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/nri/nvmlmock"
	"github.com/containerd/nri/pkg/api"
	"github.com/stretchr/testify/require"
)

// Device numbers are Linux dev_t values harvested from a node's /dev/nvidia*
// nodes. glibc encodes them as MMMM Mmmm mmmM MMmm: the major occupies bits
// 8-19 and 44-63, the minor bits 0-7 and 20-43. Cases below are derived from
// that encoding, not from the implementation.
//
// The two "wide" cases are the ones that matter. NVIDIA's real majors (195,
// 510, 511) and minors are all small, so they sit entirely in the legacy low
// bits and decode correctly even under a truncating formula. A test built only
// from real device numbers passes on broken code and proves nothing.
func TestMajorMinorDecodeLinuxDeviceNumbers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		dev       uint64
		wantMajor uint64
		wantMinor uint64
	}{
		{
			name: "zero",
			dev:  0x0, wantMajor: 0, wantMinor: 0,
		},
		{
			// /dev/null. Legacy 16-bit MMmm layout, entirely in the low bits.
			name: "legacy low bits only",
			dev:  0x103, wantMajor: 1, wantMinor: 3,
		},
		{
			// A real nvidia device number: major 511, minor 5. Included as an
			// anchor, and as the reason this bug survived review -- it decodes
			// correctly even when the high bits are dropped.
			name: "nvidia caps device",
			dev:  0x1ff05, wantMajor: 511, wantMinor: 5,
		},
		{
			// Major bit 44, the lowest bit of the extended major field. A
			// formula masking the major to 12 bits returns 0 here.
			name: "major above the legacy 12 bits",
			dev:  0x100000000000, wantMajor: 4096, wantMinor: 0,
		},
		{
			// Major bit 63, the highest bit of the extended major field.
			name: "major at the top of the extended field",
			dev:  0x8000000000000000, wantMajor: 2147483648, wantMinor: 0,
		},
		{
			// Minor bit 32. The extended minor field is bits 20-43; a formula
			// masking it to bits 20-31 returns 0 here.
			name: "minor above the legacy 20 bits",
			dev:  0x100000000, wantMajor: 0, wantMinor: 1048576,
		},
		{
			// Minor bit 43, the highest bit of the extended minor field.
			name: "minor at the top of the extended field",
			dev:  0x80000000000, wantMajor: 0, wantMinor: 2147483648,
		},
		{
			// Every field populated at once, so a swapped or overlapping mask
			// cannot pass by coincidence. Encodes major 0x12345678 and minor
			// 0x9abcdef0 per the glibc layout.
			name: "all fields populated",
			dev:  0x123459abcde678f0, wantMajor: 0x12345678, wantMinor: 0x9abcdef0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.wantMajor, major(tt.dev), "major(%#x)", tt.dev)
			require.Equal(t, tt.wantMinor, minor(tt.dev), "minor(%#x)", tt.dev)
		})
	}
}

// nriDevice stats a real path, so these exercise the syscall rather than a
// mock. The error paths are the interesting ones: both mean the plugin is about
// to hand containerd a device it cannot open.
func TestNriDeviceRejectsNonDevicePaths(t *testing.T) {
	t.Parallel()

	t.Run("nonexistent path returns the wrapped stat error", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "nvidia0")

		device, err := nriDevice(nvmlmock.Device{HostPath: missing, Path: "/dev/nvidia0"})

		// Catches a swallowed stat error: without the check, a vanished device
		// node yields a LinuxDevice with major/minor decoded from a zeroed
		// Stat_t, so containerd is told /dev/nvidia0 is device 0:0.
		require.Error(t, err)
		require.Nil(t, device)
		require.ErrorContains(t, err, "stat device "+missing)
	})

	t.Run("regular file is rejected as not a character device", func(t *testing.T) {
		t.Parallel()
		regular := filepath.Join(t.TempDir(), "nvidia0")
		require.NoError(t, os.WriteFile(regular, []byte("not a device"), 0o644))

		device, err := nriDevice(nvmlmock.Device{HostPath: regular, Path: "/dev/nvidia0"})

		// Catches a missing S_IFCHR check. A regular file stats fine, so
		// without it the plugin emits type "c" for a path that is not a
		// character device and container creation fails inside the runtime.
		require.Error(t, err)
		require.Nil(t, device)
		require.ErrorContains(t, err, regular+" is not a character device")
	})

	t.Run("directory is rejected as not a character device", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		device, err := nriDevice(nvmlmock.Device{HostPath: dir, Path: "/dev/nvidia0"})

		require.Error(t, err)
		require.Nil(t, device)
		require.ErrorContains(t, err, dir+" is not a character device")
	})
}

// The device number assertions live in dev_number_oracle_test.go: /dev/null's
// major and minor are only 1 and 3 under the Linux dev_t encoding. Everything
// asserted here is platform independent.
func TestNriDeviceMapsCharacterDeviceMetadata(t *testing.T) {
	t.Parallel()

	device, err := nriDevice(nvmlmock.Device{HostPath: "/dev/null", Path: "/dev/nvidia0"})
	require.NoError(t, err)
	require.NotNil(t, device)

	// Path must be the container path, not the host path. Catches a swap that
	// would mount the host's staging path into the container namespace.
	require.Equal(t, "/dev/nvidia0", device.Path)
	require.Equal(t, "c", device.Type)

	// FileMode must carry only the permission bits. Catches passing the raw
	// st_mode, whose S_IFCHR bits would land in the mode field.
	require.NotNil(t, device.FileMode)
	require.Equal(t, uint32(0o666), device.FileMode.GetValue()&uint32(os.ModePerm))
	require.Zero(t, device.FileMode.GetValue()&^uint32(os.ModePerm))
}

func TestFromNRI(t *testing.T) {
	t.Parallel()

	t.Run("copies pod and container fields", func(t *testing.T) {
		t.Parallel()
		pod := &api.PodSandbox{
			Namespace:   "gpu-tests",
			Annotations: map[string]string{"nvml-mock.nvidia.com/inject": "true"},
		}
		container := &api.Container{
			Env: []string{"PATH=/usr/bin", "MOCK_IB=off"},
			Mounts: []*api.Mount{{
				Source:      "/var/lib/nvml-mock",
				Destination: "/opt/nvml-mock",
				Type:        "bind",
				Options:     []string{"rbind", "ro"},
			}},
		}

		result := fromNRI(pod, container)

		require.Equal(t, "gpu-tests", result.Namespace)
		require.Equal(t, map[string]string{"nvml-mock.nvidia.com/inject": "true"}, result.PodAnnotations)
		require.Equal(t, []string{"PATH=/usr/bin", "MOCK_IB=off"}, result.Env)
		require.Equal(t, []nvmlmock.Mount{{
			Source:      "/var/lib/nvml-mock",
			Destination: "/opt/nvml-mock",
			Type:        "bind",
			Options:     []string{"rbind", "ro"},
		}}, result.Mounts)
	})

	// MEP-0002: the suppression rule in nvmlmock.Adjust is inert unless fromNRI
	// actually carries the incoming device state across. Verified against a real
	// containerd 2.2.0 CreateContainer payload, which populates Linux.Devices with
	// the device plugin's allocation and CDIDevices for the cdi-* strategies.
	t.Run("copies the devices the runtime already applied", func(t *testing.T) {
		t.Parallel()
		container := &api.Container{
			Linux: &api.LinuxContainer{
				Devices: []*api.LinuxDevice{
					{Path: "/dev/nvidia0", Type: "c", Major: 195, Minor: 0},
					{Path: "/dev/fuse", Type: "c", Major: 10, Minor: 229},
				},
			},
			CDIDevices: []*api.CDIDevice{{Name: "nvidia.com/gpu=0"}},
		}

		result := fromNRI(nil, container)

		require.Equal(t, []nvmlmock.Device{
			{Path: "/dev/nvidia0"},
			{Path: "/dev/fuse"},
		}, result.Devices)
		require.Equal(t, []string{"nvidia.com/gpu=0"}, result.CDIDevices)
	})

	t.Run("tolerates a nil Linux block", func(t *testing.T) {
		t.Parallel()
		// NRI leaves Linux nil for non-Linux or minimal containers; a nil deref
		// here takes the plugin down and stops injection node-wide.
		require.NotPanics(t, func() {
			result := fromNRI(nil, &api.Container{Env: []string{"PATH=/usr/bin"}})
			require.Empty(t, result.Devices)
			require.Empty(t, result.CDIDevices)
		})
	})

	t.Run("does not alias the runtime's slices", func(t *testing.T) {
		t.Parallel()
		container := &api.Container{
			Env:    []string{"PATH=/usr/bin"},
			Mounts: []*api.Mount{{Source: "/src", Options: []string{"rbind"}}},
		}

		result := fromNRI(nil, container)
		result.Env[0] = "PATH=/clobbered"
		result.Mounts[0].Options[0] = "rw"

		// Catches dropping the defensive copies. NRI owns these slices; writing
		// through them corrupts the runtime's own view of the container.
		require.Equal(t, []string{"PATH=/usr/bin"}, container.Env)
		require.Equal(t, []string{"rbind"}, container.Mounts[0].Options)
	})

	t.Run("tolerates nil pod and nil container", func(t *testing.T) {
		t.Parallel()
		// Catches a nil dereference. NRI may deliver either as nil, and a panic
		// in CreateContainer takes the plugin down and stops all injection.
		require.NotPanics(t, func() {
			result := fromNRI(nil, nil)
			require.Empty(t, result.Namespace)
			require.Empty(t, result.Env)
			require.Empty(t, result.Mounts)
		})

		podOnly := fromNRI(&api.PodSandbox{Namespace: "default"}, nil)
		require.Equal(t, "default", podOnly.Namespace)
		require.Empty(t, podOnly.Env)
	})
}

func TestToNRI(t *testing.T) {
	t.Parallel()

	t.Run("maps mounts and env", func(t *testing.T) {
		t.Parallel()
		adjustment := nvmlmock.Adjustment{
			Mounts: []nvmlmock.Mount{{
				Source:      "/var/lib/nvml-mock",
				Destination: "/opt/nvml-mock",
				Type:        "bind",
				Options:     []string{"rbind", "ro"},
			}},
			Env: []string{"MOCK_NVML_CONFIG=/opt/nvml-mock/driver/config/config.yaml"},
		}

		result, err := toNRI(adjustment)
		require.NoError(t, err)

		require.Len(t, result.Mounts, 1)
		require.Equal(t, "/var/lib/nvml-mock", result.Mounts[0].Source)
		require.Equal(t, "/opt/nvml-mock", result.Mounts[0].Destination)
		require.Equal(t, "bind", result.Mounts[0].Type)
		require.Equal(t, []string{"rbind", "ro"}, result.Mounts[0].Options)

		require.Len(t, result.Env, 1)
		require.Equal(t, "MOCK_NVML_CONFIG", result.Env[0].Key)
		require.Equal(t, "/opt/nvml-mock/driver/config/config.yaml", result.Env[0].Value)
	})

	t.Run("splits env on the first separator only", func(t *testing.T) {
		t.Parallel()
		result, err := toNRI(nvmlmock.Adjustment{
			Env: []string{"LD_PRELOAD=/a/lib.so:/b/lib.so", "PATH=/usr/bin=weird"},
		})
		require.NoError(t, err)

		// Catches splitting on every separator, which would truncate any value
		// containing "=" -- LD_PRELOAD chains and PATH entries are the risk.
		require.Len(t, result.Env, 2)
		require.Equal(t, "LD_PRELOAD", result.Env[0].Key)
		require.Equal(t, "/a/lib.so:/b/lib.so", result.Env[0].Value)
		require.Equal(t, "PATH", result.Env[1].Key)
		require.Equal(t, "/usr/bin=weird", result.Env[1].Value)
	})

	t.Run("drops env entries with no separator", func(t *testing.T) {
		t.Parallel()
		result, err := toNRI(nvmlmock.Adjustment{
			Env: []string{"MALFORMED", "PATH=/usr/bin"},
		})
		require.NoError(t, err)

		// Catches emitting a KeyValue with an empty key, which claims NRI
		// ownership of a variable that does not exist and can collide with
		// another plugin's adjustment.
		require.Len(t, result.Env, 1)
		require.Equal(t, "PATH", result.Env[0].Key)
	})

	t.Run("fails open per device and keeps the usable ones", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "nvidia1")
		result, err := toNRI(nvmlmock.Adjustment{
			Devices: []nvmlmock.Device{
				{HostPath: missing, Path: "/dev/nvidia1"},
				{HostPath: "/dev/null", Path: "/dev/nvidia0"},
			},
		})

		// Catches turning a per-device stat failure into a whole-container
		// error. One unstaged device node must not block container creation.
		require.NoError(t, err)
		require.NotNil(t, result.Linux)
		require.Len(t, result.Linux.Devices, 1)
		require.Equal(t, "/dev/nvidia0", result.Linux.Devices[0].Path)
	})

	t.Run("leaves Linux unset when no device is usable", func(t *testing.T) {
		t.Parallel()
		result, err := toNRI(nvmlmock.Adjustment{
			Devices: []nvmlmock.Device{
				{HostPath: filepath.Join(t.TempDir(), "nvidia0"), Path: "/dev/nvidia0"},
			},
		})
		require.NoError(t, err)
		require.Empty(t, result.Env)
		require.Empty(t, result.Mounts)
		require.Nil(t, result.Linux)
	})
}

func TestSplitCSV(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{name: "empty string yields nothing", value: "", want: nil},
		{name: "single item", value: "kube-system", want: []string{"kube-system"}},
		{
			name:  "several items",
			value: "kube-system,nvidia-system",
			want:  []string{"kube-system", "nvidia-system"},
		},
		{
			// Catches dropping the TrimSpace: a namespace of " nvidia-system"
			// never matches, so the plugin injects into a namespace an operator
			// asked it to skip.
			name:  "trims surrounding whitespace",
			value: " kube-system , nvidia-system ",
			want:  []string{"kube-system", "nvidia-system"},
		},
		{
			// Catches dropping the empty check: an empty entry becomes an empty
			// namespace or an empty LD_PRELOAD path, which breaks the chain.
			name:  "skips empty fields",
			value: ",kube-system,,nvidia-system,",
			want:  []string{"kube-system", "nvidia-system"},
		},
		{name: "only separators yields nothing", value: ",,,", want: nil},
		{name: "only whitespace yields nothing", value: "  ,  ", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, splitCSV(tt.value))
		})
	}
}

// envOr uses os.Getenv, so these cannot run with t.Parallel alongside t.Setenv.
func TestEnvOr(t *testing.T) {
	t.Run("returns the environment value when set", func(t *testing.T) {
		t.Setenv("NVML_MOCK_TEST_KEY", "/from/env")
		require.Equal(t, "/from/env", envOr("NVML_MOCK_TEST_KEY", "/fallback"))
	})

	t.Run("returns the fallback when unset", func(t *testing.T) {
		require.Equal(t, "/fallback", envOr("NVML_MOCK_TEST_UNSET_KEY", "/fallback"))
	})

	t.Run("treats an empty value as unset", func(t *testing.T) {
		t.Setenv("NVML_MOCK_TEST_KEY", "")

		// Catches switching to os.LookupEnv. An empty value must fall back:
		// Kubernetes materialises an unset optional env var as "", and an empty
		// overlay path would make the plugin bind-mount the container root.
		require.Equal(t, "/fallback", envOr("NVML_MOCK_TEST_KEY", "/fallback"))
	})
}
