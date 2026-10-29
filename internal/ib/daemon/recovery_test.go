// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type panickingConn struct{ net.Conn }

func (panickingConn) Read([]byte) (int, error) {
	panic("injected connection panic")
}

func TestServer_ConnectionPanic(t *testing.T) {
	t.Parallel()
	for _, transport := range []string{"client", "fabric"} {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			core, logs := observer.New(zap.ErrorLevel)
			srv := &Server{log: zap.New(core)}
			serve := srv.serveConn
			if transport == "fabric" {
				serve = srv.serveFabricConn
			}
			client, server := net.Pipe()
			t.Cleanup(func() {
				require.NoError(t, client.Close())
				require.NoError(t, server.Close())
			})
			require.NoError(t, client.SetReadDeadline(time.Now().Add(time.Second)))
			require.NotPanics(t, func() {
				serve(t.Context(), panickingConn{server})
			})
			_, err := client.Read(make([]byte, 1))
			require.ErrorIs(t, err, io.EOF, "the failed connection must be closed")
			entries := logs.All()
			require.Len(t, entries, 1)
			require.Equal(t, "dropping connection: panic", entries[0].Message)
			require.Equal(t, transport, entries[0].ContextMap()["transport"])
			require.Equal(t, "injected connection panic", entries[0].ContextMap()["panic"])
			require.Contains(t, entries[0].ContextMap()["stack"], "panickingConn.Read")
		})
	}
}
