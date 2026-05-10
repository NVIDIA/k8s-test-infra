/*
 * Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 */

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/google/go-github/v55/github"
)

// countingTransport wraps an http.RoundTripper so a test can assert that a
// caller routed through THIS transport rather than http.DefaultClient.
type countingTransport struct {
	calls atomic.Int32
	inner http.RoundTripper
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return c.inner.RoundTrip(r)
}

// TestDownload_UsesSuppliedClient asserts that download() issues its HTTP
// request through the *http.Client passed by the caller — i.e., NOT through
// http.DefaultClient. This is the contract that lets main()'s rate-limit
// middleware cover artifact-zip downloads.
//
// Mutation check: reverting download to http.DefaultClient.Do(req) would
// leave countingTransport.calls at 0 and this test fails.
func TestDownload_UsesSuppliedClient(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("zip-bytes"))
	}))
	t.Cleanup(srv.Close)

	tr := &countingTransport{inner: http.DefaultTransport}
	httpClient := &http.Client{Transport: tr}

	dest := filepath.Join(t.TempDir(), "out.zip")
	if err := download(context.Background(), httpClient, srv.URL, dest, "fake-token"); err != nil {
		t.Fatalf("download: %v", err)
	}

	if got := tr.calls.Load(); got != 1 {
		t.Fatalf("countingTransport.calls = %d; want 1 (download must route through supplied client)", got)
	}

	// Sanity: file was written.
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(b) != "zip-bytes" {
		t.Fatalf("dest contents = %q; want %q", string(b), "zip-bytes")
	}
}

// TestFetchRepoInfo_ReadmeUsesClientTransport asserts that the README sub-fetch
// inside fetchRepoInfo flows through the *github.Client's underlying http.Client
// (returned by client.Client()) rather than http.DefaultClient.
//
// Strategy: construct a *github.Client wrapping an *http.Client whose Transport
// is a counting+redirecting RoundTripper. Point client.BaseURL at an httptest
// server that serves both /repos/{owner}/{name} and /repos/{owner}/{name}/readme.
// The README fetch inside fetchRepoInfo uses an absolute URL pointing at
// api.github.com, so the redirect transport rewrites the host to the test
// server regardless of the original URL. If the implementation routes through
// client.Client() we see >=2 calls; if it uses http.DefaultClient we see 1.
//
// Mutation check: reverting fetchRepoInfo's readme call to
// http.DefaultClient.Do(req) leaves count at 1 and this test fails.
func TestFetchRepoInfo_ReadmeUsesClientTransport(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/n":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"name":"n","full_name":"o/n"}`))
		case "/repos/o/n/readme":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<p>readme</p>"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	// Redirect transport: ANY request, regardless of host, lands on srv.
	redirect := &redirectTransport{target: srv.URL, inner: http.DefaultTransport}
	tr := &countingTransport{inner: redirect}

	httpClient := &http.Client{Transport: tr}
	client := github.NewClient(httpClient)
	parsed, err := client.BaseURL.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	client.BaseURL = parsed

	if _, err := fetchRepoInfo(context.Background(), client, "o/n"); err != nil {
		t.Fatalf("fetchRepoInfo: %v", err)
	}

	// Expect 2 calls: Repositories.Get + readme. If readme used
	// http.DefaultClient we'd see only 1.
	if got := tr.calls.Load(); got < 2 {
		t.Fatalf("countingTransport.calls = %d; want >=2 (readme must route through client.Client())", got)
	}
}

// redirectTransport rewrites every outbound request URL to a fixed target host
// before delegating, so tests can intercept absolute URLs (e.g. api.github.com)
// without DNS or network access.
type redirectTransport struct {
	target string
	inner  http.RoundTripper
}

func (rt *redirectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// Build a new URL that keeps the path/query but uses target's scheme+host.
	u := *r.URL
	// Parse target lazily.
	tgt, err := http.NewRequest(http.MethodGet, rt.target, nil)
	if err != nil {
		return nil, err
	}
	u.Scheme = tgt.URL.Scheme
	u.Host = tgt.URL.Host
	r2 := r.Clone(r.Context())
	r2.URL = &u
	r2.Host = tgt.URL.Host
	return rt.inner.RoundTrip(r2)
}
