// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package seccompnotify

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// handover sends what runc sends a listener: the container's state as JSON,
// with fds attached, and returns what receive made of it.
func handover(t *testing.T, names []string, fds []int) (int, specs.ContainerProcessState, error) {
	t.Helper()

	// A temporary directory of its own: t.TempDir's paths can exceed the
	// length limit of a socket path.
	dir, err := os.MkdirTemp("", "sn")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	listener, err := listen(filepath.Join(dir, "l.sock"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })

	go func() {
		conn, err := net.DialUnix("unix", nil, listener.Addr().(*net.UnixAddr))
		if err != nil {
			return
		}
		// The peer reads to EOF, so the close is what ends the handover; an
		// error closing shows up there as a failed receive.
		defer func() { _ = conn.Close() }()
		if names == nil {
			return // a probe: connects and sends nothing
		}
		// The goroutine cannot fail the test; a marshal or write error shows up
		// as a handover receive rejects, which the test asserts on.
		state, _ := json.Marshal(specs.ContainerProcessState{Version: specs.Version, Fds: names, Pid: 42, Metadata: "ns/k8s-driver-manager"})
		_, _, _ = conn.WriteMsgUnix(state, unix.UnixRights(fds...), nil)
	}()

	conn, err := listener.AcceptUnix()
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()
	return receive(conn)
}

// openFds opens n fds that the test owns, to hand over.
func openFds(t *testing.T, n int) []int {
	t.Helper()

	fds := make([]int, n)
	for i := range fds {
		fd, err := unix.Open(os.DevNull, unix.O_RDONLY|unix.O_CLOEXEC, 0)
		require.NoError(t, err)
		t.Cleanup(func() { _ = unix.Close(fd) })
		fds[i] = fd
	}
	return fds
}

func sameFile(t *testing.T, a, b int) bool {
	t.Helper()

	var sa, sb unix.Stat_t
	require.NoError(t, unix.Fstat(a, &sa))
	require.NoError(t, unix.Fstat(b, &sb))
	return sa.Dev == sb.Dev && sa.Ino == sb.Ino
}

func TestReceive_TakesTheSeccompFd(t *testing.T) {
	t.Parallel()

	sent := openFds(t, 1)

	fd, state, err := handover(t, []string{specs.SeccompFdName}, sent)

	require.NoError(t, err)
	defer func() { require.NoError(t, unix.Close(fd)) }()
	require.True(t, sameFile(t, fd, sent[0]))
	require.Equal(t, "ns/k8s-driver-manager", state.Metadata)
	require.Equal(t, 42, state.Pid)
}

func TestReceive_RejectsAnyOtherFd(t *testing.T) {
	t.Parallel()

	_, _, err := handover(t, []string{"other"}, openFds(t, 1))
	require.ErrorContains(t, err, specs.SeccompFdName)

	_, _, err = handover(t, []string{"other", specs.SeccompFdName}, openFds(t, 2))
	require.Error(t, err)
}

func TestCut_CutsAsTheKernelCopies(t *testing.T) {
	t.Parallel()

	s, err := cut([]byte("nvidia\x00junk"), 55)
	require.NoError(t, err)
	require.Equal(t, "nvidia", s)

	s, err = cut([]byte("abcdefgh"), 4)
	require.NoError(t, err)
	require.Equal(t, "abcd", s, "a longer name is cut to the limit")

	_, err = cut([]byte("abc"), 4)
	require.Error(t, err, "memory ends before the name or the limit")
}

// The NRI plugin checks the listener by connecting and sending nothing.
func TestReceive_TellsAProbeByEOF(t *testing.T) {
	t.Parallel()

	_, _, err := handover(t, nil, nil)

	require.ErrorIs(t, err, io.EOF)
}
