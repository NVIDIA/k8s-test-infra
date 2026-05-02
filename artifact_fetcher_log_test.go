package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestBuildClient_LogsNeverContainCredentials runs three paths (200,
// 429-then-200, 500) through the wired client and asserts that the
// captured slog output never contains the token literal, the substring
// "Bearer", or the substring "token=".
//
// buildClient is the entry-point of the pipeline that ultimately produces
// TestResult records, so a credential leak here would propagate into every
// downstream artifact fetch.
//
// Mutation check: if a future change adds slog.Info("auth header",
// "value", req.Header.Get("Authorization")), the captured buffer would
// contain "Bearer fake-secret-token" and the assertion fails.
func TestBuildClient_LogsNeverContainCredentials(t *testing.T) {
	t.Parallel()

	const tokenLiteral = "fake-secret-token-do-not-leak"

	cases := []struct {
		name    string
		handler http.HandlerFunc
		wantErr bool
	}{
		{
			name: "200 success",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"login":"x","id":1}`)
			},
			wantErr: false,
		},
		{
			name: "429 then 200",
			handler: func() http.HandlerFunc {
				var calls atomic.Int32
				return func(w http.ResponseWriter, r *http.Request) {
					if calls.Add(1) == 1 {
						w.Header().Set("Retry-After", "1")
						w.Header().Set("X-RateLimit-Remaining", "5000")
						w.WriteHeader(http.StatusForbidden)
						_, _ = io.WriteString(w, `{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"login":"x","id":1}`)
				}
			}(),
			wantErr: false,
		},
		{
			name: "500 error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"message":"internal"}`)
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(tc.handler)
			t.Cleanup(srv.Close)

			var buf bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			t.Cleanup(cancel)

			client, ctx := buildClient(ctx, tokenLiteral, logger)
			parsed, err := client.BaseURL.Parse(srv.URL + "/")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			client.BaseURL = parsed

			req, err := client.NewRequest("GET", "user", nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			var got map[string]any
			_, err = client.Do(ctx, req, &got)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v; wantErr %v", err, tc.wantErr)
			}

			out := buf.String()
			forbidden := []string{
				tokenLiteral,
				"Bearer",
				"token=",
				"Authorization",
			}
			for _, needle := range forbidden {
				if strings.Contains(out, needle) {
					t.Fatalf("log output contains forbidden substring %q\n--- log ---\n%s\n", needle, out)
				}
			}
		})
	}
}
