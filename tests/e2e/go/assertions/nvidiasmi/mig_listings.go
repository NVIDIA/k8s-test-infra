// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiasmi

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Parsers for the `nvidia-smi mig` listings, which is the surface operators
// and nvidia-mig-parted drive. It reaches NVML through different entry points
// than `nvidia-smi -L` does, so it can disagree with the `-L` enumeration the
// rest of this package reads; comparing the two is the point of parsing it.
//
// Every listing is a box-drawn table whose columns are space-padded to align,
// so rows are matched rather than split on whitespace. The `MIG ` literal in
// the name column is what tells a data row from the continuation line that
// carries a row's second bank of counters.

// MigGPUInstance is one row of `nvidia-smi mig -lgi`.
//
// Profile drops the listing's "MIG " prefix so it compares directly with
// MigDevice.Profile, which is the whole reason to parse this table.
//
// InstanceID is the engine-assigned GPU instance ID. It is unique per GPU, not
// per node, and it is the key the capability table under /dev/nvidia-caps is
// named by. Placement is kept verbatim as "start:size" — the assertions only
// need to tell one partition's slot from another's.
type MigGPUInstance struct {
	GPU        int
	Profile    string
	ProfileID  int
	InstanceID int
	Placement  string
}

// MigProfileCapacity is the occupancy of one profile on one GPU, from
// `nvidia-smi mig -lgip`. Free and Total are the "Free/Total" column: a fully
// partitioned board reports zero free of the profile it was carved into, which
// is the signal a scheduler-facing consumer reads to decide the board is spoken
// for. The resource columns are deliberately not modelled.
type MigProfileCapacity struct {
	GPU       int
	Profile   string
	ProfileID int
	Free      int
	Total     int
}

var (
	// A data row in any of the four listings: a leading GPU index and a
	// profile name. Continuation lines have neither, so this also excludes
	// them from a row count.
	migTableRow = regexp.MustCompile(`^\|\s+\d+\s.*\bMIG\s`)

	// | GPU   Name               Profile  Instance   Placement  |
	migGPUInstanceRow = regexp.MustCompile(`^\|\s+(\d+)\s+MIG\s+(\S+)\s+(\d+)\s+(\d+)\s+(\d+:\d+)\s*\|`)

	// | GPU   Name               ID    Instances   Memory ... |
	// Only the columns through Free/Total are matched; the rest of the row
	// is resource detail no assertion reads.
	migProfileRow = regexp.MustCompile(`^\|\s+(\d+)\s+MIG\s+(\S+)\s+(\d+)\s+(\d+)/(\d+)\s`)
)

// Each listing names itself in a banner above the column headers. Requiring it
// is what stops a parser from reporting "no instances" when handed output it
// does not understand — an `nvidia-smi mig` failure prints an error and no
// table at all, and every assertion built on a silently empty result would
// pass.
const (
	gpuInstanceBanner        = "| GPU instances:"
	gpuInstanceProfileBanner = "| GPU instance profiles:"
)

// MigListing names one of the `nvidia-smi mig` tables read by row count alone,
// so that count can be tied to the listing the caller asked for. The tables
// have the same shape and often the same number of rows — a board's compute
// instances and its GPU instances are one for one under migStrategy=single —
// so a crossed flag is invisible to a bare count.
type MigListing struct {
	cmd    string
	banner string
}

var (
	// MigComputeInstances is `nvidia-smi mig -lci`, the compute instances a
	// board has.
	MigComputeInstances = MigListing{cmd: "mig -lci", banner: "| Compute instances:"}
	// MigComputeInstanceProfiles is `nvidia-smi mig -lcip`, the compute
	// instance profiles its partitions offer.
	MigComputeInstanceProfiles = MigListing{cmd: "mig -lcip", banner: "| Compute instance profiles:"}
)

