package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// e2eFixtureTimes carries the concrete event timestamps used by the
// deterministic e2e fixture so that assertions can locate the expected
// daily-bucket index without reverse-engineering the math.
type e2eFixtureTimes struct {
	Now            time.Time
	ClosedIssue1At time.Time // plain closed issue, ~10d ago
	ClosedIssue2At time.Time // plain closed issue, ~30d ago
	ClosedPRAsIss  time.Time // closed PR-as-issue, ~5d ago
	MergedPR1At    time.Time // merged PR, ~7d ago
	MergedPR2At    time.Time // merged PR, ~50d ago
}

// runE2EClosedAndMerged spins up a stub GitHub server with two pages of
// open issues, three closed items (2 plain issues + 1 PR-as-issue), and
// two merged PRs, then drives fetchIssuesPRs against it. Returns the
// aggregated result and the fixture timestamps so the caller can assert
// on bucket placement.
func runE2EClosedAndMerged(t *testing.T) (RepoIssuesPRs, e2eFixtureTimes) {
	t.Helper()
	now := time.Now().UTC()
	ft := e2eFixtureTimes{
		Now:            now,
		ClosedIssue1At: now.Add(-10 * 24 * time.Hour),
		ClosedIssue2At: now.Add(-30 * 24 * time.Hour),
		ClosedPRAsIss:  now.Add(-5 * 24 * time.Hour),
		MergedPR1At:    now.Add(-7 * 24 * time.Hour),
		MergedPR2At:    now.Add(-50 * 24 * time.Hour),
	}

	openIssuesPage1 := buildIssuesJSON([]issueFixture{
		{Number: 100, State: "open", CreatedAt: now.Add(-3 * 24 * time.Hour), Labels: []string{"bug"}},
		{Number: 101, State: "open", CreatedAt: now.Add(-30 * 24 * time.Hour), Labels: []string{"feature"}},
	})
	openIssuesPage2 := buildIssuesJSON([]issueFixture{
		{Number: 102, State: "open", CreatedAt: now.Add(-100 * 24 * time.Hour), Labels: []string{"bug"}},
	})
	closedIssuesJSON := buildIssuesJSON([]issueFixture{
		{
			Number:    200,
			State:     "closed",
			CreatedAt: ft.ClosedIssue1At.Add(-2 * 24 * time.Hour),
			ClosedAt:  &ft.ClosedIssue1At,
			Labels:    []string{"bug"},
		},
		{
			Number:    201,
			State:     "closed",
			CreatedAt: ft.ClosedIssue2At.Add(-3 * 24 * time.Hour),
			ClosedAt:  &ft.ClosedIssue2At,
			Labels:    []string{"feature"},
		},
		{
			Number:        300,
			State:         "closed",
			CreatedAt:     ft.ClosedPRAsIss.Add(-1 * 24 * time.Hour),
			ClosedAt:      &ft.ClosedPRAsIss,
			IsPullRequest: true,
			Labels:        []string{"bug"},
		},
	})
	mergedPRsJSON := buildPRsJSON([]prFixture{
		{
			Number:    400,
			CreatedAt: ft.MergedPR1At.Add(-30 * 24 * time.Hour),
			MergedAt:  ft.MergedPR1At,
		},
		{
			Number:    401,
			CreatedAt: ft.MergedPR2At.Add(-30 * 24 * time.Hour),
			MergedAt:  ft.MergedPR2At,
		},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/issues") && r.URL.Query().Get("state") == "open":
			page := r.URL.Query().Get("page")
			if page == "" || page == "1" {
				w.Header().Set("Link", `<`+srvURLOf(r)+`/repos/test/repo/issues?state=open&page=2>; rel="next"`)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, openIssuesPage1)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, openIssuesPage2)
			return
		case strings.Contains(r.URL.Path, "/issues") && r.URL.Query().Get("state") == "closed":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, closedIssuesJSON)
			return
		case strings.Contains(r.URL.Path, "/pulls"):
			// Branch on state: state=open is the open-PRs path (empty in
			// this fixture), state=closed is the merged-PRs path.
			if r.URL.Query().Get("state") == "closed" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, mergedPRsJSON)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "[]")
			return
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "[]")
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	client, ctx := buildClient(ctx, "fake-token", silentLogger())
	parsed, _ := client.BaseURL.Parse(srv.URL + "/")
	client.BaseURL = parsed

	got, err := fetchIssuesPRs(ctx, client, "test/repo")
	if err != nil {
		t.Fatalf("fetchIssuesPRs: %v", err)
	}
	return got, ft
}

