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

func main() {
	var (
		cfgPath = flag.String("config", "", "path to mock-nvml profile YAML")
		outDir  = flag.String("output", "", "fake-root directory; tree is written under <output>/sys/...")
		strict  = flag.Bool("strict", false, "fail if the profile does not declare `pcie_topology:`")
		dryRun  = flag.Bool("dry-run", false, "validate the config and exit without writing files")
	)
	flag.Parse()

	if *cfgPath == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "usage: render-pci-sysfs --config <yaml> --output <dir> [--strict] [--dry-run]")
		os.Exit(2)
	}

	data, err := os.ReadFile(*cfgPath)
	if err != nil {
		fatalf("read config: %v", err)
	}
	var prof config.Profile
	if err := yaml.Unmarshal(data, &prof); err != nil {
		fatalf("parse config: %v", err)
	}
	if err := prof.Validate(); err != nil {
		fatalf("%v", err)
	}

	topo := prof.EffectiveTopology()
	if topo == nil {
		fmt.Fprintf(os.Stderr, "render-pci-sysfs: no devices in %s, nothing to render\n", *cfgPath)
		return
	}
	if *strict && prof.PCIeTopology == nil {
		fatalf("--strict: profile %s does not declare `pcie_topology:`", *cfgPath)
	}

	if *dryRun {
		fmt.Fprintf(os.Stderr, "render-pci-sysfs: %d root complex(es), %d device(s) — config OK\n",
			len(topo.RootComplexes), countDevices(topo))
		return
	}

	if err := render.Render(render.Options{
		Topology:   topo,
		Identities: prof.DeviceIdentities(),
		Output:     *outDir,
	}); err != nil {
		fatalf("render: %v", err)
	}
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
