// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Command render-ib-sysfs reads the `infiniband:` block from a mock-nvml
// profile YAML and writes a fake InfiniBand sysfs tree under --output.
//
// Pair with libibmocksys.so (LD_PRELOAD) so that real userspace tooling
// (ibstat, ibstatus, iblinkinfo, libibverbs consumers, ...) reads from the
// rendered tree instead of /sys directly.
//
// Usage:
//
//	render-ib-sysfs \
//	    --config /config/config.yaml \
//	    --gpu-count "$GPU_COUNT" \
//	    --node-name "$NODE_NAME" \
//	    --output /var/lib/nvml-mock
package main

import (
	"flag"
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockibsysfs/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockibsysfs/render"
)

func main() {
	var (
		cfgPath   = flag.String("config", "", "path to mock-nvml profile YAML containing an `infiniband:` block")
		gpuCount  = flag.Int("gpu-count", 0, "number of GPUs (used with hcas_per_gpu when hca_count is unset)")
		nodeName  = flag.String("node-name", "", "node name (expanded into node_desc_template)")
		outDir    = flag.String("output", "", "fake-root directory; tree is written under <output>/sys/class/...")
		printOnly = flag.Bool("dry-run", false, "validate the config and exit without writing files")
	)
	flag.Parse()

	if *cfgPath == "" || *outDir == "" {
		fmt.Fprintln(os.Stderr, "usage: render-ib-sysfs --config <yaml> --output <dir> [--gpu-count N] [--node-name NAME] [--dry-run]")
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
	if !prof.Infiniband.Enabled {
		fmt.Fprintf(os.Stderr, "infiniband.enabled=false in %s, nothing to render\n", *cfgPath)
		return
	}
	if *printOnly {
		fmt.Fprintf(os.Stderr, "infiniband config OK: %+v\n", prof.Infiniband.Defaults())
		return
	}
	if err := render.Render(render.Options{
		IB:       prof.Infiniband,
		GPUCount: *gpuCount,
		NodeName: *nodeName,
		Output:   *outDir,
	}); err != nil {
		fatalf("render: %v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "render-ib-sysfs: "+format+"\n", args...)
	os.Exit(1)
}
