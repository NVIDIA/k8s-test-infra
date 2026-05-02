package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
      "issues": {"total": 42, "categories": {"bug": 12}, "ageBuckets": {"fresh":5,"recent":10,"aging":12,"stale":8,"ancient":7}, "velocity": []},
      "pullRequests": {"total": 8, "categories": {}, "ageBuckets": {"fresh":3,"recent":2,"aging":2,"stale":1,"ancient":0}, "velocity": [], "review": {"awaitingReview":3,"noReviewer":1,"avgDaysToFirstReview":1.5,"avgDaysToMerge":3.2}}
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
