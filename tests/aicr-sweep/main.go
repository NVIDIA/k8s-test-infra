// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Command aicr-sweep classifies AICR validate/conformance results collected on a
// Mokka (nvml-mock) cluster into the A/B/C/G coverage buckets, and rolls them up
// across the permutation matrix.
//
// It answers one question, posed by the AICR maintainer on 2026-07-24:
//
//	Does Mokka + AICR materially reduce GB200 bring-up time, or does it merely
//	prove the stack deploys against mocked APIs?
//
// An honest negative answer is a successful run. Nothing here is tuned toward a
// favourable number: see the invariants on Classify.
//
// Usage:
//
//	aicr-sweep -catalog catalog.yaml -cells cells.yaml \
//	           -results results/<run-id> \
//	           -json report.json -markdown coverage.md
//
// Running with no -results is legal and prints the catalog with every outcome
// recorded not-run. That is the correct output when no run happened.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// cellSpec is one planned matrix cell as written in cells.yaml, together with
// any pre-known blocking reason.
type cellSpec struct {
	Cell         `json:",inline" yaml:",inline"`
	BlockedCause Cause  `json:"blockedCause,omitempty" yaml:"blockedCause,omitempty"`
	BlockedWhy   string `json:"blockedWhy,omitempty" yaml:"blockedWhy,omitempty"`
}

type cellsDoc struct {
	Versions Versions   `json:"versions" yaml:"versions"`
	Cells    []cellSpec `json:"cells" yaml:"cells"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "aicr-sweep: %v\n", err)
		os.Exit(1)
	}
}

//nolint:cyclop // existing complexity; refactor deferred
func run() error {
	var (
		catalogPath = flag.String("catalog", "catalog.yaml", "path to the coverage catalog")
		cellsPath   = flag.String("cells", "cells.yaml", "path to the matrix cell definitions")
		resultsDir  = flag.String("results", "", "directory holding per-cell CTRF output (results/<cell-id>/*.json); empty means no run happened")
		jsonOut     = flag.String("json", "", "write the machine-readable report here")
		markdownOut = flag.String("markdown", "", "write the human-readable coverage read here")
	)
	flag.Parse()

	catalog, err := LoadCatalog(*catalogPath)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(*cellsPath) //nolint:gosec // operator-supplied path
	if err != nil {
		return fmt.Errorf("read cells %s: %w", *cellsPath, err)
	}
	var doc cellsDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse cells %s: %w", *cellsPath, err)
	}
	if len(doc.Cells) == 0 {
		return fmt.Errorf("cells file %s defines no cells", *cellsPath)
	}

	results := make([]CellResult, 0, len(doc.Cells))
	for _, spec := range doc.Cells {
		var run *RunResults
		cause, why := spec.BlockedCause, spec.BlockedWhy

		if cause == CauseNone && *resultsDir != "" {
			// Per-cell CTRF lives at results/<cell-id>/ctrf/*.json. `aicr
			// validate --output` writes one report per invocation, so a cell
			// that ran several phases separately has several files here.
			dir := filepath.Join(*resultsDir, spec.Cell.ID, "ctrf")
			if _, statErr := os.Stat(dir); statErr == nil {
				run, err = LoadCTRFDir(dir)
				if err != nil {
					// A results directory that exists but cannot be parsed is
					// reported, not swallowed. Swallowing it would silently
					// convert the whole cell to not-run and hide a harness bug.
					return fmt.Errorf("cell %s: %w", spec.Cell.ID, err)
				}
			}
		}

		cr := Classify(catalog, run, spec.Cell, doc.Versions, ProvenanceSim, cause, why)
		if run != nil {
			cr.LogsPointer = filepath.Join(*resultsDir, spec.Cell.ID)
		}
		results = append(results, cr)
	}

	roll := Roll(results)

	if *jsonOut != "" {
		out, err := RenderJSON(results, roll)
		if err != nil {
			return fmt.Errorf("render JSON: %w", err)
		}
		if err := os.WriteFile(*jsonOut, out, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", *jsonOut, err)
		}
	}

	md := RenderMarkdown(results, roll)
	if *markdownOut != "" {
		if err := os.WriteFile(*markdownOut, []byte(md), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", *markdownOut, err)
		}
	} else {
		fmt.Print(md)
	}

	return nil
}
