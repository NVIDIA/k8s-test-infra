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
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// cellSpec is one planned matrix cell as written in cells.yaml, together with
// any pre-known blocking reason.
type cellSpec struct {
	Cell         `json:",inline" yaml:",inline"`
	BlockedCause Cause  `json:"blockedCause,omitempty" yaml:"blockedCause,omitempty"`
	BlockedWhy   string `json:"blockedWhy,omitempty" yaml:"blockedWhy,omitempty"`
}

// validate rejects a cell whose block would mislead the report. The cause is
// what keeps a kind artifact or an AICR catalog gap from reading as a Mokka gap,
// so it must be one the report explains, and a blocked cell must say why.
func (s cellSpec) validate() error {
	switch s.BlockedCause {
	case CauseNone:
		return nil
	case CauseK, CauseU, CauseX, CauseBudget:
		if strings.TrimSpace(s.BlockedWhy) == "" {
			return fmt.Errorf("cell %q is blocked (%s) but has no blockedWhy: a blocked cell must say why it did not run", s.ID, s.BlockedCause)
		}
		return nil
	default:
		return fmt.Errorf("cell %q has invalid blockedCause %q, want one of K U X BUDGET", s.ID, s.BlockedCause)
	}
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

	doc, err := loadCells(*cellsPath)
	if err != nil {
		return err
	}

	results, err := classifyCells(catalog, doc, *resultsDir)
	if err != nil {
		return err
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

// classifyCells joins the catalog against every planned cell. resultsDir holds
// the per-cell CTRF output; empty means no run happened.
func classifyCells(catalog *Catalog, doc *cellsDoc, resultsDir string) ([]CellResult, error) {
	if resultsDir != "" {
		// A wrong path would otherwise read as a sweep in which nothing ran.
		info, err := os.Stat(resultsDir)
		if err != nil {
			return nil, fmt.Errorf("results directory: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("results directory %s is not a directory", resultsDir)
		}
	}

	results := make([]CellResult, 0, len(doc.Cells))
	for _, spec := range doc.Cells {
		var run *RunResults
		cause, why := spec.BlockedCause, spec.BlockedWhy

		if cause == CauseNone {
			var err error
			run, err = loadCellRun(resultsDir, spec.Cell.ID)
			if err != nil {
				return nil, fmt.Errorf("cell %s: %w", spec.Cell.ID, err)
			}
		}

		cr := Classify(catalog, run, spec.Cell, doc.Versions, ProvenanceSim, cause, why)
		if run != nil {
			cr.LogsPointer = filepath.Join(resultsDir, spec.Cell.ID)
		}
		results = append(results, cr)
	}
	return results, nil
}

// loadCellRun reads one cell's CTRF output, or returns nil when the cell has
// none: no results directory at all, or no ctrf directory for this cell.
func loadCellRun(resultsDir, id string) (*RunResults, error) {
	if resultsDir == "" {
		return nil, nil
	}

	// Per-cell CTRF lives at results/<cell-id>/ctrf/*.json. `aicr validate
	// --output` writes one report per invocation, so a cell that ran several
	// phases separately has several files here.
	dir := filepath.Join(resultsDir, id, "ctrf")
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		// Any other stat failure is a results tree the harness cannot read.
		return nil, err
	}

	// A results directory that exists but cannot be parsed is reported, not
	// swallowed. Swallowing it would silently convert the whole cell to
	// not-run and hide a harness bug.
	return LoadCTRFDir(dir)
}

// loadCells reads the matrix cell definitions.
func loadCells(path string) (*cellsDoc, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return nil, fmt.Errorf("read cells %s: %w", path, err)
	}
	var doc cellsDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse cells %s: %w", path, err)
	}
	if len(doc.Cells) == 0 {
		return nil, fmt.Errorf("cells file %s defines no cells", path)
	}

	// A cell ID is also its results directory, so a repeated ID would classify
	// one run as two cells.
	seen := make(map[string]bool, len(doc.Cells))
	for _, spec := range doc.Cells {
		if err := spec.validate(); err != nil {
			return nil, fmt.Errorf("cells file %s: %w", path, err)
		}
		if seen[spec.ID] {
			return nil, fmt.Errorf("cells file %s defines cell %q twice: a duplicate double-counts in the rollup", path, spec.ID)
		}
		seen[spec.ID] = true
	}
	return &doc, nil
}
