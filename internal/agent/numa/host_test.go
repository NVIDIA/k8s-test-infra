// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package numa

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHostRestoreRetainsBackupsOnFailure(t *testing.T) {
	t.Parallel()
	for _, step := range []string{
		"systemctl stop kubelet",
		"copy-kubelet-config.yaml",
		"copy-cpu_manager_state",
		"copy-memory_manager_state",
		"systemctl disable --now mokka-numa-filesystem.service",
		"unmount",
		"systemctl daemon-reload",
		"systemctl restart kubelet",
		"healthcheck",
	} {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			base, output, err := runRestoreFixture(t, step)
			require.Error(t, err, string(output))
			require.Contains(t, string(output), "restore failed")
			root := filepath.Join(base, "var/lib/nvml-mock/numa")
			for _, state := range []string{"kubelet-config.yaml", "cpu_manager_state", "memory_manager_state"} {
				data, readErr := os.ReadFile(filepath.Join(root, "original", state))
				require.NoError(t, readErr)
				require.Equal(t, "original "+state, string(data))
			}
			require.FileExists(t, filepath.Join(root, "applied"))
		})
	}
}

func TestHostRestoreRemovesBackupsOnlyAfterSuccess(t *testing.T) {
	t.Parallel()
	base, output, err := runRestoreFixture(t, "")
	require.NoError(t, err, string(output))
	for _, state := range []string{"config.yaml", "cpu_manager_state", "memory_manager_state"} {
		original := state
		if state == "config.yaml" {
			original = "kubelet-config.yaml"
		}
		data, readErr := os.ReadFile(filepath.Join(base, "var/lib/kubelet", state))
		require.NoError(t, readErr)
		require.Equal(t, "original "+original, string(data))
	}
	root := filepath.Join(base, "var/lib/nvml-mock/numa")
	require.NoDirExists(t, filepath.Join(root, "original"))
	require.NoFileExists(t, filepath.Join(root, "applied"))
}

// Run the actual restore function in its rollback conditional context. Redirect
// every host path and stub service/mount commands so no host setup is changed.
func runRestoreFixture(t *testing.T, failStep string) (string, []byte, error) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "var/lib/nvml-mock/numa")
	write := func(path, value string, mode os.FileMode) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(value), mode))
	}
	for _, state := range []string{"kubelet-config.yaml", "cpu_manager_state", "memory_manager_state"} {
		write(filepath.Join(root, "original", state), "original "+state, 0644)
	}
	write(filepath.Join(root, "applied"), "active", 0644)
	require.NoError(t, os.MkdirAll(filepath.Join(base, "var/lib/kubelet"), 0755))
	write(filepath.Join(base, "sys/devices/system/node/.mokka-numa"), "mokka-numa-v1", 0644)
	write(filepath.Join(base, "etc/systemd/system/mokka-numa-filesystem.service"), "unit", 0644)
	write(filepath.Join(base, "etc/systemd/system/kubelet.service.d/30-mokka-numa.conf"), "dependency", 0644)

	bin := filepath.Join(base, "bin")
	const stub = `#!/bin/bash
set -eu
command=${0##*/}
step="$command $*"
case "$command" in
  cp) step="copy-${1##*/}" ;;
  umount) step=unmount ;;
  curl) step=healthcheck ;;
esac
if [[ -n "$FAIL_STEP" && "$step" == "$FAIL_STEP" ]]; then exit 31; fi
case "$command" in
  cp) exec "$REAL_CP" "$@" ;;
  rm) exec "$REAL_RM" "$@" ;;
esac
exit 0
`
	for _, command := range []string{"systemctl", "umount", "cp", "rm", "curl", "sleep"} {
		write(filepath.Join(bin, command), stub, 0755)
	}
	realCP, err := exec.LookPath("cp")
	require.NoError(t, err)
	realRM, err := exec.LookPath("rm")
	require.NoError(t, err)
	definitions, _, found := strings.Cut(string(hostScript), "\ncase \"$action\" in")
	require.True(t, found)
	for _, path := range []string{"/var/lib/nvml-mock/numa", "/var/lib/kubelet", "/sys/devices/system/node", "/etc/systemd/system"} {
		definitions = strings.ReplaceAll(definitions, path, filepath.Join(base, path))
	}
	script := filepath.Join(base, "restore.sh")
	write(script, definitions+"\nrestore || { echo 'restore failed' >&2; exit 23; }\n", 0755)
	cmd := exec.CommandContext(t.Context(), "bash", script, "restore", "false")
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "FAIL_STEP="+failStep, "REAL_CP="+realCP, "REAL_RM="+realRM)
	output, err := cmd.CombinedOutput()
	return base, output, err
}
