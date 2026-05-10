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

// TestBuildClient_BypassRateLimitCheckSet is a placeholder for the v75+
// behavioral assertion described on the bypass TODO above buildClient.
//
// In go-github v55 we do NOT stamp our own marker into the context, since
// v55's bypassRateLimitCheck is unexported and asserting our private marker
// would only confirm we set what we set — theater, not behavior.
//
// On the v75+ bump, replace this Skip with: configure a stub server to
// return a primary-rate-limit-exhausted response (X-RateLimit-Remaining: 0,
// X-RateLimit-Reset in the future) on a FIRST call so go-github's
// c.rateLimits[] state is populated, then make a SECOND call and assert it
// succeeds because github.BypassRateLimitCheck prevented the pre-empt.
// Without the bypass, the second call would short-circuit with a
// *github.RateLimitError before reaching the network.
//
// Until then, TestBuildClient_RetriesOnSecondaryRateLimit is the real
// safety net: it proves the middleware engages on the response path, which
// is the only behavior we care about with v55.
func TestBuildClient_BypassRateLimitCheckSet(t *testing.T) {
	t.Skip("placeholder: bypassRateLimitCheck is unexported in go-github v55; re-enable on v75+ bump with a behavioral assertion (see TODO in godoc).")
}

// TestBuildClient_SecondaryRateLimitCap asserts that the WithSingleSleepLimit
// cap aborts a too-long secondary-rate-limit sleep instead of blocking the
// caller indefinitely. We override secondarySleepCap to 1ms (test-only)
// and configure the test server to return a 60-second Retry-After WITH the
// canonical detector-matching body so the middleware actually engages.
//
// Mutation check: removing WithSingleSleepLimit entirely makes the call
// block until Retry-After elapses (60s) or the test's context times out.
// The 5-second test deadline asserts the cap fired well before that.
//
// Not parallel: this test mutates the package-level secondarySleepCap
// variable. Running concurrently with any other test that calls buildClient
// would observe the 1ms cap and behave unpredictably.
func TestBuildClient_SecondaryRateLimitCap(t *testing.T) {
	// Intentionally NOT t.Parallel() — see godoc above.

	// Reduce the cap for this test only.
	originalCap := secondarySleepCap
	secondarySleepCap = 1 * time.Millisecond
	t.Cleanup(func() { secondarySleepCap = originalCap })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.Header().Set("X-RateLimit-Remaining", "5000")
		w.WriteHeader(http.StatusForbidden)
		// Canonical body — gofri detector requires the prefix
		// "You have exceeded a secondary rate limit" (verified at
		// vendor/github.com/gofri/go-github-ratelimit/v2/.../detect.go:26).
		// A placeholder body that omits the prefix makes the middleware
		// classify as primary, NOT engage, and surface 403 to the caller —
		// rendering this cap test theatrical.
		_, _ = io.WriteString(w, `{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	client, ctx := buildClient(ctx, "fake-token", silentLogger())
	parsed, err := client.BaseURL.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	client.BaseURL = parsed

	req, err := client.NewRequest("GET", "user", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	start := time.Now()
	var got map[string]any
	_, err = client.Do(ctx, req, &got)
	elapsed := time.Since(start)

	// Either the middleware aborts (returning an error) or the caller's
	// context expires. Either way, total wall-clock time MUST be less than
	// the Retry-After (60s). 5s is the test deadline; assert <2s for margin.
	if elapsed >= 2*time.Second {
		t.Fatalf("call took %s; expected < 2s after cap fired", elapsed)
	}
	if err == nil {
		t.Fatalf("expected error after sleep cap fired; got success")
	}
}
