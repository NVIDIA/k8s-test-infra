// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/stretchr/testify/require"
)

func hostFixture(t *testing.T, configure ...func(*hostOperations)) (*hostSetup, *[]string) {
	t.Helper()
	h := host.New(t.TempDir())
	for path, data := range map[string]string{
		h.RootPath("numa/desired"):                       "layout\n",
		h.RootPath("numa/sysnode/online"):                "0-1\n",
		h.RootPath("numa/kubelet-config.yaml"):           "simulated config",
		h.RootPath("kubelet/mokka-kubelet-helper"):       "helper",
		h.HostPath(kubeletConfig):                        "original config",
		h.HostPath("/var/lib/kubelet/cpu_manager_state"): "original cpu",
	} {
		require.NoError(t, fsutil.Write(path, []byte(data), 0600))
	}
	require.NoError(t, os.MkdirAll(h.RootPath("numa/sysbusnode"), 0700))
	ops, calls := fakeHostOperations()
	for _, change := range configure {
		change(&ops)
	}
	return newHostSetup(h, ops), calls
}

func fakeHostOperations() (hostOperations, *[]string) {
	calls := []string{}
	mounts := map[string]string{}
	return hostOperations{
		run: func(_ context.Context, name string, args ...string) error {
			calls = append(calls, name+" "+strings.Join(args, " "))
			if name == "mount" && args[0] == "--bind" {
				mounts[args[2]] = args[1]
			}
			if name == "mount" && args[0] == "--move" {
				mounts[args[2]] = mounts[args[1]]
				delete(mounts, args[1])
			}
			if name == "umount" {
				delete(mounts, args[0])
			}
			return nil
		},
		mounted:     func(path string) (bool, error) { _, ok := mounts[path]; return ok, nil },
		mountedFrom: func(source, target string) (bool, error) { return mounts[target] == source, nil },
		waitHealthy: func(context.Context) error { calls = append(calls, "healthcheck"); return nil },
	}, &calls
}

func TestHostApplyMountsConfigWithoutChangingOriginalAndRestores(t *testing.T) {
	t.Parallel()
	s, calls := hostFixture(t)
	h := s.host
	require.NoError(t, s.apply(t.Context()))
	requireFileContent(t, h.RunPath("mokka/numa/original/kubelet-config.yaml"), "original config")
	requireFileContent(t, h.RunPath("mokka/numa/original/cpu_manager_state"), "original cpu")
	require.FileExists(t, h.RunPath("mokka/numa/original/memory_manager_state.absent"))
	requireFileContent(t, h.HostPath(kubeletConfig), "original config")
	require.NoFileExists(t, h.HostPath("/var/lib/kubelet/cpu_manager_state"))
	requireFileContent(t, h.RunPath(appliedRel), "layout\n")
	mounted, err := s.currentMounts()
	require.NoError(t, err)
	require.True(t, mounted)
	// Read-only remounts do not propagate: each mount must be read-only
	// before it is moved onto a host volume.
	for _, target := range []string{h.SysPath("devices/system/node"), h.SysPath("bus/node/devices"), h.HostPath(kubeletConfig)} {
		found := false
		for i, call := range *calls {
			if strings.HasPrefix(call, "mount --move ") && strings.HasSuffix(call, " "+target) {
				stage := strings.TrimSuffix(strings.TrimPrefix(call, "mount --move "), " "+target)
				require.Contains(t, (*calls)[:i], "mount -o remount,bind,ro "+stage)
				found = true
			}
		}
		require.True(t, found, "mount must be published at %s", target)
	}
	service, err := os.ReadFile(h.EtcPath("systemd/system/mokka-kubelet-reset.service"))
	require.NoError(t, err)
	require.Contains(t, string(service), "ExecStart=/var/lib/nvml-mock/kubelet/mokka-kubelet-helper prestart")
	require.NotContains(t, string(service), " apply")
	require.NoError(t, fsutil.Write(h.HostPath("/var/lib/kubelet/memory_manager_state"), []byte("simulated memory"), 0600))
	*calls = nil
	require.NoError(t, s.apply(t.Context()))
	require.Empty(t, *calls, "an ordinary reconcile must not restart kubelet or delete checkpoints")
	require.FileExists(t, h.HostPath("/var/lib/kubelet/memory_manager_state"))
	require.NoError(t, s.restore(t.Context()))
	requireFileContent(t, h.HostPath(kubeletConfig), "original config")
	requireFileContent(t, h.HostPath("/var/lib/kubelet/cpu_manager_state"), "original cpu")
	require.NoFileExists(t, h.HostPath("/var/lib/kubelet/memory_manager_state"))
	require.NoDirExists(t, h.RunPath("mokka/numa/original"))
	require.NoFileExists(t, h.RunPath(appliedRel))
	require.NoFileExists(t, h.RootPath("kubelet/boot-reset.json"))
	require.NoFileExists(t, h.EtcPath("systemd/system/kubelet.service.d/30-mokka-reset.conf"))
	require.NoFileExists(t, h.EtcPath("systemd/system/mokka-kubelet-reset.service"))
	mounted, err = s.currentMounts()
	require.NoError(t, err)
	require.False(t, mounted)
	require.Contains(t, *calls, "umount "+h.HostPath(kubeletConfig))
	require.Contains(t, *calls, "umount "+h.SysPath("bus/node/devices"))
	require.Contains(t, *calls, "umount "+h.SysPath("devices/system/node"))
	require.Contains(t, *calls, "systemctl start kubelet")
}

