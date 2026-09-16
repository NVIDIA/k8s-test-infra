// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package gpuarch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ordered is every real generation, oldest first. Several tests walk it to
// assert a property across the whole set rather than at a hand-picked pair.
var ordered = []Arch{Kepler, Maxwell, Pascal, Volta, Turing, Ampere, Ada, Hopper, Blackwell, Rubin}

// TestOrderingIsMonotonic pins that each generation sorts above its
// predecessor. Rubin is 13 while Blackwell is 10, so this also covers the
// unpublished 11/12 gap, which a dense re-numbering would have hidden.
func TestOrderingIsMonotonic(t *testing.T) {
	t.Parallel()
	for i := 1; i < len(ordered); i++ {
		older, newer := ordered[i-1], ordered[i]
		require.True(t, newer.AtLeast(older), "%s must be at least %s", newer, older)
		require.True(t, older.Before(newer), "%s must be before %s", older, newer)
		require.False(t, older.AtLeast(newer), "%s must not be at least %s", older, newer)
	}
}

// TestAtLeastIsInclusive covers the boundary the real gates sit on: "Hopper and
// newer" must admit Hopper itself.
func TestAtLeastIsInclusive(t *testing.T) {
	t.Parallel()
	for _, a := range ordered {
		require.True(t, a.AtLeast(a), "%s must be at least itself", a)
		require.False(t, a.Before(a), "%s must not be before itself", a)
	}
}

// TestUnknownSatisfiesNothing is the reason this package exists. Unknown is
// 0xFFFFFFFF, so a bare >= would report it as newer than Blackwell. Every
// method must refuse it, in EITHER operand position.
func TestUnknownSatisfiesNothing(t *testing.T) {
	t.Parallel()

	require.False(t, Unknown.Known(), "Unknown must not be Known")
	for _, a := range ordered {
		require.True(t, a.Known(), "%s must be Known", a)
	}

	// Unknown as the receiver.
	require.False(t, Unknown.AtLeast(Hopper), "Unknown must not clear a Hopper gate")
	require.False(t, Unknown.Before(Hopper), "Unknown must not claim to predate Hopper")
	require.False(t, Unknown.Is(Hopper), "Unknown is not Hopper")
	require.False(t, Unknown.Between(Kepler, Rubin), "Unknown is in no range")

	// Unknown as the bound. A gate against an unspecified generation has no
	// meaningful answer, so it must not be satisfiable.
	require.False(t, Hopper.AtLeast(Unknown), "no generation clears an Unknown floor")
	require.False(t, Hopper.Before(Unknown), "Unknown is not a ceiling to be under")
	require.False(t, Hopper.Is(Unknown), "Hopper is not Unknown")
	require.False(t, Hopper.Between(Kepler, Unknown), "an Unknown upper bound admits nothing")
	require.False(t, Hopper.Between(Unknown, Rubin), "an Unknown lower bound admits nothing")

	// Unknown is not even equal to itself under Is; Known is that question.
	require.False(t, Unknown.Is(Unknown), "use Known, not Is, to test for Unknown")
}

// TestNegationAsymmetry pins the one counter-intuitive consequence of the rule
// above, so a future simplification to `return !a.AtLeast(x)` fails here.
func TestNegationAsymmetry(t *testing.T) {
	t.Parallel()
	require.False(t, Unknown.AtLeast(Hopper))
	require.False(t, Unknown.Before(Hopper))
	require.NotEqual(t, !Unknown.AtLeast(Hopper), Unknown.Before(Hopper),
		"Before must not be the negation of AtLeast at Unknown")
}

// TestIsMatchesExactlyOneGeneration guards against Is degrading into a range
// check.
func TestIsMatchesExactlyOneGeneration(t *testing.T) {
	t.Parallel()
	for _, a := range ordered {
		for _, b := range ordered {
			require.Equal(t, a == b, a.Is(b), "%s.Is(%s)", a, b)
		}
	}
}

func TestBetween(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		a      Arch
		lo, hi Arch
		want   bool
	}{
		{"lower bound is inclusive", Ampere, Ampere, Hopper, true},
		{"upper bound is inclusive", Hopper, Ampere, Hopper, true},
		{"interior", Ada, Ampere, Hopper, true},
		{"below the range", Turing, Ampere, Hopper, false},
		{"above the range", Blackwell, Ampere, Hopper, false},
		{"single-generation range", Ada, Ada, Ada, true},
		{"single-generation range excludes others", Hopper, Ada, Ada, false},
		{"spans the 11/12 gap", Blackwell, Hopper, Rubin, true},
		// An inverted range can only be a caller bug, so it admits nothing
		// rather than silently behaving like the range read the other way.
		{"inverted range does not admit its endpoints", Hopper, Rubin, Hopper, false},
		{"inverted range admits nothing", Blackwell, Rubin, Hopper, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.a.Between(tt.lo, tt.hi))
		})
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  Arch
		ok    bool
	}{
		{"kepler", "kepler", Kepler, true},
		{"maxwell", "maxwell", Maxwell, true},
		{"pascal", "pascal", Pascal, true},
		{"volta", "volta", Volta, true},
		{"turing", "turing", Turing, true},
		{"ampere", "ampere", Ampere, true},
		{"ada", "ada", Ada, true},
		// The spelling the shipped l40s profile carries.
		{"ada_lovelace alias", "ada_lovelace", Ada, true},
		{"hopper", "hopper", Hopper, true},
		{"blackwell", "blackwell", Blackwell, true},
		{"rubin", "rubin", Rubin, true},
		{"uppercase", "HOPPER", Hopper, true},
		{"mixed case alias", "Ada_Lovelace", Ada, true},
		{"surrounding space", "  hopper  ", Hopper, true},
		{"typo is rejected", "hopperr", Unknown, false},
		{"empty is rejected", "", Unknown, false},
		{"whitespace only is rejected", "   ", Unknown, false},
		// "unknown" is how the type renders itself, but it is not a
		// configurable generation, so it must not round-trip back in.
		{"the word unknown is rejected", "unknown", Unknown, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := Parse(tt.input)
			require.Equal(t, tt.ok, ok, "Parse(%q) ok", tt.input)
			require.Equal(t, tt.want, got, "Parse(%q) value", tt.input)
		})
	}
}

// TestStringRoundTrip pins that every generation's canonical name parses back
// to itself, which is what keeps String and Parse from drifting apart.
func TestStringRoundTrip(t *testing.T) {
	t.Parallel()
	for _, a := range ordered {
		got, ok := Parse(a.String())
		require.True(t, ok, "%s must parse back from its own String()", a)
		require.Equal(t, a, got)
	}
}

func TestString(t *testing.T) {
	t.Parallel()
	require.Equal(t, "hopper", Hopper.String())
	// The alias is an input spelling only; the canonical rendering is "ada".
	require.Equal(t, "ada", Ada.String())
	require.Equal(t, "unknown", Unknown.String())
	// A value NVML adds before this package names it must still be
	// identifiable in a log line rather than rendering as an empty string.
	require.Equal(t, "arch(11)", Arch(11).String())
}
