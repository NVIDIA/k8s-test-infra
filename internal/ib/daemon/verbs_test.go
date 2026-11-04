// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/NVIDIA/k8s-test-infra/internal/ib/config"
	"github.com/NVIDIA/k8s-test-infra/internal/ib/sysfs"
	"github.com/stretchr/testify/require"
)

func TestSynthesizeVerbsWrite_QueryBounds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	portDir := filepath.Join(root, "sys/class/infiniband/mlx5_0/ports/1")
	require.NoError(t, os.MkdirAll(filepath.Join(portDir, "gids"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(portDir, "lid"), []byte("0x1234\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(portDir, "gids/0"), []byte("fe80:0000:0000:0000:0000:0000:0000:1234\n"), 0o644))
	srv := &Server{cfg: Config{IBRoot: root}}
	h := &verbsHandle{caName: "mlx5_0"}
	wantPort := make([]byte, 40)
	wantPort[0] = 4
	binary.LittleEndian.PutUint32(wantPort[4:8], 0x1234)
	wantPort[31] = 1

	for _, query := range []struct {
		name string
		cmd  uint32
		want []byte
	}{
		{"port", ibUVCmdQueryPort, wantPort},
		{"gid", ibUVCmdQueryGID, []byte(net.ParseIP("fe80::1234").To16())},
	} {
		for _, size := range []int{0, 7, 8, 11, 12, 13, 16, 24} {
			for _, selector := range []byte{0, 1} {
				t.Run(fmt.Sprintf("%s/len=%d/selector=%d", query.name, size, selector), func(t *testing.T) {
					t.Parallel()
					data := make([]byte, size)
					if size >= 4 {
						binary.LittleEndian.PutUint32(data, query.cmd)
					}
					if size > 12 {
						data[12] = selector
					}
					var resp []byte
					var err error
					require.NotPanics(t, func() { resp, err = srv.synthesizeVerbsWrite(h, data) })
					if size < 8 {
						require.EqualError(t, err, "verbs cmd too short")
						return
					}
					require.NoError(t, err)
					want := query.want
					if query.cmd == ibUVCmdQueryGID && size > 12 && selector != 0 {
						want = make([]byte, 16)
					}
					require.Equal(t, want, resp)
				})
			}
		}
	}
}

func TestSynthesizeVerbsWrite_QueryDevice(t *testing.T) {
	root := t.TempDir()

	err := sysfs.Render(sysfs.Options{
		IB:       config.Infiniband{Enabled: true},
		GPUCount: 1,
		NodeName: "n1",
		RootDir:  root,
	})
	require.NoError(t, err)

	srv, err := NewServer(Config{IBRoot: root})
	require.NoError(t, err, "server")

	h := &verbsHandle{caName: "mlx5_0"}
	cmd := make([]byte, 16)
	binary.LittleEndian.PutUint32(cmd[0:4], ibUVCmdQueryDevice)

	resp, err := srv.synthesizeVerbsWrite(h, cmd)
	require.NoError(t, err, "synthesize")
	require.GreaterOrEqual(t, len(resp), 132, "phys_port_cnt: len=%d", len(resp))
	require.Equal(t, byte(1), resp[131], "phys_port_cnt")
}
