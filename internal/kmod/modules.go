// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package kmod stages mock kernel module state.
package kmod

import (
	"fmt"
	"strings"
)

// NVIDIA and NVIDIAUVM are simulated module names.
const (
	NVIDIA    = "nvidia"
	NVIDIAUVM = "nvidia_uvm"
)

// ProcModulesRelPath and SysModuleRelPath are overlay paths.
const (
	ProcModulesRelPath = "proc/modules"
	SysModuleRelPath   = "sys/module"
)

const (
	nvidiaCoreSize    = 62312448
	nvidiaUVMCoreSize = 3411968
)

const initStateLive = "live"

const (
	procModulesState   = "Live"
	procModulesAddress = "0x0000000000000000"
)

// Module describes a kernel module.
type Module struct {
	Name     string
	CoreSize int
	Holders  []string
	Version  string
}

// Refcnt returns the number of holders.
func (m Module) Refcnt() int { return len(m.Holders) }

// Modules returns the simulated NVIDIA modules.
func Modules(driverVersion string) []Module {
	return []Module{
		{
			Name:     NVIDIA,
			CoreSize: nvidiaCoreSize,
			Holders:  []string{NVIDIAUVM},
			Version:  driverVersion,
		},
		{
			Name:     NVIDIAUVM,
			CoreSize: nvidiaUVMCoreSize,
		},
	}
}

// ProcModules adds missing simulated modules to src.
func ProcModules(src string, mods []Module) string {
	loaded := loadedModules(src)

	var b strings.Builder
	b.WriteString(src)
	if src != "" && !strings.HasSuffix(src, "\n") {
		b.WriteString("\n")
	}

	for _, m := range mods {
		if _, ok := loaded[m.Name]; ok {
			continue
		}
		fmt.Fprintf(&b, "%s %d %d %s %s %s\n",
			m.Name, m.CoreSize, m.Refcnt(), usedBy(m.Holders), procModulesState, procModulesAddress)
	}

	return b.String()
}

type hostModule struct {
	coreSize     string
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

		host := hostModule{coreSize: fields[1], refcnt: fields[2]}
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
