// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"fmt"
	"strings"
)

// A decoded `nvidia-smi -q -x` document and the readings assertions take from
// it. One document answers every question the suite used to ask through `-L`
// and a series of --query-gpu calls, so a scenario that read four fields now
// makes one exec.
//
// No build tag: decoding and comparing is pure. The exec wrapper is
// GPUSnapshotFromPod in nvidiasmi.go, under //go:build e2e.

// GPUSnapshot is one decoded document.
type GPUSnapshot struct {
	log nvidiaSMILog
}

// ParseGPUSnapshot decodes `nvidia-smi -q -x` output.
func ParseGPUSnapshot(out string) (GPUSnapshot, error) {
	log, err := parseNvidiaSMIXML(out)
	if err != nil {
		return GPUSnapshot{}, err
	}
	return GPUSnapshot{log: log}, nil
}

// AttachedGPUs is the <attached_gpus> reading, nvidia-smi's own count. It is
// reported separately from Count so a truncated document is detectable.
func (s GPUSnapshot) AttachedGPUs() (int, bool) { return s.log.AttachedGPUs.intValue() }

// Count is the number of <gpu> elements in the document.
func (s GPUSnapshot) Count() int { return len(s.log.GPUs) }

// ProductNames lists <product_name> in the order nvidia-smi emits GPUs, which
// is the order it indexes them by.
func (s GPUSnapshot) ProductNames() []string {
	names := make([]string, 0, len(s.log.GPUs))
	for _, gpu := range s.log.GPUs {
		names = append(names, strings.TrimSpace(string(gpu.ProductName)))
	}
	return names
}

// UUIDs lists <uuid> in nvidia-smi's GPU order.
func (s GPUSnapshot) UUIDs() []string {
	uuids := make([]string, 0, len(s.log.GPUs))
	for _, gpu := range s.log.GPUs {
		uuids = append(uuids, strings.TrimSpace(string(gpu.UUID)))
	}
	return uuids
}

// GPU returns the readings for the GPU at index, in nvidia-smi's order — the
// same index `--id=N` takes.
func (s GPUSnapshot) GPU(index int) (GPUReadings, error) {
	if index < 0 || index >= len(s.log.GPUs) {
		return GPUReadings{}, fmt.Errorf(
			"nvidia-smi XML reported %d GPUs, want an entry at index %d", len(s.log.GPUs), index)
	}
	return GPUReadings{gpu: s.log.GPUs[index], index: index}, nil
}

// GPUReadings exposes one GPU's readings.
type GPUReadings struct {
	gpu   nvidiaSMIGPU
	index int
}

// Label names the GPU in assertion output.
func (g GPUReadings) Label() string { return g.gpu.label(g.index) }

// UUID is the <uuid> body.
func (g GPUReadings) UUID() string { return strings.TrimSpace(string(g.gpu.UUID)) }

// Processes decodes this GPU's <processes> block.
func (g GPUReadings) Processes() ([]SMIProcess, error) {
	infos := g.gpu.Processes.Infos
	processes := make([]SMIProcess, 0, len(infos))
	for _, info := range infos {
		mib, ok := nvidiaSMIInteger(info.UsedMemory)
		if !ok {
			return nil, fmt.Errorf("%s: pid %d used_memory = %q, want a MiB reading",
				g.Label(), info.PID, info.UsedMemory)
		}
		processes = append(processes, SMIProcess{PID: info.PID, Name: info.Name, MemoryMiB: mib})
	}
	return processes, nil
}

// DiffInventoryXML checks the document describes exactly wantGPUs devices, all
// named wantProductName. product_name carries the profile's DisplayName
// verbatim, so this is an equality check rather than a substring search.
func DiffInventoryXML(out, wantProductName string, wantGPUs int) []string {
	snap, err := ParseGPUSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	attached, ok := snap.AttachedGPUs()
	switch {
	case !ok:
		problems = append(problems, fmt.Sprintf("attached_gpus = %q, want %d",
			string(snap.log.AttachedGPUs), wantGPUs))
	case attached != wantGPUs:
		problems = append(problems, fmt.Sprintf("attached_gpus = %d, want %d", attached, wantGPUs))
	}
	// nvidia-smi can report a count it then fails to describe; a mismatch means
	// the document is truncated and every later index is suspect.
	if ok && attached != snap.Count() {
		problems = append(problems, fmt.Sprintf(
			"attached_gpus = %d but the document carries %d <gpu> elements", attached, snap.Count()))
	}
	for i, name := range snap.ProductNames() {
		if name != wantProductName {
			problems = append(problems, fmt.Sprintf("GPU %d product_name = %q, want %q",
				i, name, wantProductName))
		}
	}
	return problems
}

// DiffNoProcessesXML checks every GPU reports an empty <processes> block. With
// no processes configured the process-detail-list path must report none: a prior
// bug had the internal export-table stub return SUCCESS without zeroing the
// caller's count, so nvidia-smi rendered its uninitialised buffer as hundreds of
// phantom PID 0 entries.
func DiffNoProcessesXML(out string) []string {
	snap, err := ParseGPUSnapshot(out)
	if err != nil {
		return []string{err.Error()}
	}

	var problems []string
	for i, gpu := range snap.log.GPUs {
		for _, p := range gpu.Processes.Infos {
			problems = append(problems, fmt.Sprintf("%s reports pid %d (%s), want no processes",
				gpu.label(i), p.PID, p.Name))
		}
	}
	return problems
}
