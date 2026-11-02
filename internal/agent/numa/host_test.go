// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/fsutil"
	"github.com/stretchr/testify/require"
)

func hostFixture(t *testing.T) (*hostSetup, *[]string) {
	t.Helper()
	h := host.New(t.TempDir())
	for path, data := range map[string]string{
		h.RootPath("numa/desired"):                       "layout\n",
		h.RootPath("numa/sysnode/online"):                "0-1\n",
		h.RootPath("numa/kubelet-config.yaml"):           "simulated config",
		h.HostPath("/var/lib/kubelet/config.yaml"):       "original config",
		h.HostPath("/var/lib/kubelet/cpu_manager_state"): "original cpu",
	} {
		require.NoError(t, fsutil.Write(path, []byte(data), 0o600))
	}
	s := newHostSetup(h)
	calls := []string{}
	s.run = func(_ context.Context, name string, args ...string) error {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil
	}
	s.mounted = func(string) (bool, error) { return false, nil }
	s.waitHealthy = func(context.Context) error { calls = append(calls, "healthcheck"); return nil }
	return s, &calls
}

func TestHostApplyAndRestore(t *testing.T) {
	t.Parallel()
	s, calls := hostFixture(t)
	require.NoError(t, s.apply(t.Context(), true))
	h := s.host
	requireFileContent(t, h.RootPath("numa/original/kubelet-config.yaml"), "original config")
	requireFileContent(t, h.RootPath("numa/original/cpu_manager_state"), "original cpu")
	require.FileExists(t, h.RootPath("numa/original/memory_manager_state.absent"))
	requireFileContent(t, h.HostPath("/var/lib/kubelet/config.yaml"), "simulated config")
	require.NoFileExists(t, h.HostPath("/var/lib/kubelet/cpu_manager_state"))
	requireFileContent(t, h.RootPath("numa/.applied"), "layout\n")
	requireFileContent(t, h.EtcPath("systemd/system/"+filesystemUnit), filesystemService)
	require.Contains(t, *calls, "mount --make-rprivate "+h.Sys)
	require.Contains(t, *calls, "mount --bind "+h.RootPath("numa/sysnode")+" "+h.SysPath("devices/system/node"))
	require.Contains(t, *calls, "mount -o remount,bind,ro "+h.SysPath("devices/system/node"))
	require.Contains(t, *calls, "healthcheck")
	// A simulated checkpoint must disappear if no checkpoint existed before setup.
	require.NoError(t, fsutil.Write(h.HostPath("/var/lib/kubelet/memory_manager_state"), []byte("simulated memory"), 0o600))
	require.NoError(t, fsutil.Write(h.SysPath("devices/system/node/.mokka-numa"), []byte("mokka-numa-v1\n"), 0o600))
	*calls = nil
	require.NoError(t, s.restore(t.Context(), false))
	requireFileContent(t, h.HostPath("/var/lib/kubelet/config.yaml"), "original config")
	requireFileContent(t, h.HostPath("/var/lib/kubelet/cpu_manager_state"), "original cpu")
	require.NoFileExists(t, h.HostPath("/var/lib/kubelet/memory_manager_state"))
	require.NoDirExists(t, h.RootPath("numa/original"))
	require.NoFileExists(t, h.RootPath("numa/.applied"))
	require.NoFileExists(t, h.EtcPath(kubeletDropin))
	require.NoFileExists(t, h.EtcPath("systemd/system/"+filesystemUnit))
	require.Contains(t, *calls, "umount "+h.SysPath("devices/system/node"))
	require.Contains(t, *calls, "systemctl restart kubelet")
	require.Equal(t, "healthcheck", (*calls)[len(*calls)-1])
}

func TestHostRestoreRetainsBackupsOnFailure(t *testing.T) {
	t.Parallel()
	for _, step := range []string{"systemctl stop kubelet", "copy-config", "copy-cpu", "copy-memory", "systemctl disable --now " + filesystemUnit, "umount", "systemctl daemon-reload", "systemctl restart kubelet", "healthcheck"} {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			s, _ := hostFixture(t)
			h := s.host
			require.NoError(t, s.backupKubelet())
			require.NoError(t, fsutil.Write(h.RootPath("numa/original/memory_manager_state"), []byte("original memory"), 0o600))
			require.NoError(t, fsutil.Write(h.RootPath("numa/.applied"), []byte("active"), 0o600))
			require.NoError(t, s.installUnits(t.Context()))
			require.NoError(t, fsutil.Write(h.SysPath("devices/system/node/.mokka-numa"), []byte("mokka-numa-v1"), 0o600))
			switch step {
			case "copy-config", "copy-cpu", "copy-memory":
				name := map[string]string{"copy-config": "config.yaml", "copy-cpu": "cpu_manager_state", "copy-memory": "memory_manager_state"}[step]
				path := h.HostPath("/var/lib/kubelet/" + name)
				require.NoError(t, fsutil.Remove(path))
				require.NoError(t, os.Mkdir(path, 0o700)) // An atomic copy cannot replace a directory.
			case "healthcheck":
				s.waitHealthy = func(context.Context) error { return errors.New("injected health failure") }
			default:
				s.run = func(_ context.Context, name string, args ...string) error {
					command := name + " " + strings.Join(args, " ")
					if command == step || (step == "umount" && name == "umount") {
						return errors.New("injected command failure")
					}
					return nil
				}
			}
			require.Error(t, s.restore(t.Context(), false))
			requireFileContent(t, h.RootPath("numa/original/kubelet-config.yaml"), "original config")
			requireFileContent(t, h.RootPath("numa/original/cpu_manager_state"), "original cpu")
			requireFileContent(t, h.RootPath("numa/original/memory_manager_state"), "original memory")
			requireFileContent(t, h.RootPath("numa/.applied"), "active")
		})
	}
}

