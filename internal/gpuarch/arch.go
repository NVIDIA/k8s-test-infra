// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package gpuarch orders NVIDIA GPU architecture generations so a feature gate can
// be written as the question it is asking — "Hopper and newer", "before
// Ampere", "between Ampere and Hopper" — rather than as an integer comparison
// re-derived at each call site.
//
// The values are NVML's own (NVML_DEVICE_ARCH_*), so pkg/gpu/mocknvml/engine
// converts with a plain cast and TestArchConstantsMatchNVML pins the two
// numberings together. This package deliberately imports no NVML binding:
// go-nvml does not build with CGO_ENABLED=0, and the e2e harness needs to
// compare architectures without taking on cgo.
//
// # Unknown satisfies nothing
//
// NVML_DEVICE_ARCH_UNKNOWN is 0xFFFFFFFF, which sorts above every real
// generation, so a bare >= reports unidentified hardware as newer than
// Blackwell. Every comparison here answers false for Unknown, whether it is the
// receiver or the bound. That is the honest position: an unidentified part
// supports nothing, and cannot be claimed to be newer or older than anything.
//
// The consequence breaks the usual expectation and so is worth stating plainly:
//
//	!a.AtLeast(Hopper)   is NOT   a.Before(Hopper)
//
// They agree everywhere except Unknown, where both are false. Known is how to
// ask whether the generation was identified at all.
package gpuarch

import (
	"strconv"
	"strings"
)

// Arch is a GPU architecture generation, ordered oldest to newest.
//
// The values are NVML's, which are monotonic but not dense: 11 and 12 are
// reserved and unpublished, so Blackwell (10) is followed by Rubin (13).
// Ordering by the raw value stays correct across the gap.
type Arch uint32

// Unknown is NVML's answer for a generation it cannot identify. Its value
// is the largest uint32, which is exactly why every comparison in this
// package special-cases it; see the package doc.
const Unknown Arch = 0xFFFFFFFF

// The real generations, oldest to newest. Values are NVML's
// NVML_DEVICE_ARCH_*: monotonic but not dense, since 11 and 12 are
// reserved and unpublished, so Blackwell (10) is followed by Rubin (13).
const (
	Kepler    Arch = 2
	Maxwell   Arch = 3
	Pascal    Arch = 4
	Volta     Arch = 5
	Turing    Arch = 6
	Ampere    Arch = 7
	Ada       Arch = 8
	Hopper    Arch = 9
	Blackwell Arch = 10
	Rubin     Arch = 13
)

// names is the canonical spelling of each generation. Parse builds its index
// from this map, so a name can never render one way and parse another.
var names = map[Arch]string{
	Kepler:    "kepler",
	Maxwell:   "maxwell",
	Pascal:    "pascal",
	Volta:     "volta",
	Turing:    "turing",
	Ampere:    "ampere",
	Ada:       "ada",
	Hopper:    "hopper",
	Blackwell: "blackwell",
	Rubin:     "rubin",
}

// aliases are the additional spellings configuration is allowed to use.
// "ada_lovelace" is the one the shipped l40s profile carries.
var aliases = map[string]Arch{
	"ada_lovelace": Ada,
}

// byName resolves a normalized name to its generation.
var byName = newNameIndex()

func newNameIndex() map[string]Arch {
	index := make(map[string]Arch, len(names)+len(aliases))
	for a, name := range names {
		index[name] = a
	}
	for name, a := range aliases {
		index[name] = a
	}
	return index
}

// Known reports whether the generation was identified. Because every
// comparison answers false for Unknown, this is the only way to ask about it.
func (a Arch) Known() bool { return a != Unknown }

// AtLeast reports whether a is floor or newer — the inclusive "Hopper and
// newer" form real NVML feature gates take. False if either side is Unknown.
func (a Arch) AtLeast(floor Arch) bool {
	return a.Known() && floor.Known() && a >= floor
}

// Before reports whether a is strictly older than x. False if either side is
// Unknown, so this is NOT the negation of AtLeast; see the package doc.
func (a Arch) Before(x Arch) bool {
	return a.Known() && x.Known() && a < x
}

// Is reports whether a is exactly generation x. False if either side is
// Unknown, Unknown.Is(Unknown) included — Known answers that question.
func (a Arch) Is(x Arch) bool {
	return a.Known() && x.Known() && a == x
}

// Between reports whether a falls within [lo, hi], inclusive of both ends,
// since generations are discrete: Between(Ampere, Hopper) admits Ampere, Ada
// and Hopper. An inverted range admits nothing, because it can only be a
// caller bug and silently reading it backwards would hide one.
func (a Arch) Between(lo, hi Arch) bool {
	return a.AtLeast(lo) && hi.Known() && a <= hi
}

// String returns the canonical lowercase name, so an Arch parsed from
// "ada_lovelace" renders as "ada". A value NVML has defined but this package
// does not yet name renders as arch(<n>), which keeps it identifiable in a log
// line instead of vanishing into an empty string.
func (a Arch) String() string {
	if name, ok := names[a]; ok {
		return name
	}
	if a == Unknown {
		return "unknown"
	}
	return "arch(" + strconv.FormatUint(uint64(a), 10) + ")"
}

// Parse resolves a configured architecture name, trimming surrounding space and
// lowercasing before lookup, and accepting the aliases configuration uses.
//
// It reports ok rather than returning an error because its two callers want
// opposite reactions to an unrecognized name: the mock engine mirrors NVML and
// answers Unknown, while the e2e harness treats it as a typo in a repository
// profile and fails.
func Parse(s string) (Arch, bool) {
	a, ok := byName[strings.ToLower(strings.TrimSpace(s))]
	if !ok {
		return Unknown, false
	}
	return a, true
}
