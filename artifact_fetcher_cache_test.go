package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"os/exec"
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
