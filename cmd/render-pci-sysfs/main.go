// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Command render-pci-sysfs reads the `pcie_topology:` block from a
// mock-nvml profile YAML and writes a fake PCI sysfs tree under --output.
//
// The tree carries what topology-aware schedulers need (symlinks under
// /sys/bus/pci/devices and numa_node under /sys/devices/pciDDDD:BB),
// matching the layout the k8s deviceattribute library expects when it
// resolves "PCIe root" for a GPU, plus the PCI identity attribute files
// (vendor, device, class, config, ...) that let `lspci` enumerate the mock
// GPUs. See pkg/system/mockpcisysfs/render for the layout details.
//
// Usage:
//
//	render-pci-sysfs \
//	    --config /etc/nvml-mock/config.yaml \
//	    --output /var/lib/nvml-mock
//
// When the profile omits `pcie_topology:` the renderer falls back to a
// flat single-root layout covering every device in `devices:`. Pass
// --strict to require an explicit topology block (CI-friendly).
package main

import (
	"flag"
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockpcisysfs/render"
)

// defaultDMISource is where the kernel exposes the node's SMBIOS identity.
// The rendered tree mirrors it because serving the tree means bind-mounting
// it over /sys/devices, which would otherwise hide the DMI directory that
// /sys/class/dmi/id resolves into.
const defaultDMISource = "/sys/class/dmi/id"

func main() {
	var (
		opts      options
		dmiSource = flag.String("dmi-source", defaultDMISource,
			"kernel DMI directory to mirror into the tree; empty mirrors nothing")
	)
	flag.StringVar(&opts.configPath, "config", "", "path to mock-nvml profile YAML")
	flag.StringVar(&opts.outputDir, "output", "", "fake-root directory; tree is written under <output>/sys/...")
	flag.BoolVar(&opts.strict, "strict", false, "fail if the profile does not declare `pcie_topology:`")
	flag.BoolVar(&opts.dryRun, "dry-run", false, "validate the config and exit without writing files")
	flag.Parse()
	opts.dmiSource = *dmiSource

	if opts.configPath == "" || opts.outputDir == "" {
		fmt.Fprintln(os.Stderr, "usage: render-pci-sysfs --config <yaml> --output <dir> [--strict] [--dry-run]")
		os.Exit(2)
	}

	if err := run(opts); err != nil {
		fatalf("%v", err)
	}
}

// options is the resolved command line.
type options struct {
	configPath string
	outputDir  string
	dmiSource  string
	strict     bool
	dryRun     bool
}

func run(o options) error {
	data, err := os.ReadFile(o.configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var prof config.Profile
	if err := yaml.Unmarshal(data, &prof); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if err := prof.Validate(); err != nil {
		return err
	}

	topo := prof.EffectiveTopology()
	if topo != nil && o.strict && prof.PCIeTopology == nil {
		return fmt.Errorf("--strict: profile %s does not declare `pcie_topology:`", o.configPath)
	}
	if o.dryRun {
		reportDryRun(o.configPath, topo)
		return nil
	}

	// A profile with no devices still goes through Render, rather than
	// returning here: a tree rendered from a previous profile is on disk and
	// still served, and Render is what clears it along with its completion
	// marker, so setup.sh's gate cannot flip on for devices this profile does
	// not declare.
	if topo == nil {
		fmt.Fprintf(os.Stderr, "render-pci-sysfs: no devices in %s, clearing any previously rendered tree\n", o.configPath)
	}
	if err := render.Render(render.Options{
		Topology:   topo,
		Identities: prof.DeviceIdentities(),
		Output:     o.outputDir,
		DMISource:  o.dmiSource,
	}); err != nil {
		return fmt.Errorf("render: %w", err)
	}
	return nil
}

func reportDryRun(configPath string, topo *config.PCIeTopology) {
	if topo == nil {
		fmt.Fprintf(os.Stderr, "render-pci-sysfs: no devices in %s, nothing to render — config OK\n", configPath)
		return
	}
	fmt.Fprintf(os.Stderr, "render-pci-sysfs: %d root complex(es), %d device(s) — config OK\n",
		len(topo.RootComplexes), countDevices(topo))
}

func countDevices(t *config.PCIeTopology) int {
	n := 0
	for _, rc := range t.RootComplexes {
		n += len(rc.Devices)
	}
	return n
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "render-pci-sysfs: "+format+"\n", args...)
	os.Exit(1)
}
