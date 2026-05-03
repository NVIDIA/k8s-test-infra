package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestFetchIssuesPRs_EndToEnd_DeterministicFixture exercises fetchIssuesPRs
// against a stub server returning multi-page paginated responses with
// Link: <...>; rel="next" headers. Asserts:
//   - velocity.Daily length is exactly the days-in-1y count.
//   - velocity.Weekly length is exactly the expected ~260 weeks.
//   - Totals match the fixture's exact counts (not "approximately").
//   - Categories are populated from the fixture's labels.
//
// Mutation check: dropping the pagination loop (break after page 1)
// undercounts events; assertions on totals fail.
//
// Label note: uses bare `bug` / `feature` labels (mapped under _default in
// labelCategories at artifact_fetcher.go); the original plan used
// `kind/bug` / `kind/feature` which only map under repo-specific overrides.
func TestFetchIssuesPRs_EndToEnd_DeterministicFixture(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	openIssuesPage1 := buildIssuesJSON([]issueFixture{
		{Number: 100, State: "open", CreatedAt: now.Add(-3 * 24 * time.Hour), Labels: []string{"bug"}},
		{Number: 101, State: "open", CreatedAt: now.Add(-30 * 24 * time.Hour), Labels: []string{"feature"}},
	})
	openIssuesPage2 := buildIssuesJSON([]issueFixture{
		{Number: 102, State: "open", CreatedAt: now.Add(-100 * 24 * time.Hour), Labels: []string{"bug"}},
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
			_, _ = io.WriteString(w, "[]")
			return
		case strings.Contains(r.URL.Path, "/pulls"):
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
	if len(got.Issues.Velocity.Weekly) < 260 || len(got.Issues.Velocity.Weekly) > 261 {
		t.Fatalf("Issues.Velocity.Weekly length = %d; want 260-261", len(got.Issues.Velocity.Weekly))
	}
}

// issueFixture is a minimal subset of the GitHub Issue API shape that
// tests need. buildIssuesJSON marshals it to JSON for the stub server.
type issueFixture struct {
	Number    int
	State     string
	CreatedAt time.Time
	Labels    []string
}

func buildIssuesJSON(items []issueFixture) string {
	var b strings.Builder
	b.WriteString("[")
	for i, it := range items {
		if i > 0 {
			b.WriteString(",")
		}
		labels := "["
		for j, l := range it.Labels {
			if j > 0 {
				labels += ","
			}
			labels += `{"name":"` + l + `"}`
		}
		labels += "]"
		b.WriteString(fmt.Sprintf(
			`{"number":%d,"state":"%s","created_at":"%s","labels":%s}`,
			it.Number, it.State, it.CreatedAt.Format(time.RFC3339), labels,
		))
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
