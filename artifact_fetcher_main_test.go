package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// silentLogger returns a slog.Logger that writes to io.Discard so tests don't
// pollute output. Use t.Logf-backed loggers when assertions on log lines are
// needed (see artifact_fetcher_log_test.go).
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestBuildClient_RetriesOnSecondaryRateLimit asserts that the rate-limit
// middleware is wired such that a 429 response with Retry-After is
// transparently retried, and the caller observes the eventual 200. buildClient
// is the entry-point of the pipeline that ultimately produces TestResult
// records, so a regression here breaks every artifact fetch downstream.
//
// Mutation check: if buildClient forgets to wrap the OAuth2 transport with
// github_ratelimit.New, the 429 surfaces to the caller as a
// *github.AbuseRateLimitError on the first call and the test fails because
// the body of the second-call request is never observed.
func TestBuildClient_RetriesOnSecondaryRateLimit(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			// Secondary rate limit response per GitHub docs.
			w.Header().Set("Retry-After", "1")
			w.Header().Set("X-RateLimit-Remaining", "5000")
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`)
			return
		}
		// Second call: success.
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"login":"test","id":1}`)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	client, ctx := buildClient(ctx, "fake-token", silentLogger())
	// Point the client at the test server.
	baseURL := srv.URL + "/"
	parsed, err := client.BaseURL.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse base URL: %v", err)
	}
	client.BaseURL = parsed

	// Issue a real request through the wired chain.
	req, err := client.NewRequest("GET", "user", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	var got map[string]any
	resp, err := client.Do(ctx, req, &got)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200", resp.StatusCode)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d; want exactly 2 (one 429, one 200)", calls.Load())
	}
	if got["login"] != "test" {
		t.Fatalf("body login = %v; want %q", got["login"], "test")
	}
}

// TestBuildClient_BypassRateLimitCheckSet asserts that the returned context
// has go-github's BypassRateLimitCheck flag set, which is required by the
// library so go-github does not pre-empt the middleware's retry logic.
//
// Mutation check: removing the context.WithValue call returns a zero value
// instead of true, and this assertion fails.
func TestBuildClient_BypassRateLimitCheckSet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, ctx = buildClient(ctx, "fake-token", silentLogger())

	// go-github exposes BypassRateLimitCheck as a context key. Confirm it's true.
	got, _ := ctx.Value(githubBypassKey()).(bool)
	if !got {
		t.Fatalf("BypassRateLimitCheck context value = %v; want true", got)
	}
}

// githubBypassKey returns the context key that go-github checks. Defined as
// a function so the test can call into the same package-level helper that
// production code uses (see githubBypass in artifact_fetcher.go), keeping
// the lookup honest.
func githubBypassKey() any {
	return githubBypass
}