func TestHostRestoreRetainsBackupsOnFailure(t *testing.T) {
	t.Parallel()
	for _, step := range []string{"systemctl stop kubelet", "copy-cpu", "copy-memory", "umount", "systemctl daemon-reload", "systemctl start kubelet", "healthcheck"} {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			restoring := false
			s, _ := hostFixture(t, func(ops *hostOperations) {
				run := ops.run
				ops.run = func(ctx context.Context, name string, args ...string) error {
					if restoring && (name+" "+strings.Join(args, " ") == step || (step == "umount" && name == "umount")) {
						return errors.New("injected command failure")
					}
					return run(ctx, name, args...)
				}
				healthy := ops.waitHealthy
				ops.waitHealthy = func(ctx context.Context) error {
					if restoring && step == "healthcheck" {
						return errors.New("injected health failure")
					}
					return healthy(ctx)
				}
			})
			h := s.host
			require.NoError(t, s.apply(t.Context()))
			restoring = true
			require.NoError(t, fsutil.Write(h.RunPath("mokka/numa/original/memory_manager_state"), []byte("original memory"), 0600))
			switch step {
			case "copy-cpu", "copy-memory":
				file := map[string]string{"copy-cpu": "cpu_manager_state", "copy-memory": "memory_manager_state"}[step]
				path := h.HostPath("/var/lib/kubelet/" + file)
				require.NoError(t, fsutil.Remove(path))
				require.NoError(t, os.Mkdir(path, 0700))
			}
			require.Error(t, s.restore(t.Context()))
			requireFileContent(t, h.RunPath("mokka/numa/original/cpu_manager_state"), "original cpu")
			requireFileContent(t, h.RunPath("mokka/numa/original/memory_manager_state"), "original memory")
			requireFileContent(t, h.RunPath(appliedRel), "layout\n")
			require.FileExists(t, h.RootPath("kubelet/boot-reset.json"))
		})
	}
}

func TestHostApplyFailureRollsBack(t *testing.T) {
	t.Parallel()
	for _, failRestore := range []bool{false, true} {
		t.Run(map[bool]string{false: "restored", true: "rollback also fails"}[failRestore], func(t *testing.T) {
			t.Parallel()
			starts := 0
			s, _ := hostFixture(t, func(ops *hostOperations) {
				original := ops.run
				ops.run = func(ctx context.Context, name string, args ...string) error {
					if name == "systemctl" && args[0] == "start" {
						starts++
						if starts == 1 {
							return errors.New("injected apply failure")
						}
						if failRestore {
							return errors.New("injected rollback failure")
						}
					}
					return original(ctx, name, args...)
				}
			})
			err := s.apply(t.Context())
			require.ErrorContains(t, err, "injected apply failure")
			requireFileContent(t, s.host.HostPath(kubeletConfig), "original config")
			require.NoFileExists(t, s.host.RunPath(appliedRel))
			if failRestore {
				require.ErrorContains(t, err, "injected rollback failure")
				require.FileExists(t, s.host.RunPath(pendingRel))
				require.DirExists(t, s.host.RunPath("mokka/numa/original"))
			} else {
				require.NoDirExists(t, s.host.RunPath("mokka/numa/original"))
			}
		})
	}
}

