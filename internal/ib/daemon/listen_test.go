// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/ib/config"
	"github.com/NVIDIA/k8s-test-infra/internal/ib/sysfs"
	"github.com/stretchr/testify/require"
)

// Listen exists so a supervisor can publish readiness on the far side of the
// bind, which only holds if a returned listener is already connectable. Serve is
// deliberately never called here: a dial that needs Serve to have started would
// put the readiness flip back on the wrong side of the window.
func TestListen_SocketIsConnectableBeforeServe(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, sysfs.Render(sysfs.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-a", RootDir: dir,
	}))
	// Short path under /tmp: macOS caps a unix socket address at 104 bytes.
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	sock := filepath.Join(os.TempDir(), "mock-ib-"+safe+".sock")
	t.Cleanup(func() { _ = os.Remove(sock) })

	srv, err := NewServer(Config{SocketPath: sock, IBRoot: dir})
	require.NoError(t, err)

	ln, err := srv.Listen()
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	conn, err := net.Dial("unix", sock)
	require.NoError(t, err, "Listen returned before the socket accepted connections")
	require.NoError(t, conn.Close())

	// 0666 because workload containers run as arbitrary UIDs. Checked here
	// rather than after Serve for the same reason as the dial above.
	info, err := os.Stat(sock)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o666), info.Mode().Perm())
}

// A failed bind must leave nothing behind for a caller to mistake for a live
// socket, and must report why. The path is occupied by a non-empty directory:
// os.Remove cannot clear it, so bind(2) fails on a path that still exists.
func TestListen_ReportsAFailedBind(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, sysfs.Render(sysfs.Options{
		IB: config.Infiniband{Enabled: true}, GPUCount: 2, NodeName: "node-a", RootDir: dir,
	}))
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	sock := filepath.Join(os.TempDir(), "mock-ib-"+safe+".sock")
	require.NoError(t, os.MkdirAll(sock, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sock, "occupant"), []byte("x"), 0o644))
	t.Cleanup(func() { _ = os.RemoveAll(sock) })

	srv, err := NewServer(Config{SocketPath: sock, IBRoot: dir})
	require.NoError(t, err)

	ln, err := srv.Listen()
	require.Error(t, err)
	require.Nil(t, ln)
	require.Contains(t, err.Error(), "listen unix "+sock)
}
