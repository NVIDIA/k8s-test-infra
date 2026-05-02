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
