package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureLogger returns a slog.Logger that writes to the provided buffer
// using the text handler. Tests inspect the buffer to verify the right
// log level / message / fields are emitted.
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// captureStdout temporarily redirects os.Stdout for the duration of fn and
// returns whatever was written. Used to assert ::warning:: workflow
// annotations, which we emit via fmt.Println so they appear on stdout where
// GitHub Actions parses them.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	_ = w.Close()
	return <-done
}

// TestLoadPreviousIssuesPRs_MissingFile asserts that a non-existent path
// returns an empty IssuesPRsFile, no error, and an info-level "no prior
// cache" log entry.
//
// Mutation check: returning an error instead of the empty map causes the
// nil-error assertion to fail.
func TestLoadPreviousIssuesPRs_MissingFile(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	missing := filepath.Join(tmp, "does-not-exist.json")

	var buf bytes.Buffer
	logger := captureLogger(&buf)

	got, err := loadPreviousIssuesPRs(missing, logger)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if got.Repos == nil {
		t.Fatalf("Repos = nil; want empty map")
	}
	if len(got.Repos) != 0 {
		t.Fatalf("len(Repos) = %d; want 0", len(got.Repos))
	}
	if !strings.Contains(buf.String(), "no prior cache") {
		t.Fatalf("expected 'no prior cache' in log; got:\n%s", buf.String())
	}
}

// TestLoadPreviousIssuesPRs_ValidFile asserts a known-good JSON file
// round-trips into the parsed shape with the expected repo keys and
// fetchedAt values.
//
// Mutation check: dropping the json.Unmarshal call leaves Repos empty;
// the key assertion fails.
func TestLoadPreviousIssuesPRs_ValidFile(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "issues_prs.json")

	const fixture = `{
  "repos": {
    "nvidia/gpu-operator": {
      "fetchedAt": "2026-04-29T12:00:00Z",
      "issues": {"total": 42, "categories": {"bug": 12}, "ageBuckets": {"fresh":5,"recent":10,"aging":12,"stale":8,"ancient":7}, "velocity": {"daily": [], "weekly": []}},
      "pullRequests": {"total": 8, "categories": {}, "ageBuckets": {"fresh":3,"recent":2,"aging":2,"stale":1,"ancient":0}, "velocity": {"daily": [], "weekly": []}, "review": {"awaitingReview":3,"noReviewer":1,"avgDaysToFirstReview":1.5,"avgDaysToMerge":3.2}}
    }
  }
}`
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var buf bytes.Buffer
	logger := captureLogger(&buf)

	got, err := loadPreviousIssuesPRs(path, logger)
	if err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	entry, ok := got.Repos["nvidia/gpu-operator"]
	if !ok {
		t.Fatalf("Repos missing key 'nvidia/gpu-operator'; have keys: %v", keysOf(got.Repos))
	}
	if entry.FetchedAt != "2026-04-29T12:00:00Z" {
		t.Fatalf("FetchedAt = %q; want %q", entry.FetchedAt, "2026-04-29T12:00:00Z")
	}
	if entry.Issues.Total != 42 {
		t.Fatalf("issues.Total = %d; want 42", entry.Issues.Total)
	}
	if strings.Contains(buf.String(), "malformed") {
		t.Fatalf("unexpected 'malformed' in log:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "prior cache loaded") {
		t.Fatalf("expected 'prior cache loaded' in log:\n%s", buf.String())
	}
}

