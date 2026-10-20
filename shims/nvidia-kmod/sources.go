// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package nvidiakmod carries the simulated NVIDIA kernel modules and their
// loader, and the contract between the node agent, which stages them, and the
// NRI plugin, which hands them to the GPU Operator's driver container. The
// staged tree also carries the dynamic linker directive that has the container,
// and every operand that enters its root, load the mock NVML.
//
// What travels is source, not modules: a module loads only into the kernel it
// was compiled for, and that kernel is known only on the node. The loader
// builds the modules inside the driver container, against the kernel headers
// the container installs for its own build of the real driver.
package nvidiakmod

import (
	"embed"
	"fmt"
	"strings"
)

// Sources holds the module sources, the loader and the persistenced stand-in
// at their paths in this directory. embed drops file modes, so whoever stages
// them has to make Executables executable again.
//
//go:embed Kbuild Makefile load stand-ins/nvidia-persistenced src/*.c src/*.h
var Sources embed.FS

// LoaderName is the loader's path within Sources.
const LoaderName = "load"

// PersistencedName is the path within Sources of the stand-in for
// nvidia-persistenced, which the driver container runs in place of the daemon.
const PersistencedName = "stand-ins/nvidia-persistenced"

// Executables are the paths within Sources that are run directly.
var Executables = []string{LoaderName, PersistencedName}

const (
	// StagedDir is where the node agent stages Sources, relative to its
	// overlay root, together with GPUsFile, LdSoConfFile and ModprobeConfFile.
	StagedDir = "nvidia-kmod"
	// GPUsFile holds the mokka_gpus value the loader passes to nvidia.ko.
	GPUsFile = "gpus"
	// ModprobeConfFile holds the install directives ModprobeConf renders.
	ModprobeConfFile = "modprobe.conf"
	// LdSoConfFile holds the directive LdSoConf renders.
	LdSoConfFile = "ld.so.conf"

	// ContainerDir is where StagedDir appears inside the driver container.
	// It is fixed rather than configurable because the staged install
	// directives name it.
	ContainerDir = "/opt/mokka/nvidia-kmod"
	// ContainerStandInDir is the directory of ContainerDir that goes first on
	// the driver container's PATH, so that PersistencedName shadows the
	// daemon.
	ContainerStandInDir = ContainerDir + "/stand-ins"
	// ContainerModprobeConf is where the directives appear inside the driver
	// container: a directory modprobe reads, under a name no NVIDIA component
	// writes.
	ContainerModprobeConf = "/etc/modprobe.d/mokka-nvidia.conf"

	// ContainerNVMLDir is where the driver container serves the mock NVML:
	// the first directory the GPU Operator's operands search a driver root
	// for the library.
	ContainerNVMLDir = "/usr/lib64"
	// ContainerLdSoConf is where the directive appears inside the driver
	// container. ldconfig reads the directory in name order and keeps the
	// first library it finds under each name, so this name sorts ahead of the
	// distribution's own.
	ContainerLdSoConf = "/etc/ld.so.conf.d/000-mokka-nvml.conf"
)

// Modules are the modules the loader stands in for, named as modprobe
// normalizes them.
var Modules = []string{"nvidia", "nvidia_uvm", "nvidia_modeset", "nvidia_peermem"}

// ModprobeConf renders the install directives that make modprobe run the
// loader in dir instead of inserting the real modules. modprobe substitutes
// $CMDLINE_OPTS with the options its configuration and command line give the
// module.
func ModprobeConf(dir string) string {
	var b strings.Builder

	b.WriteString("# Rendered by the Mokka node agent: modprobe runs the loader\n" +
		"# instead of inserting the real NVIDIA modules.\n")
	for _, module := range Modules {
		fmt.Fprintf(&b, "install %s %s/%s %s $CMDLINE_OPTS\n", module, dir, LoaderName, module)
	}

	return b.String()
}

// LdSoConf renders the directive that has ldconfig index dir ahead of the
// distribution's library directories. The driver installer writes the real
// NVML and then rebuilds the cache with ldconfig, after which
// libnvidia-ml.so.1 resolves to the mock in dir for every process that loads
// it through the container's root.
func LdSoConf(dir string) string {
	return "# Rendered by the Mokka node agent: the mock NVML takes precedence\n" +
		"# over the library the driver installer writes.\n" +
		dir + "\n"
}

// GPU is one simulated GPU, as nvidia.ko's mokka_gpus parameter describes it.
type GPU struct {
	// BusID is the PCI location, dddd:bb:ss.f.
	BusID string
	UUID  string
	Model string
	Minor int
	// DeviceID is the PCI device ID, without the vendor half.
	DeviceID uint16
}

// Records renders gpus as the mokka_gpus value: comma-separated
// "<bdf>|<uuid>|<model>|<minor>|<pci-device-id>" records.
func Records(gpus []GPU) string {
	records := make([]string, 0, len(gpus))
	for _, gpu := range gpus {
		records = append(records, fmt.Sprintf("%s|%s|%s|%d|%04x",
			strings.ToLower(gpu.BusID), gpu.UUID, gpu.Model, gpu.Minor, gpu.DeviceID))
	}

	return strings.Join(records, ",")
}