// ListMigGPUInstances parses `nvidia-smi mig -lgi` into the partitions it
// reports. A board with MIG on and nothing carved out yields an empty listing,
// which is a real state and not an error; output that is not this listing is.
func ListMigGPUInstances(out string) ([]MigGPUInstance, error) {
	if !strings.Contains(out, gpuInstanceBanner) {
		return nil, notAListing("mig -lgi", out)
	}
	var instances []MigGPUInstance
	for line := range strings.Lines(out) {
		if !migTableRow.MatchString(line) {
			continue
		}
		m := migGPUInstanceRow.FindStringSubmatch(line)
		if m == nil {
			return nil, unparsedRow("mig -lgi", line)
		}
		var c fields
		inst := MigGPUInstance{
			GPU:        c.atoi("GPU index", m[1]),
			Profile:    m[2],
			ProfileID:  c.atoi("profile ID", m[3]),
			InstanceID: c.atoi("instance ID", m[4]),
			Placement:  m[5],
		}
		if c.err != nil {
			return nil, fmt.Errorf("mig -lgi row %q: %w", strings.TrimSpace(line), c.err)
		}
		instances = append(instances, inst)
	}
	return instances, nil
}

// ListMigProfileCapacity parses `nvidia-smi mig -lgip` into the per-GPU
// occupancy of every profile the board offers.
func ListMigProfileCapacity(out string) ([]MigProfileCapacity, error) {
	if !strings.Contains(out, gpuInstanceProfileBanner) {
		return nil, notAListing("mig -lgip", out)
	}
	var profiles []MigProfileCapacity
	for line := range strings.Lines(out) {
		if !migTableRow.MatchString(line) {
			continue
		}
		m := migProfileRow.FindStringSubmatch(line)
		if m == nil {
			return nil, unparsedRow("mig -lgip", line)
		}
		var c fields
		p := MigProfileCapacity{
			GPU:       c.atoi("GPU index", m[1]),
			Profile:   m[2],
			ProfileID: c.atoi("profile ID", m[3]),
			Free:      c.atoi("free count", m[4]),
			Total:     c.atoi("total count", m[5]),
		}
		if c.err != nil {
			return nil, fmt.Errorf("mig -lgip row %q: %w", strings.TrimSpace(line), c.err)
		}
		profiles = append(profiles, p)
	}
	return profiles, nil
}

// CountMigTableRows counts the data rows of the named `nvidia-smi mig` table.
//
// This is the coarse reading of the compute-instance listings, whose columns
// restate what -lgi already carries. Checking the banner is what makes the
// count a statement about that listing rather than about whichever table the
// caller happened to pass, and it is what turns an `nvidia-smi mig` failure —
// which prints an error and no table — into an error rather than a zero.
func CountMigTableRows(out string, listing MigListing) (int, error) {
	if !strings.Contains(out, listing.banner) {
		return 0, notAListing(listing.cmd, out)
	}
	rows := 0
	for line := range strings.Lines(out) {
		if migTableRow.MatchString(line) {
			rows++
		}
	}
	return rows, nil
}

// MigCapacityFor returns the occupancy of profile on gpu, and whether the
// listing offered it there at all.
func MigCapacityFor(profiles []MigProfileCapacity, gpu int, profile string) (MigProfileCapacity, bool) {
	for _, p := range profiles {
		if p.GPU == gpu && p.Profile == profile {
			return p, true
		}
	}
	return MigProfileCapacity{}, false
}

// MigInstancesOfGPU narrows a -lgi listing to one GPU, so a per-board claim is
// not satisfied by another board's partitions.
func MigInstancesOfGPU(instances []MigGPUInstance, gpu int) []MigGPUInstance {
	var out []MigGPUInstance
	for _, i := range instances {
		if i.GPU == gpu {
			out = append(out, i)
		}
	}
	return out
}

func notAListing(cmd, out string) error {
	return fmt.Errorf("output is not an `nvidia-smi %s` listing: %s", cmd, firstLine(out))
}

func unparsedRow(cmd, line string) error {
	return fmt.Errorf("unrecognised `nvidia-smi %s` row %q; the column layout has changed",
		cmd, strings.TrimSpace(line))
}

// firstLine keeps an error message short when the output is a whole table.
func firstLine(out string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	if line == "" {
		return "<empty>"
	}
	return strconv.Quote(line)
}

// fields converts the digit runs a row pattern captured, holding the first
// failure rather than folding it into a zero. The patterns already constrain
// these to digits, so the only reachable failure is a value too large for an
// int — which is a mock reporting nonsense and worth surfacing as such.
type fields struct{ err error }

func (f *fields) atoi(name, s string) int {
	if f.err != nil {
		return 0
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		f.err = fmt.Errorf("%s: %w", name, err)
	}
	return v
}
