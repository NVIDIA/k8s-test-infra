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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v55/github"
)

// TestRunIssuesPRsPhase_DRARepoMigration asserts that the issues/PRs phase,
// when seeded with the package-level allRepos, routes a request to
// /repos/kubernetes-sigs/dra-driver-nvidia-gpu/... and never to the
// donated-away nvidia/k8s-dra-driver-gpu path.
//
// NVIDIA donated the DRA driver to kubernetes-sigs on 2026-04-30. The old
// path 301-redirects, but the dashboard's per-repo cache is keyed on the
// literal repo string used here, so a stale literal silently fragments
// cache lookups under the wrong owner.
//
// Strategy: stub a server that accepts any /repos/... path with an empty
// list, redirect-transport every request to it, then call runIssuesPRsPhase
// with allRepos. Record observed paths; assert presence of the new owner
// substring and absence of the old.
//
// Mutation check: reverting the allRepos literal back to
// "nvidia/k8s-dra-driver-gpu" causes the old-path assertion to fire and
// this test fails. This narrowly guards the Task 9 literal change; Task 11
// adds a broader static-shape test covering all four locations (defaultRepos,
// allRepos, defaultImages, src/data/projects.ts).
//
// Spec: docs/plans/2026-04-30-dra-repo-migration-design.md (folded into PR-B).
func TestRunIssuesPRsPhase_DRARepoMigration(t *testing.T) {
	t.Parallel()

	const (
		oldOwnerPrefix = "/repos/nvidia/k8s-dra-driver-gpu"
		newOwnerPrefix = "/repos/kubernetes-sigs/dra-driver-nvidia-gpu"
	)

	var (
		mu            sync.Mutex
		observedPaths []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		observedPaths = append(observedPaths, r.URL.Path)
		mu.Unlock()
		// Empty list satisfies fetchIssuesPRs's pagination loop without
		// requiring full GitHub-shaped fixtures; the request URL is what
		// this test cares about.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	redirect := &redirectTransport{target: srv.URL, inner: http.DefaultTransport}
	httpClient := &http.Client{Transport: redirect}
	client := github.NewClient(httpClient)
	parsed, err := client.BaseURL.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	client.BaseURL = parsed

	out := runIssuesPRsPhase(
		context.Background(),
		client,
		allRepos,
		2*time.Second,
		4,
		IssuesPRsFile{Repos: map[string]RepoIssuesPRs{}},
		silentLogger(),
	)
	if out.Repos == nil {
		t.Fatalf("runIssuesPRsPhase returned nil Repos map")
	}

	mu.Lock()
	defer mu.Unlock()

	var sawNew, sawOld bool
	for _, p := range observedPaths {
		if strings.HasPrefix(p, newOwnerPrefix) {
			sawNew = true
		}
		if strings.HasPrefix(p, oldOwnerPrefix) {
			sawOld = true
		}
	}

	if sawOld {
		t.Errorf("observed request to stale path prefix %q; allRepos must use %q after donation to kubernetes-sigs (2026-04-30)",
			oldOwnerPrefix, newOwnerPrefix)
	}
	if !sawNew {
		t.Errorf("no request to migrated path prefix %q; allRepos must reference kubernetes-sigs/dra-driver-nvidia-gpu (paths seen: %v)",
			newOwnerPrefix, observedPaths)
	}
}
