// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package ib

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Readiness ordering.
//
// Simulator.Ready() feeds the node agent's /readyz (internal/agent.Readiness),
// and the only thing a consumer does with "the IB simulator is ready" is dial
// the mock-ib socket. Readiness must therefore never run ahead of the bind.
//
// The tests below spin on Ready() instead of polling it on an interval. A sleep
// between samples lets the daemon finish binding inside the gap, which is
// exactly the window under test: a test that waits for the socket to show up
// passes on the broken ordering too.

// spinUntilReady samples Ready() in a tight loop and reports whether it went
// true before timeout. It yields to the scheduler every 1024 samples so it
// cannot starve the daemon goroutine on a single-P runtime, but it never
// sleeps: the sampling interval is what gives this file its discrimination.
func spinUntilReady(s *Simulator, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for i := 0; ; i++ {
		if s.Ready() {
			return true
		}
		if i%1024 == 0 {
			runtime.Gosched()
			if time.Now().After(deadline) {
				return false
			}
		}
	}
}

// socketPathFor returns a short Unix socket path. t.TempDir() embeds the test
// name, and sockaddr_un.sun_path caps an address at 104 bytes on Darwin (108 on
// Linux), so the subtest names below would push the address over the limit and
// fail the bind with EINVAL: a confounded result that looks like the ordering
// bug but is not.
func socketPathFor(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ib")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, socketName)
}

// At the first instant Ready() reports true the socket has to accept a
// connection. One dial, no retry: retrying would hide a readiness flag that
// precedes the bind.
//
// Repeated because the assertion is a race the test has to win, not a state it
// can wait for. Each round is an independent observation of the same ordering,
// and one bad dial fails the test.
func TestReady_ImpliesTheSocketIsAlreadyBound(t *testing.T) {
	const rounds = 10
	for i := range rounds {
		t.Run(fmt.Sprintf("round-%d", i), func(t *testing.T) {
			isolateSources(t)
			h := newTestHost(t)
			socket := socketPathFor(t)
			s := New(h, Options{Mode: ModeFull, SocketPath: socket})

			require.NoError(t, s.Stage(t.Context(), testState(testNetwork())))
			require.False(t, s.Ready(), "full mode must not report ready before the daemon serves")

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runErr := make(chan error, 1)
			go func() { runErr <- s.Run(ctx) }()

			require.True(t, spinUntilReady(s, 10*time.Second), "daemon never became ready")

			c, err := net.Dial("unix", socket)
			require.NoError(t, err, "Ready() reported true before the daemon socket was bound")
			require.NoError(t, c.Close())

			cancel()
			select {
			case err := <-runErr:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not exit on context cancellation")
			}
		})
	}
}

// The same invariant from the failing side, and this one does not depend on
// winning a race: if the bind can never succeed, Ready() must stay false for
// the whole run however many times Run rebuilds the daemon.
//
// The socket path is occupied by a non-empty directory. os.Remove cannot clear
// it (ENOTEMPTY) and bind(2) refuses to overwrite an existing path, so every
// daemon generation fails at net.Listen. A plain file would not do: the daemon
// removes the path first, and the bind would then succeed.
func TestReady_StaysFalseWhenTheSocketCannotBeBound(t *testing.T) {
	isolateSources(t)
	h := newTestHost(t)
	socket := socketPathFor(t)
	require.NoError(t, os.MkdirAll(socket, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(socket, "occupant"), []byte("x"), 0o644))

	s := New(h, Options{Mode: ModeFull, SocketPath: socket})
	require.NoError(t, s.Stage(t.Context(), testState(testNetwork())))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(ctx) }()

	// Spans the first generation and the retry after serverRestartBackoff, so
	// the sampler is live across more than one failed bind.
	require.False(t, spinUntilReady(s, serverRestartBackoff+500*time.Millisecond),
		"Ready() reported true although the daemon socket never bound")

	// The bind really is impossible, so the guard above cannot pass vacuously.
	_, err := net.Dial("unix", socket)
	require.Error(t, err, "socket path must not be connectable in this test")

	cancel()
	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not exit on context cancellation")
	}
}