// TestFetchIssuesPRs_EndToEnd_DeterministicFixture exercises fetchIssuesPRs
// against a stub server returning multi-page paginated responses with
// Link: <...>; rel="next" headers. Asserts:
//   - velocity.Daily length is exactly the days-in-1y count.
//   - velocity.Weekly length is approximately 260 weeks (260-262 range
//     covers calendar drift: 5y = 1826 or 1827 days = 260.86 weeks, and
//     the iteration in buildVelocitySlice can touch one extra ISO week
//     when the start/end boundaries straddle a week change).
//   - Totals match the fixture's exact counts (not "approximately").
//   - Categories are populated from the fixture's labels.
//   - Closed-issue + closed-PR (PR-as-issue) aggregation paths produce the
//     correct daily-bucket sums and land in the expected day indices.
//   - Merged-PR aggregation produces correct daily-bucket sums and lands in
//     the expected day index.
//
// Mutation check: dropping the open-issues pagination loop (break after
// page 1) undercounts events; assertions on totals fail.
// Mutation check: removing the closed-events loop (artifact_fetcher.go
// `for _, iss := range closedItems`) fails the closed/merged sum
// assertions for issues and PR-as-issue.
// Mutation check: removing the merged-events loop (artifact_fetcher.go
// `for _, pr := range mergedPRs`) fails the merged-sum assertions.
//
// Label note: uses bare `bug` / `feature` labels (mapped under _default in
// labelCategories at artifact_fetcher.go); the original plan used
// `kind/bug` / `kind/feature` which only map under repo-specific overrides.
func TestFetchIssuesPRs_EndToEnd_DeterministicFixture(t *testing.T) {
	t.Parallel()
	got, ft := runE2EClosedAndMerged(t)

	if got.Issues.Total != 3 {
		t.Fatalf("Issues.Total = %d; want 3 (fixture has 3 open across 2 pages)", got.Issues.Total)
	}
	if got.Issues.Categories["bug"] != 2 {
		t.Fatalf("Issues.Categories[bug] = %d; want 2", got.Issues.Categories["bug"])
	}
	if got.Issues.Categories["feature-request"] != 1 {
		t.Fatalf("Issues.Categories[feature-request] = %d; want 1", got.Issues.Categories["feature-request"])
	}
	if len(got.Issues.Velocity.Daily) != 365 {
		t.Fatalf("Issues.Velocity.Daily length = %d; want 365", len(got.Issues.Velocity.Daily))
	}
	// 5y = 1826 or 1827 days = 260.86 ISO weeks. The iteration in
	// buildVelocitySlice steps in 7-day increments from fiveYearsAgo to
	// now and bucketizes each step by isoWeek (Monday of that week). If
	// the start or end falls near a week boundary, the iteration can touch
	// one extra ISO week, producing 262. Allow 260-262 to absorb the drift.
	if len(got.Issues.Velocity.Weekly) < 260 || len(got.Issues.Velocity.Weekly) > 262 {
		t.Fatalf("Issues.Velocity.Weekly length = %d; want 260-262", len(got.Issues.Velocity.Weekly))
	}

	// --- Closed-issue aggregation: two plain closed issues should land in
	// the issue daily-velocity stream, the PR-as-issue should NOT.
	var issueClosedSum int
	for _, d := range got.Issues.Velocity.Daily {
		issueClosedSum += d.Closed
	}
	if issueClosedSum != 2 {
		t.Fatalf("sum(Issues.Velocity.Daily[*].Closed) = %d; want 2 (two plain closed issues at -10d and -30d)", issueClosedSum)
	}

	// --- PR-as-issue aggregation: the closed PR-as-issue should land in
	// the PR daily-velocity stream's Closed field.
	var prClosedSum int
	for _, d := range got.PullRequests.Velocity.Daily {
		prClosedSum += d.Closed
	}
	if prClosedSum != 1 {
		t.Fatalf("sum(PullRequests.Velocity.Daily[*].Closed) = %d; want 1 (one closed PR-as-issue at -5d)", prClosedSum)
	}

	// --- Merged-PR aggregation: two merged PRs should land in the PR
	// daily-velocity stream's Merged field.
	var prMergedSum int
	prMergedHasNonZero := false
	for _, d := range got.PullRequests.Velocity.Daily {
		if d.Merged != nil {
			prMergedSum += *d.Merged
			if *d.Merged > 0 {
				prMergedHasNonZero = true
			}
		}
	}
	if !prMergedHasNonZero {
		t.Fatalf("PullRequests.Velocity.Daily contained no Merged>0 entries; want at least one (fixture has 2 merged PRs in 1y window)")
	}
	if prMergedSum != 2 {
		t.Fatalf("sum(*PullRequests.Velocity.Daily[*].Merged) = %d; want 2 (two merged PRs at -7d and -50d)", prMergedSum)
	}

	// --- Spot-check specific date buckets. The Daily array spans
	// [oneYearAgo, now) UTC, truncated to day. Index i corresponds to
	// from.AddDate(0, 0, i) in bucketByDay, where from = oneYearAgo
	// truncated to day.
	dailyIndexFor := func(eventAt time.Time) int {
		from := ft.Now.UTC().AddDate(-1, 0, 0).Truncate(24 * time.Hour)
		evDay := eventAt.UTC().Truncate(24 * time.Hour)
		return int(evDay.Sub(from) / (24 * time.Hour))
	}
	idxClosed1 := dailyIndexFor(ft.ClosedIssue1At)
	if idxClosed1 < 0 || idxClosed1 >= len(got.Issues.Velocity.Daily) {
		t.Fatalf("computed bucket index %d for ClosedIssue1At=%s out of range [0,%d)", idxClosed1, ft.ClosedIssue1At.Format(time.RFC3339), len(got.Issues.Velocity.Daily))
	}
	if got.Issues.Velocity.Daily[idxClosed1].Closed < 1 {
		t.Errorf("Issues.Velocity.Daily[%d] (date=%s) Closed = %d; want >=1 (event at -10d should land here)",
			idxClosed1, got.Issues.Velocity.Daily[idxClosed1].Date, got.Issues.Velocity.Daily[idxClosed1].Closed)
	}

	idxMerged1 := dailyIndexFor(ft.MergedPR1At)
	if idxMerged1 < 0 || idxMerged1 >= len(got.PullRequests.Velocity.Daily) {
		t.Fatalf("computed bucket index %d for MergedPR1At=%s out of range [0,%d)", idxMerged1, ft.MergedPR1At.Format(time.RFC3339), len(got.PullRequests.Velocity.Daily))
	}
	mergedAtIdx := got.PullRequests.Velocity.Daily[idxMerged1].Merged
	if mergedAtIdx == nil || *mergedAtIdx < 1 {
		var observed string
		if mergedAtIdx == nil {
			observed = "nil"
		} else {
			observed = fmt.Sprintf("%d", *mergedAtIdx)
		}
		t.Errorf("PullRequests.Velocity.Daily[%d] (date=%s) Merged = %s; want >=1 (event at -7d should land here)",
			idxMerged1, got.PullRequests.Velocity.Daily[idxMerged1].Date, observed)
	}
}