func TestHostApplyFailureRollsBack(t *testing.T) {
	t.Parallel()
	for _, failRestore := range []bool{false, true} {
		t.Run(map[bool]string{false: "restored", true: "rollback also fails"}[failRestore], func(t *testing.T) {
			t.Parallel()
			s, _ := hostFixture(t)
			originalRun := s.run
			s.run = func(ctx context.Context, name string, args ...string) error {
				if name == "systemctl" && args[0] == "start" {
					return errors.New("injected apply failure")
				}
				if failRestore && name == "systemctl" && args[0] == "restart" {
					return errors.New("injected rollback failure")
				}
				return originalRun(ctx, name, args...)
			}
			err := s.apply(t.Context(), true)
			require.ErrorContains(t, err, "injected apply failure")
			requireFileContent(t, s.host.HostPath("/var/lib/kubelet/config.yaml"), "original config")
			require.NoFileExists(t, s.host.RootPath("numa/.applied"))
			if failRestore {
				require.ErrorContains(t, err, "injected rollback failure")
				require.DirExists(t, s.host.RootPath("numa/original"))
			} else {
				require.NoDirExists(t, s.host.RootPath("numa/original"))
			}
		})
	}
}

func TestHostUpgradesLegacyServiceWithoutRestartingKubelet(t *testing.T) {
	t.Parallel()
	s, calls := hostFixture(t)
	require.NoError(t, fsutil.Write(s.host.RootPath("numa/.applied"), []byte("layout\n"), 0o600))
	require.NoError(t, fsutil.Write(s.host.EtcPath("systemd/system/"+filesystemUnit), []byte("ExecStart=/bin/bash host.sh mount\n"), 0o644))
	require.NoError(t, s.apply(t.Context(), true))
	require.Equal(t, []string{"systemctl daemon-reload"}, *calls)
	require.NoDirExists(t, s.host.RootPath("numa/original"))
	requireFileContent(t, s.host.HostPath("/var/lib/kubelet/config.yaml"), "original config")
	requireFileContent(t, s.host.EtcPath("systemd/system/"+filesystemUnit), filesystemService)
}

func TestHostMountOnlyLeavesKubeletConfigAlone(t *testing.T) {
	t.Parallel()
	s, calls := hostFixture(t)
	require.NoError(t, s.apply(t.Context(), false))
	require.NoError(t, s.restore(t.Context(), false))
	require.NoDirExists(t, s.host.RootPath("numa/original"))
	requireFileContent(t, s.host.HostPath("/var/lib/kubelet/config.yaml"), "original config")
	for _, call := range *calls {
		require.NotContains(t, call, "stop kubelet")
		require.NotContains(t, call, "restart kubelet")
	}
}

func TestHostRefusesForeignMount(t *testing.T) {
	t.Parallel()
	s, calls := hostFixture(t)
	s.mounted = func(string) (bool, error) { return true, nil }
	require.ErrorContains(t, s.mountTree(t.Context()), "unmanaged NUMA mount")
	require.Empty(t, *calls)
	require.NoError(t, fsutil.Write(s.host.SysPath("devices/system/node/.mokka-numa"), []byte("mokka-numa-v1"), 0o600))
	require.NoError(t, s.mountTree(t.Context()))
	require.Empty(t, *calls)
}

func TestRunHostRejectsUnknownAction(t *testing.T) {
	t.Parallel()
	require.ErrorContains(t, RunHost(t.Context(), "invalid", false), "unknown NUMA host action")
}

func requireFileContent(t *testing.T, path, value string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	require.NoError(t, err)
	require.Equal(t, value, string(data))
}

func TestKubeletHealth(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz" {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.WriteHeader(status)
			}))
			defer server.Close()
			transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
			}}
			defer transport.CloseIdleConnections()
			err := kubeletHealth(t.Context(), &http.Client{Transport: transport})
			if status == http.StatusOK {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "503")
			}
		})
	}
}

func TestWaitForKubeletHonorsCancellation(t *testing.T) {
	t.Parallel()
	s, _ := hostFixture(t)
	s.run = func(context.Context, string, ...string) error { return errors.New("inactive") }
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, s.waitForKubelet(ctx), context.Canceled)
}

func TestHostRestoreClearsBothAppliedMarkers(t *testing.T) {
	t.Parallel()
	s, _ := hostFixture(t)
	require.NoError(t, s.apply(t.Context(), true))
	require.NoError(t, fsutil.Write(s.host.RootPath("numa/applied"), []byte("legacy state"), 0600))
	require.NoError(t, s.restore(t.Context(), false))
	require.NoFileExists(t, s.host.RootPath("numa/.applied"))
	require.NoFileExists(t, s.host.RootPath("numa/applied"))
	require.NoDirExists(t, s.host.RootPath("numa/original"))
	requireFileContent(t, s.host.HostPath("/var/lib/kubelet/config.yaml"), "original config")
}
