// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package infiniband

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	ibconfig "github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/render"
)

// Source paths inside the container image. Package vars so tests can exercise
// both the present and absent branches without depending on the host.
var (
	// toolStageRoot holds ELF tools pre-patched at image build time by
	// stage-ib-tools.sh: each carries RPATH=$ORIGIN/../lib64 with its shared
	// libraries alongside, so an injected pod resolves libibmad/libibumad/
	// libibverbs/libnl from the overlay itself rather than from the image.
	toolStageRoot = "/usr/local/nvml-mock-ib"
	shimGlob      = "/usr/local/lib/libibmock*.so*"
	verbsConfDir  = "/etc/libibverbs.d"
	checkFabric   = "/usr/local/bin/check-fabric"
)

// fallbackTools are looked up on PATH when the image did not pre-stage them.
// In practice this is ibstatus, a /bin/sh script with no RPATH to give it.
var fallbackTools = []string{
	"ibnetdiscover", "ibstat", "iblinkinfo", "ibstatus", "sminfo", "ibping", "ibv_devinfo",
}

// ibRoot returns the rendered sysfs tree. It is the same directory the chart
// exposes as MOCK_IB_ROOT: the DaemonSet mounts one hostPath at both
// /host/var/lib/nvml-mock and /var/lib/nvml-mock.
func ibRoot(h *host.Host) string { return filepath.Join(h.Root, "ib") }

// stageSysfs renders the fake IB sysfs tree real tools read through
// libibmocksys.so's path redirection.
func stageSysfs(h *host.Host, state *agent.State) error {
	return render.Render(render.Options{
		IB:       buildIB(state.NodeShape.Network),
		NodeName: state.Node.NodeName,
		Output:   ibRoot(h),
	})
}

// buildIB maps the compiled NetworkShape back onto the renderer's schema.
// HCACount is already resolved, so it goes in as an explicit override and
// Options.GPUCount is left unused.
func buildIB(n agent.NetworkShape) ibconfig.Infiniband {
	return ibconfig.Infiniband{
		Enabled:          n.IBEnabled,
		HCAType:          n.HCAType,
		FWVersion:        n.FWVersion,
		HWRev:            n.HWRev,
		BoardID:          n.BoardID,
		NodeDescTemplate: n.NodeDescTemplate,
		LinkLayer:        n.LinkLayer,
		RateGbps:         n.RateGbps,
		PortState:        n.PortState,
		PhysState:        n.PhysState,
		HCACountOverride: n.HCACount,
		GUIDPrefix:       n.GUIDPrefix,
	}
}

// stageIBTools copies the pre-patched tools and their shared libraries into the
// overlay the NRI plugin bind-mounts into workloads. Without the libraries
// alongside, the first tool an injected pod runs dies on "error while loading
// shared libraries" — neither distroless nor ubuntu:22.04 ships the IB stack.
// See NVIDIA/k8s-test-infra#438.
func stageIBTools(h *host.Host) error {
	binDir := filepath.Join(h.Root, "driver/usr/bin")
	libDir := filepath.Join(h.Root, "driver/usr/lib64")

	staged, err := copyTree(h, filepath.Join(toolStageRoot, "bin"), binDir)
	if err != nil {
		return err
	}
	if _, err := copyTree(h, filepath.Join(toolStageRoot, "lib64"), libDir); err != nil {
		return err
	}

	// Never overwrite a pre-staged tool: the PATH copy has no RPATH.
	for _, tool := range fallbackTools {
		if staged[tool] {
			continue
		}
		src, err := exec.LookPath(tool)
		if err != nil {
			continue
		}
		if err := h.CopyFile(src, filepath.Join(binDir, tool), 0o755); err != nil {
			return fmt.Errorf("stage %s: %w", tool, err)
		}
	}
	return nil
}

// stageIBShims installs the LD_PRELOAD shims that forward UMAD and verbs traffic
// to the mock-ib daemon and redirect sysfs reads into ibRoot.
func stageIBShims(h *host.Host) error {
	return copyGlob(h, shimGlob, filepath.Join(h.Root, "driver/usr/local/lib"))
}

// stageVerbsConfig carries the ibverbs provider set along with the tools it
// belongs to. libibverbs reads /etc/libibverbs.d from a compile-time absolute
// path with no env override, so this copy does not redirect an injected
// container's lookup — the mock answers verbs through libibmockverbs.so.1 and
// never consults it. It is here for consumers that mount the overlay as a root.
func stageVerbsConfig(h *host.Host) error {
	_, err := copyTree(h, verbsConfDir, filepath.Join(h.Root, "driver/etc/libibverbs.d"))
	return err
}

// stageCheckFabric stages the fabric consumer so NRI-injected pods can verify
// their per-node ComputeDomain identity via nvmlDeviceGetGpuFabricInfo.
//
// TODO: check-fabric is an NVML fabric consumer rather than an IB tool; it lives
// here only because setup.sh staged it in the same block. Move it to gpudriver
// or nvlink once nvlink lands.
func stageCheckFabric(h *host.Host) error {
	if _, err := os.Stat(checkFabric); err != nil {
		return nil
	}
	return h.CopyFile(checkFabric, filepath.Join(h.Root, "driver/usr/bin/check-fabric"), 0o755)
}

// copyTree copies srcDir's regular files into dstDir, returning the base names
// it wrote. A missing srcDir is not an error: not every image pre-stages one.
func copyTree(h *host.Host, srcDir, dstDir string) (map[string]bool, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", srcDir, err)
	}
	if err := h.MkdirAll(dstDir, 0o755); err != nil {
		return nil, err
	}

	written := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if err := h.CopyFile(filepath.Join(srcDir, name), filepath.Join(dstDir, name), 0o755); err != nil {
			return nil, fmt.Errorf("stage %s: %w", name, err)
		}
		written[name] = true
	}
	return written, nil
}

// copyGlob copies every match of pattern into dstDir. No matches is not an
// error: the shims are absent from images built without them.
func copyGlob(h *host.Host, pattern, dstDir string) error {
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		return nil
	}
	if err := h.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	for _, src := range matches {
		dst := filepath.Join(dstDir, filepath.Base(src))
		if err := h.CopyFile(src, dst, 0o755); err != nil {
			return fmt.Errorf("stage %s: %w", filepath.Base(src), err)
		}
	}
	return nil
}