// issueFixture is a minimal subset of the GitHub Issue API shape that
// tests need. buildIssuesJSON marshals it to JSON for the stub server.
//
// ClosedAt, when non-nil, emits the JSON field `closed_at`. IsPullRequest,
// when true, emits a `pull_request` object so go-github sets
// PullRequestLinks non-nil — that's how the Issues endpoint distinguishes
// PR-as-issue rows from plain issues.
type issueFixture struct {
	Number        int
	State         string
	CreatedAt     time.Time
	ClosedAt      *time.Time
	Labels        []string
	IsPullRequest bool
}

func buildIssuesJSON(items []issueFixture) string {
	var b strings.Builder
	b.WriteString("[")
	for i, it := range items {
		if i > 0 {
			b.WriteString(",")
		}
		var labels strings.Builder
		labels.WriteString("[")
		for j, l := range it.Labels {
			if j > 0 {
				labels.WriteString(",")
			}
			labels.WriteString(`{"name":"`)
			labels.WriteString(l)
			labels.WriteString(`"}`)
		}
		labels.WriteString("]")
		fmt.Fprintf(&b,
			`{"number":%d,"state":"%s","created_at":"%s","labels":%s`,
			it.Number, it.State, it.CreatedAt.Format(time.RFC3339), labels.String(),
		)
		if it.ClosedAt != nil {
			fmt.Fprintf(&b, `,"closed_at":"%s"`, it.ClosedAt.Format(time.RFC3339))
		}
		if it.IsPullRequest {
			b.WriteString(`,"pull_request":{"url":"http://example/pr"}`)
		}
		b.WriteString("}")
	}
	b.WriteString("]")
	return b.String()
}

// prFixture is a minimal subset of the GitHub PullRequest API shape used
// by the merged-PR aggregation path. CreatedAt and MergedAt must both be
// set; MergedAt should be inside the 5y window so the fetcher's
// pastCutoff break does not fire prematurely.
type prFixture struct {
	Number    int
	CreatedAt time.Time
	MergedAt  time.Time
}

func buildPRsJSON(items []prFixture) string {
	var b strings.Builder
	b.WriteString("[")
	for i, it := range items {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b,
			`{"number":%d,"state":"closed","created_at":"%s","merged_at":"%s"}`,
			it.Number, it.CreatedAt.Format(time.RFC3339), it.MergedAt.Format(time.RFC3339),
		)
	}
	b.WriteString("]")
	return b.String()
}

