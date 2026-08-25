// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockimex/render"
)

// stageChannelDevs creates the chardevs that the compute-domain CDI spec injects
// into workload pods; the nodes must exist for containerd to admit the container.
func stageChannelDevs(h *host.Host, state *agent.State) error {
	dir := filepath.Join(h.Root, "driver/dev/nvidia-caps-imex-channels")

	if err := h.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	major := uint32(state.IMEX.IMEXMajor) //nolint:gosec // bounded by IMEXState validation

	for i := range state.IMEX.ChannelCount {
		path := filepath.Join(dir, fmt.Sprintf("channel%d", i))
		if err := h.Mknod(path, major, uint32(i)); err != nil { //nolint:gosec // minor is a bounded loop index
			return fmt.Errorf("channel%d: %w", i, err)
		}
	}

	return nil
}

// stageProcDevices synthesises the substitute /proc/devices the DRA compute-domain
// plugin reads via ALT_PROC_DEVICES_PATH — without the entry the plugin aborts at startup.
func stageProcDevices(h *host.Host, state *agent.State, procDevicesPath string) error {
	src, err := os.ReadFile(procDevicesPath) //nolint:gosec // path is validated by caller

	if err != nil {
		return fmt.Errorf("read %s: %w", procDevicesPath, err)
	}

	rendered, err := render.ProcDevices(string(src), state.IMEX.IMEXMajor, state.IMEX.CapsMajor)

	if err != nil {
		return fmt.Errorf("render proc-devices: %w", err)
	}

	return h.WriteFile(filepath.Join(h.Root, "imex/proc-devices"), []byte(rendered), 0o644)
}

// stageFabricImexMgmt writes the capability file the DRA plugin reads alongside
// proc-devices to discover the fabric IMEX management device minor.
func stageFabricImexMgmt(h *host.Host) error {
	content := `DeviceFileMinor: 512
DeviceFileMode: 438
DeviceFileModify: 0
`
	p := filepath.Join(h.Root, "driver/proc/driver/nvidia/capabilities/fabric-imex-mgmt")
	return h.WriteFile(p, []byte(content), 0o644)
}
