// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package dmi owns the system identity a node shows under
// /sys/devices/virtual/dmi/id. DMI (Desktop Management Interface) is the
// kernel's view of the firmware's SMBIOS tables; a kernel booted without them —
// an arm64 VM started from a device tree, Docker Desktop on Apple Silicon among
// them — exposes none, and readers such as NFD fail on every discovery pass.
package dmi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/NVIDIA/k8s-test-infra/internal/pcisysfs"
	"github.com/NVIDIA/k8s-test-infra/internal/sysattr"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/sysoverlay"
)

const (
	name      = "dmi"
	sysVendor = "NVIDIA"

	// relPath is the kernel's DMI directory relative to /sys; the attributes
	// live in its id subdirectory.
	relPath = "devices/virtual/dmi"
)

var (
	_ agent.Simulator = (*Simulator)(nil)
	_ agent.Applier   = (*Simulator)(nil)
)

// Simulator owns the identity in both places a reader finds it: the node's
// /sys/devices/virtual/dmi/id, simulated where the kernel shows none, and its
// copy in the rendered sysfs tree, which hides the node's from served
// containers.
type Simulator struct {
	// overlay adds DMI to the node's /sys.
	overlay sysoverlay.Overlay
	// kernel is the node's DMI id directory, as the kernel or overlay shows it.
	kernel string
	// served is the same directory inside the rendered sysfs tree, which the
	// CDI spec serves to GPU containers in place of /sys/devices.
	served string
	// node simulates the node's view; nil leaves it as the kernel shows it.
	node  *sysoverlay.Simulator
	ready atomic.Bool
}

// New returns the DMI simulator. simulate serves an identity on a node whose
// kernel exposes none; the served tree's copy is staged either way, since a
// container on a node with kernel DMI cannot start without it.
func New(h *host.Host, simulate bool) *Simulator {
	s := &Simulator{
		overlay: sysoverlay.At(h, relPath),
		kernel:  h.SysPath(relPath, "id"),
		served:  h.RootPath(pcisysfs.SysRelPath, relPath, "id"),
	}
	if simulate {
		s.node = sysoverlay.NewSimulator(name, s.overlay, stageNode)
	}
	return s
}

// Name returns the simulator's stable identifier.
func (s *Simulator) Name() string { return name }

// Ready reports whether both views are published.
func (s *Simulator) Ready() bool { return s.ready.Load() }

// Stage writes the node's simulated identity, if any, and the served copy.
func (s *Simulator) Stage(ctx context.Context, state *agent.State) error {
	s.ready.Store(false)

	if s.node != nil {
		if err := s.node.Stage(ctx, state); err != nil {
			return err
		}
	}

	if err := s.stageServed(state); err != nil {
		return fmt.Errorf("stage served dmi: %w", err)
	}
	return nil
}

// stageServed writes the identity a served container reads in place of the
// node's. It is decided from the kernel's view and the state alone, not from
// whether the node's overmount is up yet, so a served container sees the same
// identity from the first pass.
func (s *Simulator) stageServed(state *agent.State) error {
	if !state.HasPCITopology() {
		return os.RemoveAll(s.served)
	}

	v, err := s.overlay.Look()
	if err != nil {
		return err
	}

	switch {
	case v == sysoverlay.Kernel:
		return s.stageMountTargets()
	case s.node != nil:
		return sysattr.Write(s.served, identityOf(state).attributes(), 0o444)
	default:
		return os.RemoveAll(s.served)
	}
}

// stageMountTargets reproduces the kernel's product files, which kind's
// createContainer hook bind-mounts its own copies over in every container. A
// mount(8) target cannot be created on a read-only sysfs, so without these a
// served pod dies on "mount point does not exist".
//
// product_uuid identifies the node and the kernel shows it to root alone, so
// it is staged empty — a target to mount over, not a value.
func (s *Simulator) stageMountTargets() error {
	// Unreadable is not fatal: a mount target need not carry a value.
	product, _ := os.ReadFile(filepath.Join(s.kernel, "product_name"))

	return errors.Join(
		sysattr.Write(s.served, sysattr.Attributes{"product_name": strings.TrimSpace(string(product))}, 0o444),
		sysattr.Write(s.served, sysattr.Attributes{"product_uuid": ""}, 0o400),
	)
}

// Apply serves the node's simulated identity, if any.
func (s *Simulator) Apply(ctx context.Context, state *agent.State) error {
	s.ready.Store(false)

	if s.node != nil {
		if err := s.node.Apply(ctx, state); err != nil {
			return err
		}
	}

	s.ready.Store(true)
	return nil
}

// Revoke withdraws the node's simulated identity, if any.
func (s *Simulator) Revoke(ctx context.Context) error {
	zap.L().Info("revoking simulator", zap.String("simulator", name))
	s.ready.Store(false)

	if s.node == nil {
		return nil
	}
	return s.node.Revoke(ctx)
}

// Discard removes the served copy and the node's staged identity.
func (s *Simulator) Discard(ctx context.Context) error {
	zap.L().Info("discarding simulator", zap.String("simulator", name))

	var errs []error
	if s.node != nil {
		errs = append(errs, s.node.Discard(ctx))
	}
	if err := os.RemoveAll(s.served); err != nil {
		errs = append(errs, fmt.Errorf("remove served dmi: %w", err))
	}
	return errors.Join(errs...)
}

// identity is the system the DMI attributes describe.
type identity struct {
	vendor, product string
}

// identityOf names the machine after its first GPU, the same string
// writeMachineType gives GFD, so both name it alike, as GFD's DMI default does
// on real hardware.
func identityOf(state *agent.State) identity {
	id := identity{vendor: sysVendor}
	if len(state.Devices) > 0 {
		id.product = state.Devices[0].Name
	}
	return id
}

// attributes are the world-readable attributes NFD's system source reads.
// Vendor fields repeat the system vendor and name fields the product; the
// rest are empty, as a real board leaves many of them.
//
// product_uuid and the serials are deliberately absent: the kernel shows them
// to root alone, and kind's createContainer hook bind-mounts its own
// product_uuid into every container once the node shows one — a container's
// own sysfs has no target for it, so no container on the node would start.
func (id identity) attributes() sysattr.Attributes {
	return sysattr.Attributes{
		"sys_vendor":     id.vendor,
		"bios_vendor":    id.vendor,
		"board_vendor":   id.vendor,
		"chassis_vendor": id.vendor,

		"product_name": id.product,
		"board_name":   id.product,

		"bios_date":         "",
		"bios_version":      "",
		"board_asset_tag":   "",
		"board_version":     "",
		"chassis_asset_tag": "",
		"chassis_type":      "",
		"chassis_version":   "",
		"product_family":    "",
		"product_sku":       "",
		"product_version":   "",
	}
}

// stageNode writes the node's simulated identity into entry/id.
func stageNode(entry string, state *agent.State) error {
	if err := sysattr.Write(filepath.Join(entry, "id"), identityOf(state).attributes(), 0o444); err != nil {
		return fmt.Errorf("stage dmi: %w", err)
	}
	return nil
}
