// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// render-imex-procdevices writes a substitute /proc/devices carrying NVIDIA
// caps character-device entries, for consumption by the NVIDIA DRA driver's
// compute-domain kubelet plugin via ALT_PROC_DEVICES_PATH.
//
// On a Mokka node there is no NVIDIA kernel module, so /proc/devices has no
// nvidia-caps-imex-channels entry and the plugin aborts at startup. The DRA
// driver's chart exposes an altProcDevices value for exactly this case; this
// command produces the file it points at.
//
// Usage:
//
//	render-imex-procdevices \
//	    --source /proc/devices \
//	    --output /var/lib/nvml-mock/imex/proc-devices \
//	    --imex-major 235 --caps-major 236
//
// Rendering is idempotent, so the DaemonSet may re-run it on restart.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/NVIDIA/k8s-test-infra/pkg/system/mockimex/render"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "render-imex-procdevices: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("render-imex-procdevices", flag.ContinueOnError)
	source := fs.String("source", "/proc/devices", "path to the real /proc/devices to derive from")
	output := fs.String("output", "", "path to write the rendered file (required)")
	imexMajor := fs.Int("imex-major", 235, "device major to advertise for "+render.IMEXChannelsDeviceName)
	capsMajor := fs.Int("caps-major", 236, "device major to advertise for "+render.CapsDeviceName)

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *output == "" {
		return errors.New("--output is required")
	}

	data, err := os.ReadFile(*source) //nolint:gosec // operator-supplied source path
	if err != nil {
		return fmt.Errorf("read %s: %w", *source, err)
	}

	rendered, err := render.ProcDevices(string(data), *imexMajor, *capsMajor)
	if err != nil {
		return fmt.Errorf("render from %s: %w", *source, err)
	}

	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		return fmt.Errorf("create output directory for %s: %w", *output, err)
	}
	if err := os.WriteFile(*output, []byte(rendered), 0o644); err != nil { //nolint:gosec // world-readable by design: consumed by another container
		return fmt.Errorf("write %s: %w", *output, err)
	}

	fmt.Printf("wrote %s (%s major=%d, %s major=%d)\n",
		*output, render.IMEXChannelsDeviceName, *imexMajor, render.CapsDeviceName, *capsMajor)
	return nil
}
