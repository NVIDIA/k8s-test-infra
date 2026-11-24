// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package nicdiff compares a real node's NIC surfaces with a Mokka node's, as
// `collect network` captures them, and reports one check per surface. It
// backs `mokka-capture diff nic`.
//
//	mokka-capture diff nic [-causes file] [-failed] [-json|-report] <real node dir> <mock node dir>
//
// Both node directories, or listings `nic-surfaces` wrote, are read into
// nicsurfaces' normalized list, and every object and attribute either side
// has becomes a check that passes, fails, or is not compared. Identifiers are compared through their relations (an SMI
// device shares its parent's node GUID), never by value. Every failure and
// every check not made is counted by cause: a schema gap, a rule bug, a
// capture blind spot, or by design. Blind spots (a capture file one side
// lacks) and values not comparable by design are recognised here; the rest
// come from a causes file, and a failure it does not explain is unclassified,
// which fails the command.
package nicdiff

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/urfave/cli/v3"
	"go.yaml.in/yaml/v3"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/cmdline"
	"github.com/NVIDIA/k8s-test-infra/internal/capture/nicsurfaces"
)

// Command is `mokka-capture diff nic`.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "nic",
		Usage: "compare a real node's NIC capture with a Mokka node's, one check per surface",
		Description: "Reads two capture node directories, as collect network writes them, through nic-surfaces, " +
			"or a listing nic-surfaces wrote in place of either. " +
			"Exits 1 when a failure is unclassified: neither recognised here nor explained by the causes file.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "causes", Usage: "YAML file giving the cause of known differences"},
			&cli.BoolFlag{Name: "failed", Usage: "list only the failed checks"},
			&cli.BoolFlag{Name: "json", Usage: "write one JSON object per check, then the summary"},
			&cli.BoolFlag{Name: "report", Usage: "write a Markdown report: checks by surface, then failures and checks not made grouped by cause"},
		},
		Arguments: []cli.Argument{
			&cli.StringArg{Name: "real-node-dir", Required: true},
			&cli.StringArg{Name: "mock-node-dir", Required: true},
		},
		Action: func(_ context.Context, cmd *cli.Command) error {
			if err := cmdline.RejectExtraArgs(cmd); err != nil {
				return err
			}
			if cmd.Bool("json") && cmd.Bool("report") {
				return cmdline.Usage(cmd, "error: -json and -report are two ways to write the result; pass one")
			}
			return cmdline.Exit(run(options{
				causes: cmd.String("causes"),
				failed: cmd.Bool("failed"),
				json:   cmd.Bool("json"),
				report: cmd.Bool("report"),
			}, cmd.StringArg("real-node-dir"), cmd.StringArg("mock-node-dir"), cmd.Root().Writer, cmd.Root().ErrWriter))
		},
	}
}

type options struct {
	causes               string
	failed, json, report bool
}