// keysOf is a small test helper for clearer assertion messages.
func keysOf(m map[string]RepoIssuesPRs) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestLoadPreviousIssuesPRs_MalformedJSON asserts that:
//   - Function returns empty IssuesPRsFile with nil error.
//   - slog records an error-level entry mentioning "malformed".
//   - stdout contains a ::warning title=issues_prs cache:: annotation
//     (the GitHub Actions surface that makes degraded runs visible in
//     the workflow summary UI).
//
// Mutation check: removing the fmt.Println line drops the annotation;
// the stdout assertion fails. Replacing the warn-and-return with
// log.Fatal causes os.Exit which the test harness traps via subprocess.
func TestLoadPreviousIssuesPRs_MalformedJSON(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "issues_prs.json")
	if err := os.WriteFile(path, []byte("{garbage not json"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var buf bytes.Buffer
	logger := captureLogger(&buf)

	var got IssuesPRsFile
	var err error
	stdout := captureStdout(t, func() {
		got, err = loadPreviousIssuesPRs(path, logger)
	})

	if err != nil {
		t.Fatalf("err = %v; want nil (malformed should NOT propagate as error)", err)
	}
	if len(got.Repos) != 0 {
		t.Fatalf("len(Repos) = %d; want 0 on malformed", len(got.Repos))
	}
	if !strings.Contains(buf.String(), "malformed") {
		t.Fatalf("slog missing 'malformed':\n%s", buf.String())
	}
	if !strings.Contains(stdout, "::warning") || !strings.Contains(stdout, "issues_prs cache") {
		t.Fatalf("stdout missing GitHub Actions ::warning::; got:\n%s", stdout)
	}
}

// TestLoadPreviousIssuesPRs_IOErrorReturnsError pins the contract that the
// function returns a non-nil error when os.ReadFile fails for reasons other
// than ErrNotExist. main() relies on this to surface unexpected I/O issues
// (permission denied, EISDIR, etc.) instead of silently degrading to an
// empty cache for an entire deploy run.
//
// We trigger EISDIR by passing a directory path to ReadFile.
//
// Mutation check: returning nil error from the os.ReadFile branch (e.g.,
// always falling through to the "no prior cache" path) would cause this
// test to fail with err == nil.
func TestLoadPreviousIssuesPRs_IOErrorReturnsError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	asFile := filepath.Join(dir, "issues_prs.json")
	if err := os.Mkdir(asFile, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	var buf bytes.Buffer
	logger := captureLogger(&buf)

	got, err := loadPreviousIssuesPRs(asFile, logger)
	if err == nil {
		t.Fatalf("err = nil; want non-nil for directory-as-path I/O failure")
	}
	if !strings.Contains(err.Error(), "loadPreviousIssuesPRs read") {
		t.Fatalf("err = %v; want it wrapped with 'loadPreviousIssuesPRs read' context", err)
	}
	if got.Repos == nil {
		t.Fatalf("Repos = nil; want empty map alongside the error")
	}
}

// TestLoadPreviousIssuesPRs_NonFatalRegression asserts the function does
// not call log.Fatal on malformed JSON. We use the standard Go subprocess
// pattern: re-exec the test binary with an env flag that runs only the
// malformed-JSON code path and inspect the exit code.
//
// Mutation check: replace the warn-and-return-empty branch with
// log.Fatalf("malformed: %v", jerr) — the subprocess exits non-zero,
// the assertion fires.
func TestLoadPreviousIssuesPRs_NonFatalRegression(t *testing.T) {
	t.Parallel()

	if os.Getenv("LOAD_PREVIOUS_FATAL_PROBE") == "1" {
		// Subprocess mode: run the malformed-JSON path and exit 0 on success.
		path := filepath.Join(t.TempDir(), "issues_prs.json")
		_ = os.WriteFile(path, []byte("{garbage"), 0o644)
		_, _ = loadPreviousIssuesPRs(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
		os.Exit(0)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=TestLoadPreviousIssuesPRs_NonFatalRegression$", "-test.v")
	cmd.Env = append(os.Environ(), "LOAD_PREVIOUS_FATAL_PROBE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess exited non-zero: %v\noutput:\n%s", err, out)
	}
}

// TestRunIssuesPRsPhase_CacheFallback covers the heart of PR-B: when one
// repo's fetch fails and another succeeds, the failing repo is restored
// from cache (with prev fetchedAt preserved) and the successful repo
// gets fresh data (with current fetchedAt, NOT the cached one).
//
// Both assertions are required (QA "paired assertion" item from rev1):
//   - drop fallback branch → repo A omitted → fails
//   - always use cache → repo B has stale fetchedAt → fails
//
// We use a single httptest.Server that switches behavior based on the
// path's repo segment: 500 server error for repo A, 200 with empty
// issues for repo B. The cache-fallback branch in runIssuesPRsPhase
// triggers on ANY error from fetchIssuesPRs, not specifically rate-limit
// errors. Using 500 (vs the rate-limit 403 that the original plan
// proposed) avoids poisoning the gofri middleware's per-category state,
// which would otherwise short-circuit repo-b's concurrent request and
// flake the test under -race (~67% failure rate). See plan deviation
// note.
func TestRunIssuesPRsPhase_CacheFallback(t *testing.T) {
	t.Parallel()

	const cachedFetchedAt = "2026-04-15T08:00:00Z"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Path looks like /repos/owner/name/issues
		switch {
		case strings.Contains(r.URL.Path, "/repos/test/repo-a/"):
			// Server error — exercises the cache-fallback branch without
			// poisoning the rate-limit middleware's per-category state.
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"internal server error"}`)
		case strings.Contains(r.URL.Path, "/repos/test/repo-b/"):
			// Success — empty issue/PR list.
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[]`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"not found"}`)
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	client, ctx := buildClient(ctx, "fake-token", silentLogger())
	parsed, err := client.BaseURL.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	client.BaseURL = parsed

	cache := IssuesPRsFile{
		Repos: map[string]RepoIssuesPRs{
			"test/repo-a": {
				FetchedAt: cachedFetchedAt,
				Issues: IssueStats{
					Total:      99,
					Categories: map[string]int{"bug": 50},
					AgeBuckets: AgeBuckets{Fresh: 10, Recent: 20, Aging: 30, Stale: 25, Ancient: 14},
					Velocity:   Velocity{Daily: []VelocityDay{}, Weekly: []VelocityWeek{}},
				},
				PullRequests: PRStats{
					Total: 5, Categories: map[string]int{}, AgeBuckets: AgeBuckets{},
					Velocity: Velocity{Daily: []VelocityDay{}, Weekly: []VelocityWeek{}}, Review: PRReviewMetrics{},
				},
			},
		},
	}

	var buf bytes.Buffer
	logger := captureLogger(&buf)

	got := runIssuesPRsPhase(
		ctx, client,
		[]string{"test/repo-a", "test/repo-b"},
		5*time.Second,
		2,
		cache,
		logger,
	)

	// repo-a: should be present from cache, with cached fetchedAt.
	a, ok := got.Repos["test/repo-a"]
	if !ok {
		t.Fatalf("expected repo-a from cache; got keys: %v\nlog:\n%s", keysOf(got.Repos), buf.String())
	}
	if a.FetchedAt != cachedFetchedAt {
		t.Fatalf("repo-a fetchedAt = %q; want cached %q", a.FetchedAt, cachedFetchedAt)
	}
	if a.Issues.Total != 99 {
		t.Fatalf("repo-a issues.total = %d; want cached 99", a.Issues.Total)
	}

	// repo-b: should be present with FRESH fetchedAt (NOT the cache value).
	b, ok := got.Repos["test/repo-b"]
	if !ok {
		t.Fatalf("expected repo-b fresh; got keys: %v", keysOf(got.Repos))
	}
	if b.FetchedAt == cachedFetchedAt {
		t.Fatalf("repo-b fetchedAt = %q; want a fresh timestamp (NOT the cached value — fallback wrongly used)", b.FetchedAt)
	}
	if b.FetchedAt == "" {
		t.Fatalf("repo-b fetchedAt is empty; expected a fresh ISO8601 timestamp from this run")
	}

	// Log assertions to confirm the right messages fired.
	if !strings.Contains(buf.String(), "using cached data") {
		t.Fatalf("expected 'using cached data' log line; got:\n%s", buf.String())
	}
}

// TestRunIssuesPRsPhase_NoCacheOmit covers the second branch of cache
// fallback: when a repo fails AND has no cache entry, it's omitted from
// the output entirely (and a 'no data and no prior cache' warning is
// logged). The dashboard's existing data.repos[slug] filter naturally
// hides repos without data. Same 500-vs-403 deviation as the sibling
// test; kept consistent.
func TestRunIssuesPRsPhase_NoCacheOmit(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"message":"internal server error"}`)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	client, ctx := buildClient(ctx, "fake-token", silentLogger())
	parsed, _ := client.BaseURL.Parse(srv.URL + "/")
	client.BaseURL = parsed

	emptyCache := IssuesPRsFile{Repos: map[string]RepoIssuesPRs{}}

	var buf bytes.Buffer
	logger := captureLogger(&buf)

	got := runIssuesPRsPhase(
		ctx, client,
		[]string{"test/no-cache-repo"},
		5*time.Second,
		2,
		emptyCache,
		logger,
	)

	if _, exists := got.Repos["test/no-cache-repo"]; exists {
		t.Fatalf("expected repo to be omitted; got: %v", got.Repos)
	}
	if !strings.Contains(buf.String(), "no data and no prior cache") {
		t.Fatalf("expected 'no data and no prior cache' log; got:\n%s", buf.String())
	}
}
