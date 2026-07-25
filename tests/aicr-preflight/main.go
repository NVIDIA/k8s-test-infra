// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "aicr-preflight: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr *os.File) error {
	fs := flag.NewFlagSet("aicr-preflight", flag.ContinueOnError)
	fs.SetOutput(stderr)

	catalogPath := fs.String("catalog", "catalog.yaml", "path to the coverage catalog")
	ctrfDir := fs.String("ctrf", "", "directory of AICR CTRF reports from `aicr validate --output`")
	jsonOut := fs.String("json", "", "write the machine-readable report here (default stdout)")
	mdOut := fs.String("markdown", "", "also write a markdown catalog table here")
	provenance := fs.String("provenance", string(ProvenanceSim), "evidence provenance: sim or silicon")
	cluster := fs.String("cluster", "", "cluster identifier recorded in the report")
	profile := fs.String("profile", "", "nvml-mock GPU profile recorded in the report")

	if err := fs.Parse(args); err != nil {
		return err
	}

	prov := Provenance(*provenance)
	if prov != ProvenanceSim && prov != ProvenanceSilicon {
		return fmt.Errorf("invalid -provenance %q, want sim or silicon", *provenance)
	}

	catalog, err := LoadCatalog(*catalogPath)
	if err != nil {
		return err
	}

	// No CTRF directory means no run happened. That is a legitimate state: the
	// catalog still reports, with every outcome recorded not-run. It must never
	// silently look like a clean pass.
	var results *RunResults
	if *ctrfDir != "" {
		results, err = LoadCTRFDir(*ctrfDir)
		if err != nil {
			return err
		}
	} else {
		_, _ = fmt.Fprintln(stderr, "aicr-preflight: no -ctrf directory given; every check will be recorded not-run")
	}

	report := BuildReport(catalog, results, prov, *cluster, *profile, time.Now())

	jsonTarget := stdout
	if *jsonOut != "" {
		f, err := os.Create(*jsonOut) //nolint:gosec // operator-supplied output path
		if err != nil {
			return fmt.Errorf("create %s: %w", *jsonOut, err)
		}
		defer f.Close() //nolint:errcheck // best-effort close on the output file
		jsonTarget = f
	}
	if err := report.WriteJSON(jsonTarget); err != nil {
		return err
	}

	if *mdOut != "" {
		f, err := os.Create(*mdOut) //nolint:gosec // operator-supplied output path
		if err != nil {
			return fmt.Errorf("create %s: %w", *mdOut, err)
		}
		defer f.Close() //nolint:errcheck // best-effort close on the output file
		if err := report.WriteMarkdown(f); err != nil {
			return err
		}
	}

	return nil
}