func run(opts options, realDir, mockDir string, stdout, stderr io.Writer) int {
	checks, err := load(opts.causes, realDir, mockDir, stderr)
	if err == nil {
		s := summarize(checks)
		switch {
		case opts.json:
			err = writeJSON(stdout, checks, s, opts.failed)
		case opts.report:
			writeReport(stdout, checks, s, realDir, mockDir)
		default:
			writeText(stdout, checks, s, opts.failed)
		}
		if err == nil && s.Failed[causeUnclassified] > 0 {
			return 1
		}
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// load reads both captures and the causes file and returns the classified
// checks, warning about causes entries that explain nothing.
func load(causesPath, realDir, mockDir string, stderr io.Writer) ([]check, error) {
	hw, err := nicsurfaces.Read(realDir)
	if err != nil {
		return nil, err
	}
	sim, err := nicsurfaces.Read(mockDir)
	if err != nil {
		return nil, err
	}
	known, err := readCauses(causesPath)
	if err != nil {
		return nil, err
	}
	checks, unused := classify(compare(hw, sim), known)
	for _, k := range unused {
		_, _ = fmt.Fprintf(stderr, "warning: causes entry %q matches no failure; remove it if its gap is fixed\n", k.Match)
	}
	return checks, nil
}

// cause explains the checks whose "rule object" matches match.
type cause struct {
	Match string `yaml:"match"`
	Cause string `yaml:"cause"`
	Note  string `yaml:"note"`
	re    *regexp.Regexp
}

var validCauses = map[string]bool{causeSchemaGap: true, causeRuleBug: true, causeBlindSpot: true, causeByDesign: true}

func readCauses(path string) ([]cause, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []cause
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for i := range out {
		if !validCauses[out[i].Cause] {
			return nil, fmt.Errorf("%s: entry %d: cause %q is not one of schema gap, rule bug, capture blind spot, by design", path, i+1, out[i].Cause)
		}
		if out[i].re, err = regexp.Compile(out[i].Match); err != nil {
			return nil, fmt.Errorf("%s: entry %d: %w", path, i+1, err)
		}
	}
	return out, nil
}

// classify gives each failure the cause of the first entry that matches it,
// and the rest the cause unclassified. A difference explained as by design is
// out of scope, not a failure: it becomes a check not made and keeps what
// differed. classify also returns the entries that explain nothing: gaps that
// were fixed, or entries an earlier one shadows.
func classify(checks []check, known []cause) ([]check, []cause) {
	used := make([]bool, len(known))
	for i := range checks {
		if checks[i].Status != fail {
			continue
		}
		checks[i].Cause = causeUnclassified
		name := checks[i].Rule + " " + checks[i].Object
		for j, k := range known {
			if k.re.MatchString(name) {
				used[j] = true
				checks[i].Cause, checks[i].Note = k.Cause, k.Note
				if k.Cause == causeByDesign {
					checks[i].Status = notCompared
				}
				break
			}
		}
	}
	var unused []cause
	for j, k := range known {
		if !used[j] {
			unused = append(unused, k)
		}
	}
	return checks, unused
}

type summary struct {
	Checks      int            `json:"checks"`
	Passed      int            `json:"passed"`
	Failed      map[string]int `json:"failed"`
	NotCompared map[string]int `json:"not_compared"`
}

func summarize(checks []check) summary {
	s := summary{Failed: map[string]int{}, NotCompared: map[string]int{}}
	for _, c := range checks {
		switch c.Status {
		case pass:
			s.Checks++
			s.Passed++
		case fail:
			s.Checks++
			s.Failed[c.Cause]++
		default:
			s.NotCompared[c.Cause]++
		}
	}
	return s
}

func writeJSON(w io.Writer, checks []check, s summary, failedOnly bool) error {
	enc := json.NewEncoder(w)
	for _, c := range checks {
		if failedOnly && c.Status != fail {
			continue
		}
		if err := enc.Encode(c); err != nil {
			return err
		}
	}
	return enc.Encode(struct {
		Summary summary `json:"summary"`
	}{s})
}

const maxValue = 100

func clip(v string) string {
	if len(v) > maxValue {
		return v[:maxValue] + "…"
	}
	return v
}

func writeText(w io.Writer, checks []check, s summary, failedOnly bool) {
	for _, c := range checks {
		if !failedOnly || c.Status == fail {
			_, _ = fmt.Fprintln(w, textLine(c))
		}
	}
	failed := 0
	for _, n := range s.Failed {
		failed += n
	}
	_, _ = fmt.Fprintf(w, "\nchecks:        %d (passed %d, failed %d)\n", s.Checks, s.Passed, failed)
	_, _ = fmt.Fprintf(w, "failed by cause:")
	for _, c := range []string{causeSchemaGap, causeRuleBug, causeBlindSpot, causeUnclassified} {
		_, _ = fmt.Fprintf(w, "  %s %d", c, s.Failed[c])
	}
	notCompared := s.NotCompared[causeByDesign] + s.NotCompared[causeBlindSpot]
	_, _ = fmt.Fprintf(w, "\nnot compared:  %d (by design %d, capture blind spot %d)\n",
		notCompared, s.NotCompared[causeByDesign], s.NotCompared[causeBlindSpot])
}

// textLine is one check as a line: status, rule, object, what was found, and
// the cause in brackets.
func textLine(c check) string {
	line := fmt.Sprintf("%s  %s  %s", c.Status, c.Rule, c.Object)
	switch {
	case c.Status == pass:
		line += "  = " + clip(c.Real)
	case c.Status != fail && c.Detail == "":
	case c.Detail != "" && c.Real != "" && c.Sim != "":
		line += fmt.Sprintf("  real=%q simulated=%q: %s", clip(c.Real), clip(c.Sim), c.Detail)
	case c.Detail != "":
		line += "  " + c.Detail
		if v := c.Real + c.Sim; v != "" {
			line += fmt.Sprintf(" (%s)", clip(v))
		}
	default:
		line += fmt.Sprintf("  real=%q simulated=%q", clip(c.Real), clip(c.Sim))
	}
	return line + causeNote(c)
}

// causeNote is a check's cause and note in brackets, or "" without a cause.
func causeNote(c check) string {
	switch {
	case c.Cause == "":
		return ""
	case c.Note == "":
		return "  [" + c.Cause + "]"
	default:
		return "  [" + c.Cause + ": " + c.Note + "]"
	}
}
