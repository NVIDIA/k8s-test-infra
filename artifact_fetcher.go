/*
 * Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/go-github/v55/github"
	types "github.com/onsi/ginkgo/v2/types"
	"golang.org/x/oauth2"
)

// -----------------------------------------------------------------------------
// CLI flags
// -----------------------------------------------------------------------------
var (
	repoList     = flag.String("repos", "", "Comma-separated list of org/repo (default built‑ins)")
	artifactName = flag.String("artifact", "ginkgo-logs", "Artifact name to download (exact match)")
	outDir       = flag.String("out", "artifacts", "Directory for extracted artifacts")
	perPage      = flag.Int("perpage", 20, "Artifacts per page to query")
	timeout      = flag.Duration("timeout", 30*time.Second, "Per‑repo timeout")
	workers      = flag.Int("workers", runtime.NumCPU(), "Max parallel repo workers")
)

var defaultRepos = []string{
	"nvidia/nvidia-container-toolkit",
	"nvidia/k8s-device-plugin",
	"nvidia/k8s-dra-driver-gpu",
}

var defaultImages = []string{
	"nvidia/k8s-device-plugin",
	"nvidia/k8s-dra-driver-gpu",
	"nvidia/container-toolkit",
	"nvidia/gpu-operator",
	"nvidia/driver",
}

// TestResult corresponds to the schema consumed by Hugo.
// Adjust field names/tags carefully when evolving downstream expectations.

type TestResult struct {
	Project   string `json:"project"`
	LastRun   string `json:"lastRun"`
	Passed    int    `json:"passed"`
	Failed    int    `json:"failed"`
	ActionURL string `json:"actionRunUrl"`
}

type ImageInfo struct {
	Repo    string `json:"repo"`
	Tag     string `json:"tag"`
	Pushed  string `json:"pushedAt"`
	HTMLURL string `json:"htmlUrl"`
}

// -----------------------------------------------------------------------------
func main() {
	log.SetFlags(0)
	flag.Parse()

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Fatal("GITHUB_TOKEN not set")
	}

	repos := defaultRepos
	if *repoList != "" {
		repos = strings.Split(*repoList, ",")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	resCh := make(chan TestResult, len(repos))
	errCh := make(chan error, len(repos))

	sem := make(chan struct{}, *workers)
	var wg sync.WaitGroup

	for _, repo := range repos {
		repo = strings.TrimSpace(repo)
		if repo == "" {
			continue
		}
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			tr, err := processRepo(ctx, client, r, token, *artifactName, *perPage, *outDir, *timeout)
			if err != nil {
				errCh <- fmt.Errorf("%s: %w", r, err)
				return
			}
			resCh <- tr
		}(repo)
	}

	imageRepos := defaultImages
	imgCh := make(chan ImageInfo, len(imageRepos))
	for _, repo := range imageRepos {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			img, err := fetchLatestImageTag(ctx, client, r)
			if err != nil {
				errCh <- fmt.Errorf("image %s: %w", r, err)
				return
			}
			imgCh <- img
		}(repo)
	}

	wg.Wait()
	close(resCh)
	close(errCh)
	close(imgCh)

	for err := range errCh {
		log.Println("error:", err)
	}

	var results []TestResult
	for tr := range resCh {
		results = append(results, tr)
	}
	if len(results) == 0 {
		log.Fatal("no results produced")
	}

	if err := writeJSON(filepath.Join(*outDir, "results.json"), results); err != nil {
		log.Fatal(err)
	}

	// Write image info to a separate file
	var images []ImageInfo
	for img := range imgCh {
		images = append(images, img)
	}

	if err := writeJSON(filepath.Join(*outDir, "images.json"), images); err != nil {
		log.Fatal(err)
	}
}

// -----------------------------------------------------------------------------
// processRepo retrieves and parses the most recent artifact named artifactName.
// -----------------------------------------------------------------------------
func processRepo(parent context.Context, client *github.Client, repo, token, artifactName string, perPage int, outRoot string, timeout time.Duration) (TestResult, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return TestResult{}, fmt.Errorf("invalid repo: %s", repo)
	}
	owner, name := parts[0], parts[1]

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	arts, _, err := client.Actions.ListArtifacts(ctx, owner, name, &github.ListOptions{PerPage: perPage})
	if err != nil {
		return TestResult{}, err
	}
	if len(arts.Artifacts) == 0 {
		return TestResult{}, errors.New("no artifacts returned")
	}

	var art *github.Artifact
	for _, a := range arts.Artifacts { // newest first by default
		if a.GetName() == artifactName {
			art = a
			break
		}
	}
	if art == nil {
		return TestResult{}, fmt.Errorf("artifact %q not found", artifactName)
	}

	runID := art.GetWorkflowRun().GetID()
	actionURL := fmt.Sprintf("https://github.com/%s/actions/runs/%d", repo, runID)
	zipURL := art.GetArchiveDownloadURL()
	if zipURL == "" {
		return TestResult{}, errors.New("empty download url")
	}

	tmpZip := filepath.Join(os.TempDir(), fmt.Sprintf("%s_%d.zip", name, art.GetID()))
	if err := download(ctx, zipURL, tmpZip, token); err != nil {
		return TestResult{}, err
	}
	defer os.Remove(tmpZip)

	dest := filepath.Join(outRoot, name)
	if err := safeUnzip(tmpZip, dest); err != nil {
		return TestResult{}, err
	}

	tr, err := parse(filepath.Join(dest, "ginkgo.json"), name, actionURL)
	if err != nil {
		return TestResult{}, err
	}
	return tr, nil
}

// -----------------------------------------------------------------------------
// download fetches a URL with token auth.
// -----------------------------------------------------------------------------
func download(ctx context.Context, url, dest, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", resp.Status)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

// -----------------------------------------------------------------------------
// safeUnzip prevents ZipSlip by validating path prefix.
// -----------------------------------------------------------------------------
func safeUnzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	if err = os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	for _, f := range r.File {
		fp := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(fp, filepath.Clean(dest)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal path: %s", fp)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(fp, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(fp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// -----------------------------------------------------------------------------
// Ginkgo JSON parsing helpers
// -----------------------------------------------------------------------------

// fallback types when the above import is undesired; comment out if vendor size
// is a concern.
// type ginkgoReport struct {
//     EndTime      string `json:"EndTime"`
//     SpecReports  []struct{ State string `json:"State"` } `json:"SpecReports"`
// }

// -----------------------------------------------------------------------------
func parse(path, project, url string) (TestResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return TestResult{}, err
	}
	defer f.Close()

	// First attempt: decode as []types.Report (ginkgo v2 default JSON)
	var reports []types.Report
	if err := json.NewDecoder(f).Decode(&reports); err == nil && len(reports) > 0 {
		return summariseReports(reports, project, url)
	}

	// Rewind and attempt legacy single-object summary (CI can emit custom JSON)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return TestResult{}, err
	}
	var legacy struct {
		LastRun string `json:"lastRun"`
		Passed  int    `json:"passed"`
		Failed  int    `json:"failed"`
	}
	if err := json.NewDecoder(f).Decode(&legacy); err == nil && legacy.Passed+legacy.Failed > 0 {
		if legacy.LastRun == "" {
			legacy.LastRun = time.Now().Format(time.RFC3339)
		}
		return TestResult{project, legacy.LastRun, legacy.Passed, legacy.Failed, url}, nil
	}
	return TestResult{}, fmt.Errorf("parse: unknown JSON schema in %s", path)
}

// summariseReports aggregates Ginkgo spec states across N suites.
func summariseReports(reports []types.Report, project, url string) (TestResult, error) {
	var passed, failed int
	latest := time.Time{}

	for _, rep := range reports {
		if rep.EndTime.After(latest) {
			latest = rep.EndTime
		}
		for _, s := range rep.SpecReports {
			switch s.State {
			case types.SpecStatePassed:
				passed++
			case types.SpecStateFailed, types.SpecStateAborted, types.SpecStateInterrupted:
				failed++
			}
		}
	}
	if latest.IsZero() {
		latest = time.Now()
	}
	return TestResult{
		Project:   project,
		LastRun:   latest.Format(time.RFC3339),
		Passed:    passed,
		Failed:    failed,
		ActionURL: url,
	}, nil
}

// -----------------------------------------------------------------------------
// writeJSON marshals v into path, creating parent directories.
// -----------------------------------------------------------------------------
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func fetchLatestImageTag(ctx context.Context, client *github.Client, repo string) (ImageInfo, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return ImageInfo{}, fmt.Errorf("invalid repo: %s", repo)
	}
	owner, name := parts[0], parts[1]

	versions, _, err := client.Organizations.PackageGetAllVersions(ctx, owner, "container", name, nil)
	if err != nil {
		return ImageInfo{}, err
	}

	var latest *github.PackageVersion
	for _, v := range versions {
		if latest == nil || v.GetCreatedAt().Time.After(latest.GetCreatedAt().Time) {
			latest = v
		}
	}
	if latest == nil || len(latest.Metadata.Container.Tags) == 0 {
		return ImageInfo{}, fmt.Errorf("no tags found for %s", repo)
	}

	tag := latest.Metadata.Container.Tags[0]
	versionID := latest.GetID()
	htmlURL := fmt.Sprintf("https://github.com/%s/%s/pkgs/container/%s/%d?tag=%s",
		owner, name, name, versionID, tag)

	return ImageInfo{
		Repo:    repo,
		Tag:     tag,
		Pushed:  latest.GetCreatedAt().Format(time.RFC3339),
		HTMLURL: htmlURL,
	}, nil
}