func srvURLOf(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// TestFetchIssuesPRs_EmptyResponse asserts that a stub returning empty
// arrays yields velocity.Daily and Weekly with exact zero entries (not
// nil), so the dashboard's empty-state placeholder triggers correctly.
//
// Mutation check: a special-case "if no events, return nil arrays"
// fails the length assertion.
// TestFetchIssuesPRs_PaginationCap pins the per-loop pagination cap that
// prevents runaway page-walking on a repo with very long history. Without
// the cap, the fetcher pages indefinitely through every closed issue whose
// updated_at falls within the 5y window — GitHub's `Since` filter uses
// updated_at, not created_at, so old issues with recent comments stay in
// the result set. Long-lived repos (gpu-operator, k8s-device-plugin) have
// thousands of such issues; without a cap the deploy stalls for hours and
// exhausts the rate-limit budget. Verified empirically on the cancelled
// run 2026-05-11T13:12:35Z (5h 20m of silence post traffic phase).
//
// The stub server always returns a full page (100 issues) AND advertises a
// next page via Link header, simulating an infinite-pagination repo. The
// fetcher must stop at maxIssuesPRsPages and return cleanly.
//
// Mutation: removing the page-cap exit causes the fetcher to keep paging
// past 30 calls; the openCalls assertion fires.
func TestFetchIssuesPRs_PaginationCap(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()

	var openCalls int32
	var openCallsMu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/issues") || r.URL.Query().Get("state") != "open" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, "[]")
			return
		}
		openCallsMu.Lock()
		openCalls++
		thisPage := openCalls
		openCallsMu.Unlock()

		// Always advertise a "next" page — the fetcher never sees end-of-list.
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos/test/repo/issues?state=open&page=%d>; rel="next"`, srvURLOf(r), thisPage+1))
		w.Header().Set("Content-Type", "application/json")

		// 100 distinct open issues per page, all with createdAt in the last year
		// so they all land in the velocity window.
		fixtures := make([]issueFixture, 0, 100)
		for i := 0; i < 100; i++ {
			fixtures = append(fixtures, issueFixture{
				Number:    int(thisPage)*1000 + i,
				State:     "open",
				CreatedAt: now.Add(-time.Duration(i) * time.Hour),
				Labels:    []string{"bug"},
			})
		}
		_, _ = io.WriteString(w, buildIssuesJSON(fixtures))
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	client, ctx := buildClient(ctx, "fake-token", silentLogger())
	parsed, _ := client.BaseURL.Parse(srv.URL + "/")
	client.BaseURL = parsed

	got, err := fetchIssuesPRs(ctx, client, "test/repo")
	if err != nil {
		t.Fatalf("fetchIssuesPRs: %v", err)
	}

	// The cap is maxIssuesPRsPages; verify the fetcher honored it.
	if openCalls > int32(maxIssuesPRsPages) {
		t.Errorf("open-issues API calls = %d; want <= %d (page cap leaked)", openCalls, maxIssuesPRsPages)
	}
	if openCalls < int32(maxIssuesPRsPages) {
		t.Errorf("open-issues API calls = %d; want exactly %d (cap should be reached)", openCalls, maxIssuesPRsPages)
	}
	// And the events do actually flow into the aggregator (100 per page × cap).
	wantTotal := int(maxIssuesPRsPages) * 100
	if got.Issues.Total != wantTotal {
		t.Errorf("Issues.Total = %d; want %d (cap × per_page)", got.Issues.Total, wantTotal)
	}
}

func TestFetchIssuesPRs_EmptyResponse(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "[]")
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	client, ctx := buildClient(ctx, "fake-token", silentLogger())
	parsed, _ := client.BaseURL.Parse(srv.URL + "/")
	client.BaseURL = parsed

	got, err := fetchIssuesPRs(ctx, client, "test/empty")
	if err != nil {
		t.Fatalf("fetchIssuesPRs: %v", err)
	}
	if got.Issues.Total != 0 {
		t.Fatalf("Issues.Total = %d; want 0", got.Issues.Total)
	}
	if len(got.Issues.Velocity.Daily) != 365 {
		t.Fatalf("Daily length = %d; want 365 (continuous-zeros)", len(got.Issues.Velocity.Daily))
	}
	for i, d := range got.Issues.Velocity.Daily {
		if d.Opened != 0 || d.Closed != 0 || d.Merged != nil {
			t.Errorf("daily[%d] = %+v; want all-zero", i, d)
		}
	}
}