func TestHostMountFailureDoesNotPublishAndPreservesSource(t *testing.T) {
	t.Parallel()
	for _, failCleanup := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleanup succeeds", true: "cleanup fails"}[failCleanup], func(t *testing.T) {
			t.Parallel()
			var stage string
			s, _ := hostFixture(t, func(ops *hostOperations) {
				original := ops.run
				ops.run = func(ctx context.Context, name string, args ...string) error {
					if name == "mount" && args[0] == "--bind" {
						stage = args[2]
					}
					if name == "mount" && args[0] == "-o" {
						return errors.New("read-only remount failed")
					}
					if failCleanup && name == "umount" {
						return errors.New("staging unmount failed")
					}
					return original(ctx, name, args...)
				}
			})
			source := s.host.RootPath("numa/sysnode")
			target := s.host.SysPath("devices/system/node")
			err := s.mountReadOnly(t.Context(), source, target)
			require.ErrorContains(t, err, "read-only remount failed")
			mounted, mountErr := s.mounted(target)
			require.NoError(t, mountErr)
			require.False(t, mounted, "a writable mount must never reach the node")
			requireFileContent(t, filepath.Join(source, "online"), "0-1\n")
			require.NotEmpty(t, stage)
			if failCleanup {
				require.ErrorContains(t, err, "staging unmount failed")
				require.DirExists(t, stage, "do not delete a directory that still contains a mount")
				t.Cleanup(func() { require.NoError(t, os.RemoveAll(filepath.Dir(stage))) })
			} else {
				require.NoDirExists(t, filepath.Dir(stage))
			}
		})
	}
}

func TestHostRefusesForeignMount(t *testing.T) {
	t.Parallel()
	s, calls := hostFixture(t, func(ops *hostOperations) {
		ops.mounted = func(string) (bool, error) { return true, nil }
	})
	require.ErrorContains(t, s.mountTree(t.Context()), "unmanaged NUMA mount")
	require.ErrorContains(t, s.mountConfig(t.Context()), "unmanaged NUMA mount")
	require.ErrorContains(t, s.unmountOwned(t.Context(), s.host.RootPath("numa/sysnode"), s.host.SysPath("devices/system/node")), "unmanaged NUMA mount")
	require.Empty(t, *calls)
}

func requireFileContent(t *testing.T, path, value string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	require.NoError(t, err)
	require.Equal(t, value, string(data))
}

func TestWaitForKubeletHonorsCancellation(t *testing.T) {
	t.Parallel()
	s, _ := hostFixture(t, func(ops *hostOperations) {
		ops.run = func(context.Context, string, ...string) error { return errors.New("inactive") }
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, s.waitForKubelet(ctx), context.Canceled)
}

func TestPendingSetupRestartsKubeletBeforeReportingReady(t *testing.T) {
	t.Parallel()
	s, calls := hostFixture(t)
	require.NoError(t, s.apply(t.Context()))
	require.NoError(t, os.Rename(s.host.RunPath(appliedRel), s.host.RunPath(pendingRel)))
	*calls = nil
	require.NoError(t, s.apply(t.Context()))
	require.Contains(t, *calls, "systemctl start kubelet")
	require.Contains(t, *calls, "healthcheck")
	require.NoFileExists(t, s.host.RunPath(pendingRel))
	require.FileExists(t, s.host.RunPath(appliedRel))
}

func TestHostRequiresBothTopologyMounts(t *testing.T) {
	t.Parallel()
	s, _ := hostFixture(t)
	require.NoError(t, s.apply(t.Context()))
	require.NoError(t, s.run(t.Context(), "umount", s.host.SysPath("bus/node/devices")))
	complete, err := s.currentMounts()
	require.NoError(t, err)
	require.False(t, complete, "the bus view must not be missing from an applied topology")
}

func TestFailedSetupBeforeBackupRestartsKubelet(t *testing.T) {
	t.Parallel()
	failed := false
	s, calls := hostFixture(t, func(ops *hostOperations) {
		original := ops.run
		ops.run = func(ctx context.Context, name string, args ...string) error {
			if !failed && name == "systemctl" && args[0] == "stop" && args[1] == "kubelet" {
				failed = true
				return errors.New("setup interrupted before backup")
			}
			return original(ctx, name, args...)
		}
	})
	require.ErrorContains(t, s.apply(t.Context()), "setup interrupted before backup")
	require.Contains(t, *calls, "systemctl start kubelet")
	require.Contains(t, *calls, "healthcheck")
	requireFileContent(t, s.host.HostPath("/var/lib/kubelet/cpu_manager_state"), "original cpu")
	require.NoFileExists(t, s.host.RunPath(pendingRel))
	require.NoDirExists(t, s.host.RunPath("mokka/numa/original"))
}
