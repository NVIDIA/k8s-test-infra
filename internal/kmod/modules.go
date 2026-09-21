// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package kmod stages mock kernel module state, and mirrors the node's own
// modules beside it. The kernel publishes that state twice.
//
//	/proc/modules:
//	name    size      refcnt  used_by                                      state  address
//	nvidia  62312448  4       nvidia_uvm,nvidia_modeset,gdrdrv,nvidia_fs,  Live   0x0000000000000000
//
//	/sys/module/<name>/: coresize, refcnt, initstate, version, holders/
//
// lsmod lists the modules from /proc/modules, then reads their columns from
// /sys/module/<name>/.
package kmod

import (
	"fmt"
	"strings"
)

// Simulated module names. The GPU Operator validator greps lsmod for
// nvidia_fs, gdrdrv, nvidia_peermem and mlx5_core, and stats nvidia's refcnt.
// Nothing checks nvidia_uvm or nvidia_modeset; a real node loads them.
const (
	NVIDIA        = "nvidia"
	NVIDIAUVM     = "nvidia_uvm"
	NVIDIAModeset = "nvidia_modeset"
	NVIDIAFS      = "nvidia_fs"
	NVIDIAPeermem = "nvidia_peermem"
	GDRDrv        = "gdrdrv"
	MLX5Core      = "mlx5_core"
)

// ProcModulesRelPath and SysModuleRelPath are overlay paths.
const (
	ProcModulesRelPath = "proc/modules"
	SysModuleRelPath   = "sys/module"
)

// In-memory sizes, captured from a node with the real driver. The kernel serves
// these as coresize. They are not the size of the .ko file on disk. Every build
// reports its own numbers, so these are representative rather than exact.
const (
	nvidiaModuleSizeBytes        = 62312448
	nvidiaUVMModuleSizeBytes     = 3411968
	nvidiaModesetModuleSizeBytes = 2097152
	nvidiaFSModuleSizeBytes      = 245760
	nvidiaPeermemModuleSizeBytes = 16384
	gdrdrvModuleSizeBytes        = 262144
	mlx5CoreModuleSizeBytes      = 3145728
)

const initStateLive = "live"

const (
	procModulesState   = "Live"
	procModulesAddress = "0x0000000000000000"
)

// Module describes a kernel module.
type Module struct {
	Name string

	SizeBytes int

	Holders []string
	Version string
}

// Refcnt returns the number of holders.
func (m Module) Refcnt() int { return len(m.Holders) }

// Modules returns the simulated NVIDIA modules. ibEnabled adds the two that
// need a fabric, because nothing loads nvidia_peermem automatically.
func Modules(driverVersion string, ibEnabled bool) []Module {
	holders := []string{NVIDIAUVM, NVIDIAModeset, GDRDrv, NVIDIAFS}
	if ibEnabled {
		holders = append(holders, NVIDIAPeermem)
	}

	mods := make([]Module, 0, 7)
	mods = append(mods,
		Module{
			Name:      NVIDIA,
			SizeBytes: nvidiaModuleSizeBytes,
			Holders:   holders,
			Version:   driverVersion,
		},
		Module{Name: NVIDIAUVM, SizeBytes: nvidiaUVMModuleSizeBytes},
		Module{Name: NVIDIAModeset, SizeBytes: nvidiaModesetModuleSizeBytes},
		Module{Name: GDRDrv, SizeBytes: gdrdrvModuleSizeBytes},
		Module{Name: NVIDIAFS, SizeBytes: nvidiaFSModuleSizeBytes},
	)

	if !ibEnabled {
		return mods
	}

	// mlx5_core is held by mlx5_ib on a real node, and nothing reads that.
	return append(mods,
		Module{Name: NVIDIAPeermem, SizeBytes: nvidiaPeermemModuleSizeBytes},
		Module{Name: MLX5Core, SizeBytes: mlx5CoreModuleSizeBytes},
	)
}

// HostModules is the node's /proc/modules, read once. Render and ProcModules
// both read host presence from it, so the two surfaces agree.
type HostModules struct {
	// text goes back out unchanged, so a line this package cannot parse still
	// reaches the container.
	text   string
	byName map[string]hostModule
}

// ParseProcModules reads the host /proc/modules text once, for both surfaces.
func ParseProcModules(text string) HostModules {
	return HostModules{text: text, byName: loadedModules(text)}
}

// ProcModules appends a line for every simulated module the host does not load.
func ProcModules(host HostModules, mods []Module) string {
	var b strings.Builder
	b.WriteString(host.text)
	if host.text != "" && !strings.HasSuffix(host.text, "\n") {
		b.WriteString("\n")
	}

	for _, m := range mods {
		if _, ok := host.byName[m.Name]; ok {
			continue
		}
		fmt.Fprintf(&b, "%s %d %d %s %s %s\n",
			m.Name, m.SizeBytes, m.Refcnt(), usedBy(m.Holders), procModulesState, procModulesAddress)
	}

	return b.String()
}

type hostModule struct {
	sizeBytes    string
	refcnt       string
	holders      []string
	holdersKnown bool
}

func loadedModules(procModules string) map[string]hostModule {
	mods := make(map[string]hostModule)

	for line := range strings.SplitSeq(procModules, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		host := hostModule{sizeBytes: fields[1], refcnt: fields[2]}
		if len(fields) > 3 {
			host.holders = holdersOf(fields[3])
			host.holdersKnown = true
		}
		mods[fields[0]] = host
	}

	return mods
}

func holdersOf(deps string) []string {
	if deps == "-" {
		return nil
	}

	var holders []string
	for h := range strings.SplitSeq(deps, ",") {
		if h != "" {
			holders = append(holders, h)
		}
	}

	return holders
}

func usedBy(holders []string) string {
	if len(holders) == 0 {
		return "-"
	}

	return strings.Join(holders, ",") + ","
}
