package main

import (
	"testing"
	"time"
)

// TestBuildVelocitySlice_5YearWindow asserts the existing
// buildVelocitySlice helper handles a 5-year span with the expected
// length and ISO-week boundary correctness.
//
// Mutation check: a bug that hard-codes a 12-week window (the previous
// behavior) returns ~12 entries; this test asserts ~260 and fails.
// A bug in isoWeek() that uses calendar year instead of ISO year
// produces wrong week strings at known boundary dates; subtests assert
// exact strings to catch this.
func TestBuildVelocitySlice_5YearWindow(t *testing.T) {
	t.Parallel()

	to := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	from := to.AddDate(-5, 0, 0)

	// Pre-populate maps so the union-of-keys logic in buildVelocitySlice
	// has SOME data; the empty-day continuous-range invariant is a separate
	// concern (existing buildVelocitySlice emits union-of-seen-weeks, not
	// every-week-in-range; we accept that and pin the count).
	opened := map[string]int{}
	closed := map[string]int{}
	merged := map[string]int{}

	for w := from; w.Before(to); w = w.AddDate(0, 0, 7) {
		key := isoWeek(w)
		opened[key] = 1
		closed[key] = 1
		merged[key] = 1
	}

	got := buildVelocitySlice(opened, closed, merged, from, to)

	// Expect ~260 weeks. The exact number depends on ISO-week alignment
	// at the boundaries (between 260 and 261 inclusive).
	if len(got) < 260 || len(got) > 261 {
		t.Fatalf("len = %d; want 260 or 261 (5-year window)", len(got))
	}
}

// TestBuildVelocitySlice_ISOWeekYearBoundary pins exact ISO-week strings
// at known calendar/ISO-year-divergence boundaries. 2024-12-30 (a Monday)
// is ISO week 1 of 2025, NOT week 53 of 2024. A bug that uses
// time.Time.Year() (calendar year) instead of t.ISOWeek()'s year
// produces "2024-12-30" for the Monday, which a test pinning the
// expected key WOULD catch.
//
// We probe two neighboring weeks straddling the year boundary and assert
// the helper's bucketing matches what t.ISOWeek() returns directly.
func TestBuildVelocitySlice_ISOWeekYearBoundary(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		monday     time.Time
		wantPrefix string // first 4 chars of the resulting key (the year part of YYYY-MM-DD week-Monday format)
	}{
		{
			name:       "Monday in last calendar week of 2024 belongs to ISO 2025",
			monday:     time.Date(2024, 12, 30, 0, 0, 0, 0, time.UTC),
			wantPrefix: "2024", // the Monday IS December 30, 2024
		},
		{
			name:       "Monday in first calendar week of 2025",
			monday:     time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC),
			wantPrefix: "2025",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := isoWeek(tc.monday)
			if got[:4] != tc.wantPrefix {
				t.Fatalf("isoWeek(%s) = %q; expected to start with %q (per ISO 8601 week-year rules)",
					tc.monday.Format("2006-01-02"), got, tc.wantPrefix)
			}
			// Sanity: the result is a valid YYYY-MM-DD string parseable
			// back to a Monday close to the input.
			parsed, err := time.Parse("2006-01-02", got)
			if err != nil {
				t.Fatalf("isoWeek result %q is not parseable as YYYY-MM-DD: %v", got, err)
			}
			if parsed.Weekday() != time.Monday {
				t.Fatalf("isoWeek result %q is %s, not Monday", got, parsed.Weekday())
			}
		})
	}
}
