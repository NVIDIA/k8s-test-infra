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
	"sort"
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
	"nvidia/holodeck",
}

// imageRepo pairs a GitHub repository path with its GitHub Packages container
// name. The repo field is the full owner/repo used for constructing HTML URLs,
// while pkgName is the container package name used by the Packages API.
type imageRepo struct {
	repo    string // GitHub repo path, e.g. "nvidia/nvidia-container-toolkit"
	pkgName string // GitHub Packages name, e.g. "container-toolkit"
}

var defaultImages = []imageRepo{
	{"nvidia/k8s-device-plugin", "k8s-device-plugin"},
	{"nvidia/k8s-dra-driver-gpu", "k8s-dra-driver-gpu"},
	{"nvidia/nvidia-container-toolkit", "container-toolkit"},
	{"nvidia/gpu-operator", "gpu-operator"},
	{"nvidia/gpu-driver-container", "driver"},
}

var allRepos = []string{
	"nvidia/gpu-operator",
	"nvidia/nvidia-container-toolkit",
	"nvidia/k8s-device-plugin",
	"nvidia/k8s-dra-driver-gpu",
	"nvidia/holodeck",
	"nvidia/go-nvml",
	"nvidia/mig-parted",
	"nvidia/gpu-driver-container",
	"nvidia/k8s-nim-operator",
}

type RepoInfo struct {
	Name        string   `json:"name"`
	FullName    string   `json:"fullName"`
	Description string   `json:"description"`
	Stars       int      `json:"stars"`
	Forks       int      `json:"forks"`
	Language    string   `json:"language"`
	License     string   `json:"license"`
	HTMLURL     string   `json:"htmlUrl"`
	Topics      []string `json:"topics"`
	README      string   `json:"readme"`
}

type WorkflowStatus struct {
	Repo       string `json:"repo"`
	Workflow   string `json:"workflow"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	RunURL     string `json:"runUrl"`
	UpdatedAt  string `json:"updatedAt"`
	CommitSHA  string `json:"commitSha"`
	CommitURL  string `json:"commitUrl"`
}

// TestResult corresponds to the schema consumed by Hugo.
// Adjust field names/tags carefully when evolving downstream expectations.

type TestResult struct {
	Project   string `json:"project"`
	Repo      string `json:"repo"`
	LastRun   string `json:"lastRun"`
	Passed    int    `json:"passed"`
	Failed    int    `json:"failed"`
	Skipped   int    `json:"skipped"`
	ActionURL string `json:"actionRunUrl"`
	Source    string `json:"source"`
}

type ImageInfo struct {
	Repo    string `json:"repo"`
	Tag     string `json:"tag"`
	Pushed  string `json:"pushedAt"`
	HTMLURL string `json:"htmlUrl"`
}

// HistorySnapshot captures a point-in-time summary of workflow statuses.
type HistorySnapshot struct {
	Timestamp string                    `json:"timestamp"`
	Workflows map[string]int            `json:"workflows"`
	PerRepo   map[string]map[string]int `json:"perRepo"`
}

type TrafficDay struct {
	Date    string `json:"date"`
	Count   int    `json:"count"`
	Uniques int    `json:"uniques"`
}

type RepoTraffic struct {
	Clones []TrafficDay `json:"clones"`
	Views  []TrafficDay `json:"views"`
}

type RepoStatsEntry struct {
	Date  string `json:"date"`
	Stars int    `json:"stars"`
	Forks int    `json:"forks"`
}

type HistoryFile struct {
	Snapshots []HistorySnapshot          `json:"snapshots"`
	Traffic   map[string]RepoTraffic     `json:"traffic"`
	RepoStats map[string][]RepoStatsEntry `json:"repoStats"`
}

type repoTrafficResult struct {
	repo    string
	traffic RepoTraffic
}

// Issues/PRs types
type AgeBuckets struct {
	Fresh   int `json:"fresh"`
	Recent  int `json:"recent"`
	Aging   int `json:"aging"`
	Stale   int `json:"stale"`
	Ancient int `json:"ancient"`
}

type VelocityWeek struct {
	Week   string `json:"week"`
	Opened int    `json:"opened"`
	Closed int    `json:"closed"`
	Merged int    `json:"merged,omitempty"`
}

type PRReviewMetrics struct {
	AwaitingReview       int     `json:"awaitingReview"`
	NoReviewer           int     `json:"noReviewer"`
	AvgDaysToFirstReview float64 `json:"avgDaysToFirstReview"`
	AvgDaysToMerge       float64 `json:"avgDaysToMerge"`
}

type IssueStats struct {
	Total      int            `json:"total"`
	Categories map[string]int `json:"categories"`
	AgeBuckets AgeBuckets     `json:"ageBuckets"`
	Velocity   []VelocityWeek `json:"velocity"`
}

type PRStats struct {
	Total      int             `json:"total"`
	Categories map[string]int  `json:"categories"`
	AgeBuckets AgeBuckets      `json:"ageBuckets"`
	Velocity   []VelocityWeek  `json:"velocity"`
	Review     PRReviewMetrics `json:"review"`
}

type RepoIssuesPRs struct {
	FetchedAt    string     `json:"fetchedAt"`
	Issues       IssueStats `json:"issues"`
	PullRequests PRStats    `json:"pullRequests"`
}

type IssuesPRsFile struct {
	Repos map[string]RepoIssuesPRs `json:"repos"`
}

type issuesPRsResult struct {
	repo string
	data RepoIssuesPRs
}

var labelCategories = map[string]map[string]string{
	"_default": {
		"bug":              "bug",
		"fix":              "bug",
		"feature":          "feature-request",
		"feature-request":  "feature-request",
		"feature request":  "feature-request",
		"enhancement":      "enhancement",
		"question":         "question",
		"documentation":    "docs",
		"good first issue": "good-first-issue",
	},
	"NVIDIA/gpu-operator": {
		"kind/bug":          "bug",
		"kind/feature":      "feature-request",
		"priority/critical": "critical",
	},
	"NVIDIA/k8s-device-plugin": {
		"kind/bug":     "bug",
		"kind/feature": "feature-request",
	},
	"NVIDIA/nvidia-container-toolkit": {
		"kind/bug":     "bug",
		"kind/feature": "feature-request",
	},
}

func categorizeLabels(repo string, labels []string) string {
	merged := make(map[string]string)
	for k, v := range labelCategories["_default"] {
		merged[k] = v
	}
	if repoMap, ok := labelCategories[repo]; ok {
		for k, v := range repoMap {
			merged[k] = v
		}
	}
	for _, label := range labels {
		lower := strings.ToLower(label)
		if cat, ok := merged[lower]; ok {
			return cat
		}
	}
	return "other"
}

func computeAgeBucket(created time.Time, now time.Time) string {
	age := now.Sub(created)
	switch {
	case age < 7*24*time.Hour:
		return "fresh"
	case age < 30*24*time.Hour:
		return "recent"
	case age < 90*24*time.Hour:
		return "aging"
	case age < 365*24*time.Hour:
		return "stale"
	default:
		return "ancient"
	}
}

func isoWeek(t time.Time) string {
	year, week := t.ISOWeek()
	jan4 := time.Date(year, 1, 4, 0, 0, 0, 0, time.UTC)
	weekday := jan4.Weekday()
	if weekday == 0 {
		weekday = 7
	}
	firstMonday := jan4.AddDate(0, 0, -int(weekday-1))
	monday := firstMonday.AddDate(0, 0, (week-1)*7)
	return monday.Format("2006-01-02")
}

func buildVelocitySlice(opened, closed, merged map[string]int, from, to time.Time) []VelocityWeek {
	weeks := make(map[string]bool)
	for w := from; w.Before(to); w = w.AddDate(0, 0, 7) {
		weeks[isoWeek(w)] = true
	}
	for w := range opened {
		weeks[w] = true
	}
	for w := range closed {
		weeks[w] = true
	}
	if merged != nil {
		for w := range merged {
			weeks[w] = true
		}
	}
	var sorted []string
	for w := range weeks {
		sorted = append(sorted, w)
	}
	sort.Strings(sorted)

	var result []VelocityWeek
	for _, w := range sorted {
		vw := VelocityWeek{
			Week:   w,
			Opened: opened[w],
			Closed: closed[w],
		}
		if merged != nil {
			vw.Merged = merged[w]
		}
		result = append(result, vw)
	}
	return result
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

	imgCh := make(chan ImageInfo, len(defaultImages))
	for _, ir := range defaultImages {
		wg.Add(1)
		go func(ir imageRepo) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			imgCtx, cancel := context.WithTimeout(ctx, *timeout)
			defer cancel()

			img, err := fetchLatestImageTag(imgCtx, client, ir)
			if err != nil {
				errCh <- fmt.Errorf("image %s/%s: %w", ir.repo, ir.pkgName, err)
				return
			}
			imgCh <- img
		}(ir)
	}

	wfCh := make(chan []WorkflowStatus, len(allRepos))
	for _, repo := range allRepos {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			wfCtx, cancel := context.WithTimeout(ctx, *timeout)
			defer cancel()

			wfs, err := fetchWorkflowStatus(wfCtx, client, r)
			if err != nil {
				errCh <- fmt.Errorf("workflow %s: %w", r, err)
				return
			}
			wfCh <- wfs
		}(repo)
	}

	repoCh := make(chan RepoInfo, len(allRepos))
	for _, repo := range allRepos {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			repoCtx, cancel := context.WithTimeout(ctx, *timeout)
			defer cancel()

			info, err := fetchRepoInfo(repoCtx, client, r)
			if err != nil {
				errCh <- fmt.Errorf("repo info %s: %w", r, err)
				return
			}
			repoCh <- info
		}(repo)
	}

	trafficCh := make(chan repoTrafficResult, len(allRepos))
	for _, repo := range allRepos {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			tCtx, cancel := context.WithTimeout(ctx, *timeout)
			defer cancel()

			t, err := fetchTraffic(tCtx, client, r)
			if err != nil {
				errCh <- fmt.Errorf("traffic %s: %w", r, err)
				return
			}
			trafficCh <- repoTrafficResult{repo: r, traffic: t}
		}(repo)
	}

	issuesPRsCh := make(chan issuesPRsResult, len(allRepos))
	for _, repo := range allRepos {
		wg.Add(1)
		go func(r string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			ipCtx, cancel := context.WithTimeout(ctx, 2*(*timeout))
			defer cancel()

			data, err := fetchIssuesPRs(ipCtx, client, r)
			if err != nil {
				errCh <- fmt.Errorf("issues/prs %s: %w", r, err)
				return
			}
			issuesPRsCh <- issuesPRsResult{repo: r, data: data}
		}(repo)
	}

	wg.Wait()
	close(resCh)
	close(errCh)
	close(imgCh)
	close(wfCh)
	close(repoCh)
	close(trafficCh)
	close(issuesPRsCh)

	for err := range errCh {
		log.Println("error:", err)
	}

	var results []TestResult
	for tr := range resCh {
		results = append(results, tr)
	}

	if err := writeJSON(filepath.Join(*outDir, "results.json"), map[string]any{"results": results}); err != nil {
		log.Fatal(err)
	}

	var images []ImageInfo
	for img := range imgCh {
		images = append(images, img)
	}

	if err := writeJSON(filepath.Join(*outDir, "images.json"), map[string]any{"images": images}); err != nil {
		log.Fatal(err)
	}

	var allWorkflows []WorkflowStatus
	for wfs := range wfCh {
		allWorkflows = append(allWorkflows, wfs...)
	}

	if err := writeJSON(filepath.Join(*outDir, "workflows.json"), map[string]any{"workflows": allWorkflows}); err != nil {
		log.Fatal(err)
	}

	var repoInfos []RepoInfo
	for info := range repoCh {
		repoInfos = append(repoInfos, info)
	}

	if err := writeJSON(filepath.Join(*outDir, "repos.json"), map[string]any{"repos": repoInfos}); err != nil {
		log.Fatal(err)
	}

	// --- Build issues_prs.json ---
	issuesPRsFile := IssuesPRsFile{Repos: make(map[string]RepoIssuesPRs)}
	for ipr := range issuesPRsCh {
		issuesPRsFile.Repos[ipr.repo] = ipr.data
	}

	if err := writeJSON(filepath.Join(*outDir, "issues_prs.json"), issuesPRsFile); err != nil {
		log.Fatal(err)
	}

	// --- Build history.json ---
	historyPath := filepath.Join(*outDir, "history.json")
	history := loadHistory(historyPath)

	// Build workflow snapshot
	snap := HistorySnapshot{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Workflows: make(map[string]int),
		PerRepo:   make(map[string]map[string]int),
	}
	for _, wf := range allWorkflows {
		snap.Workflows[wf.Status]++
		if snap.PerRepo[wf.Repo] == nil {
			snap.PerRepo[wf.Repo] = make(map[string]int)
		}
		snap.PerRepo[wf.Repo][wf.Status]++
	}
	history.Snapshots = append(history.Snapshots, snap)
	if len(history.Snapshots) > 1000 {
		history.Snapshots = history.Snapshots[len(history.Snapshots)-1000:]
	}

	// Drain trafficCh and merge
	for tr := range trafficCh {
		existing := history.Traffic[tr.repo]
		history.Traffic[tr.repo] = RepoTraffic{
			Clones: mergeTrafficDays(existing.Clones, tr.traffic.Clones),
			Views:  mergeTrafficDays(existing.Views, tr.traffic.Views),
		}
	}

	// Snapshot repo stats (stars, forks) for today
	today := time.Now().UTC().Format("2006-01-02")
	for _, ri := range repoInfos {
		key := ri.FullName
		entries := history.RepoStats[key]
		alreadyHasToday := false
		for _, e := range entries {
			if e.Date == today {
				alreadyHasToday = true
				break
			}
		}
		if !alreadyHasToday {
			history.RepoStats[key] = append(entries, RepoStatsEntry{
				Date:  today,
				Stars: ri.Stars,
				Forks: ri.Forks,
			})
		}
	}

	if err := writeJSON(historyPath, history); err != nil {
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
		return TestResult{
			Project:   project,
			Repo:      "NVIDIA/" + project,
			LastRun:   legacy.LastRun,
			Passed:    legacy.Passed,
			Failed:    legacy.Failed,
			Skipped:   0,
			ActionURL: url,
			Source:    "ginkgo",
		}, nil
	}
	return TestResult{}, fmt.Errorf("parse: unknown JSON schema in %s", path)
}

// summariseReports aggregates Ginkgo spec states across N suites.
func summariseReports(reports []types.Report, project, url string) (TestResult, error) {
	var passed, failed, skipped int
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
			case types.SpecStateSkipped, types.SpecStatePending:
				skipped++
			}
		}
	}
	if latest.IsZero() {
		latest = time.Now()
	}
	return TestResult{
		Project:   project,
		Repo:      "NVIDIA/" + project,
		LastRun:   latest.Format(time.RFC3339),
		Passed:    passed,
		Failed:    failed,
		Skipped:   skipped,
		ActionURL: url,
		Source:    "ginkgo",
	}, nil
}

// -----------------------------------------------------------------------------
// fetchWorkflowStatus retrieves the latest run status for each workflow in a repo.
// -----------------------------------------------------------------------------
func fetchWorkflowStatus(ctx context.Context, client *github.Client, repo string) ([]WorkflowStatus, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo: %s", repo)
	}
	owner, name := parts[0], parts[1]

	const maxWorkflows = 1000
	var allWfs []*github.Workflow
	opt := &github.ListOptions{PerPage: 100}
	for {
		workflows, resp, err := client.Actions.ListWorkflows(ctx, owner, name, opt)
		if err != nil {
			return nil, err
		}
		allWfs = append(allWfs, workflows.Workflows...)
		if len(allWfs) >= maxWorkflows {
			log.Printf("warning: reached workflow pagination limit (%d) for %s; additional workflows will be ignored", maxWorkflows, repo)
			break
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	var statuses []WorkflowStatus
	for _, wf := range allWfs {
		runs, _, err := client.Actions.ListWorkflowRunsByID(ctx, owner, name, wf.GetID(), &github.ListWorkflowRunsOptions{
			ListOptions: github.ListOptions{PerPage: 1},
		})
		if err != nil || len(runs.WorkflowRuns) == 0 {
			continue
		}
		run := runs.WorkflowRuns[0]
		status := "unknown"
		if run.GetStatus() == "completed" {
			switch run.GetConclusion() {
			case "success":
				status = "success"
			case "failure":
				status = "failure"
			default:
				status = "unknown"
			}
		} else if run.GetStatus() == "in_progress" || run.GetStatus() == "queued" {
			status = "in_progress"
		}

		statuses = append(statuses, WorkflowStatus{
			Repo:       repo,
			Workflow:   wf.GetName(),
			Status:     status,
			Conclusion: run.GetConclusion(),
			RunURL:     run.GetHTMLURL(),
			UpdatedAt:  run.GetUpdatedAt().Format(time.RFC3339),
			CommitSHA:  run.GetHeadSHA(),
			CommitURL:  fmt.Sprintf("https://github.com/%s/commit/%s", repo, run.GetHeadSHA()),
		})
	}
	return statuses, nil
}

// -----------------------------------------------------------------------------
// fetchRepoInfo retrieves repository metadata and README content.
// -----------------------------------------------------------------------------
func fetchRepoInfo(ctx context.Context, client *github.Client, repo string) (RepoInfo, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return RepoInfo{}, fmt.Errorf("invalid repo: %s", repo)
	}
	owner, name := parts[0], parts[1]

	r, _, err := client.Repositories.Get(ctx, owner, name)
	if err != nil {
		return RepoInfo{}, err
	}

	license := ""
	if r.License != nil {
		license = r.License.GetSPDXID()
	}

	// Fetch README as rendered HTML via GitHub API
	readmeContent := ""
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("https://api.github.com/repos/%s/%s/readme", owner, name), nil)
	if reqErr == nil {
		req.Header.Set("Accept", "application/vnd.github.html+json")
		req.Header.Set("Authorization", "token "+os.Getenv("GITHUB_TOKEN"))
		resp, doErr := http.DefaultClient.Do(req)
		if doErr == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				body, readErr := io.ReadAll(resp.Body)
				if readErr == nil {
					readmeContent = string(body)
				}
			}
		}
	}

	return RepoInfo{
		Name:        name,
		FullName:    r.GetFullName(),
		Description: r.GetDescription(),
		Stars:       r.GetStargazersCount(),
		Forks:       r.GetForksCount(),
		Language:    r.GetLanguage(),
		License:     license,
		HTMLURL:     r.GetHTMLURL(),
		Topics:      r.Topics,
		README:      readmeContent,
	}, nil
}

// -----------------------------------------------------------------------------
// fetchTraffic retrieves clone and view traffic data for a repository.
// -----------------------------------------------------------------------------
func fetchTraffic(ctx context.Context, client *github.Client, repo string) (RepoTraffic, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return RepoTraffic{}, fmt.Errorf("invalid repo: %s", repo)
	}
	owner, name := parts[0], parts[1]

	clones, _, err := client.Repositories.ListTrafficClones(ctx, owner, name, &github.TrafficBreakdownOptions{Per: "day"})
	if err != nil {
		log.Printf("warning: traffic clones for %s: %v (may need admin access)", repo, err)
		return RepoTraffic{}, nil
	}

	views, _, err := client.Repositories.ListTrafficViews(ctx, owner, name, &github.TrafficBreakdownOptions{Per: "day"})
	if err != nil {
		log.Printf("warning: traffic views for %s: %v", repo, err)
		return RepoTraffic{}, nil
	}

	var cloneDays []TrafficDay
	for _, c := range clones.Clones {
		cloneDays = append(cloneDays, TrafficDay{
			Date:    c.GetTimestamp().Format("2006-01-02"),
			Count:   c.GetCount(),
			Uniques: c.GetUniques(),
		})
	}

	var viewDays []TrafficDay
	for _, v := range views.Views {
		viewDays = append(viewDays, TrafficDay{
			Date:    v.GetTimestamp().Format("2006-01-02"),
			Count:   v.GetCount(),
			Uniques: v.GetUniques(),
		})
	}

	return RepoTraffic{Clones: cloneDays, Views: viewDays}, nil
}

func fetchIssuesPRs(ctx context.Context, client *github.Client, repo string) (RepoIssuesPRs, error) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return RepoIssuesPRs{}, fmt.Errorf("invalid repo: %s", repo)
	}
	owner, name := parts[0], parts[1]
	now := time.Now().UTC()
	twelveWeeksAgo := now.AddDate(0, 0, -84)

	// Fetch all open issues (GitHub issues endpoint includes PRs, filter them out)
	var openIssues []*github.Issue
	issueOpt := &github.IssueListByRepoOptions{
		State:       "open",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		issues, resp, err := client.Issues.ListByRepo(ctx, owner, name, issueOpt)
		if err != nil {
			return RepoIssuesPRs{}, fmt.Errorf("list open issues: %w", err)
		}
		for _, iss := range issues {
			if iss.PullRequestLinks == nil {
				openIssues = append(openIssues, iss)
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		issueOpt.Page = resp.NextPage
	}

	// Fetch all open PRs
	var openPRs []*github.PullRequest
	prOpt := &github.PullRequestListOptions{
		State:       "open",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		prs, resp, err := client.PullRequests.List(ctx, owner, name, prOpt)
		if err != nil {
			return RepoIssuesPRs{}, fmt.Errorf("list open PRs: %w", err)
		}
		openPRs = append(openPRs, prs...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		prOpt.Page = resp.NextPage
	}

	// Fetch recently closed issues+PRs (for velocity)
	var closedItems []*github.Issue
	closedOpt := &github.IssueListByRepoOptions{
		State:       "closed",
		Since:       twelveWeeksAgo,
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		issues, resp, err := client.Issues.ListByRepo(ctx, owner, name, closedOpt)
		if err != nil {
			log.Printf("warning: failed to fetch closed issues for %s: %v", repo, err)
			break
		}
		closedItems = append(closedItems, issues...)
		if resp == nil || resp.NextPage == 0 {
			break
		}
		closedOpt.Page = resp.NextPage
	}

	// Fetch recently merged PRs (for merge velocity and avg merge time)
	var mergedPRs []*github.PullRequest
	mergedOpt := &github.PullRequestListOptions{
		State:       "closed",
		Sort:        "updated",
		Direction:   "desc",
		ListOptions: github.ListOptions{PerPage: 100},
	}
	for {
		prs, resp, err := client.PullRequests.List(ctx, owner, name, mergedOpt)
		if err != nil {
			log.Printf("warning: failed to fetch merged PRs for %s: %v", repo, err)
			break
		}
		pastCutoff := false
		for _, pr := range prs {
			if pr.GetMergedAt().IsZero() {
				continue
			}
			if pr.GetMergedAt().Before(twelveWeeksAgo) {
				pastCutoff = true
				break
			}
			mergedPRs = append(mergedPRs, pr)
		}
		if pastCutoff || resp == nil || resp.NextPage == 0 {
			break
		}
		mergedOpt.Page = resp.NextPage
	}

	// --- Aggregate issue stats ---
	issueCats := make(map[string]int)
	var issueAgeBuckets AgeBuckets
	for _, iss := range openIssues {
		var labels []string
		for _, l := range iss.Labels {
			labels = append(labels, l.GetName())
		}
		cat := categorizeLabels(repo, labels)
		issueCats[cat]++

		bucket := computeAgeBucket(iss.GetCreatedAt().Time, now)
		switch bucket {
		case "fresh":
			issueAgeBuckets.Fresh++
		case "recent":
			issueAgeBuckets.Recent++
		case "aging":
			issueAgeBuckets.Aging++
		case "stale":
			issueAgeBuckets.Stale++
		case "ancient":
			issueAgeBuckets.Ancient++
		}
	}

	// Issue velocity: opened + closed per week
	issueOpenedByWeek := make(map[string]int)
	issueClosedByWeek := make(map[string]int)

	for _, iss := range openIssues {
		if iss.GetCreatedAt().Time.After(twelveWeeksAgo) {
			w := isoWeek(iss.GetCreatedAt().Time)
			issueOpenedByWeek[w]++
		}
	}
	for _, iss := range closedItems {
		if iss.PullRequestLinks != nil {
			continue
		}
		if iss.GetCreatedAt().Time.After(twelveWeeksAgo) {
			w := isoWeek(iss.GetCreatedAt().Time)
			issueOpenedByWeek[w]++
		}
		if iss.ClosedAt != nil && iss.GetClosedAt().Time.After(twelveWeeksAgo) {
			w := isoWeek(iss.GetClosedAt().Time)
			issueClosedByWeek[w]++
		}
	}

	// --- Aggregate PR stats ---
	prCats := make(map[string]int)
	var prAgeBuckets AgeBuckets
	awaitingReview := 0
	noReviewer := 0

	for _, pr := range openPRs {
		var labels []string
		for _, l := range pr.Labels {
			labels = append(labels, l.GetName())
		}
		cat := categorizeLabels(repo, labels)
		prCats[cat]++

		bucket := computeAgeBucket(pr.GetCreatedAt().Time, now)
		switch bucket {
		case "fresh":
			prAgeBuckets.Fresh++
		case "recent":
			prAgeBuckets.Recent++
		case "aging":
			prAgeBuckets.Aging++
		case "stale":
			prAgeBuckets.Stale++
		case "ancient":
			prAgeBuckets.Ancient++
		}

		if len(pr.RequestedReviewers) > 0 {
			awaitingReview++
		}
		if len(pr.RequestedReviewers) == 0 && len(pr.RequestedTeams) == 0 {
			noReviewer++
		}
	}

	// PR velocity
	prOpenedByWeek := make(map[string]int)
	prClosedByWeek := make(map[string]int)
	prMergedByWeek := make(map[string]int)

	for _, pr := range openPRs {
		if pr.GetCreatedAt().Time.After(twelveWeeksAgo) {
			w := isoWeek(pr.GetCreatedAt().Time)
			prOpenedByWeek[w]++
		}
	}
	for _, iss := range closedItems {
		if iss.PullRequestLinks == nil {
			continue
		}
		if iss.GetCreatedAt().Time.After(twelveWeeksAgo) {
			w := isoWeek(iss.GetCreatedAt().Time)
			prOpenedByWeek[w]++
		}
		if iss.ClosedAt != nil && iss.GetClosedAt().Time.After(twelveWeeksAgo) {
			w := isoWeek(iss.GetClosedAt().Time)
			prClosedByWeek[w]++
		}
	}
	for _, pr := range mergedPRs {
		w := isoWeek(pr.GetMergedAt().Time)
		prMergedByWeek[w]++
	}

	// Avg days to merge
	var totalMergeDays float64
	for _, pr := range mergedPRs {
		mergeDuration := pr.GetMergedAt().Time.Sub(pr.GetCreatedAt().Time)
		totalMergeDays += mergeDuration.Hours() / 24
	}
	avgDaysToMerge := 0.0
	if len(mergedPRs) > 0 {
		avgDaysToMerge = totalMergeDays / float64(len(mergedPRs))
	}

	// Avg days to first review for open PRs
	var totalFirstReviewDays float64
	reviewedCount := 0
	for _, pr := range openPRs {
		reviews, _, err := client.PullRequests.ListReviews(ctx, owner, name, pr.GetNumber(), &github.ListOptions{PerPage: 1})
		if err != nil || len(reviews) == 0 {
			continue
		}
		firstReview := reviews[0]
		duration := firstReview.GetSubmittedAt().Time.Sub(pr.GetCreatedAt().Time)
		totalFirstReviewDays += duration.Hours() / 24
		reviewedCount++
	}
	avgDaysToFirstReview := 0.0
	if reviewedCount > 0 {
		avgDaysToFirstReview = totalFirstReviewDays / float64(reviewedCount)
	}

	issueVelocity := buildVelocitySlice(issueOpenedByWeek, issueClosedByWeek, nil, twelveWeeksAgo, now)
	prVelocity := buildVelocitySlice(prOpenedByWeek, prClosedByWeek, prMergedByWeek, twelveWeeksAgo, now)

	return RepoIssuesPRs{
		FetchedAt: now.Format(time.RFC3339),
		Issues: IssueStats{
			Total:      len(openIssues),
			Categories: issueCats,
			AgeBuckets: issueAgeBuckets,
			Velocity:   issueVelocity,
		},
		PullRequests: PRStats{
			Total:      len(openPRs),
			Categories: prCats,
			AgeBuckets: prAgeBuckets,
			Velocity:   prVelocity,
			Review: PRReviewMetrics{
				AwaitingReview:       awaitingReview,
				NoReviewer:           noReviewer,
				AvgDaysToFirstReview: avgDaysToFirstReview,
				AvgDaysToMerge:       avgDaysToMerge,
			},
		},
	}, nil
}

// -----------------------------------------------------------------------------
// loadHistory reads an existing history.json file, returning an empty structure
// if the file does not exist or cannot be parsed.
// -----------------------------------------------------------------------------
func loadHistory(path string) HistoryFile {
	var h HistoryFile
	data, err := os.ReadFile(path)
	if err != nil {
		return HistoryFile{
			Traffic:   make(map[string]RepoTraffic),
			RepoStats: make(map[string][]RepoStatsEntry),
		}
	}
	if err := json.Unmarshal(data, &h); err != nil {
		log.Printf("warning: failed to parse existing history.json, starting fresh: %v", err)
		return HistoryFile{
			Traffic:   make(map[string]RepoTraffic),
			RepoStats: make(map[string][]RepoStatsEntry),
		}
	}
	if h.Traffic == nil {
		h.Traffic = make(map[string]RepoTraffic)
	}
	if h.RepoStats == nil {
		h.RepoStats = make(map[string][]RepoStatsEntry)
	}
	return h
}

// mergeTrafficDays combines existing and incoming traffic data, deduplicating by
// date and keeping the latest value for each day.
func mergeTrafficDays(existing, incoming []TrafficDay) []TrafficDay {
	byDate := make(map[string]TrafficDay)
	for _, d := range existing {
		byDate[d.Date] = d
	}
	for _, d := range incoming {
		byDate[d.Date] = d
	}
	merged := make([]TrafficDay, 0, len(byDate))
	for _, d := range byDate {
		merged = append(merged, d)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Date < merged[j].Date
	})
	return merged
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

func fetchLatestImageTag(ctx context.Context, client *github.Client, ir imageRepo) (ImageInfo, error) {
	parts := strings.Split(ir.repo, "/")
	if len(parts) != 2 {
		return ImageInfo{}, fmt.Errorf("invalid repo: %s", ir.repo)
	}
	owner := parts[0]

	versions, _, err := client.Organizations.PackageGetAllVersions(ctx, owner, "container", ir.pkgName, nil)
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
		return ImageInfo{}, fmt.Errorf("no tags found for %s", ir.repo)
	}

	tag := latest.Metadata.Container.Tags[0]
	versionID := latest.GetID()
	// URL format: https://github.com/{owner}/{repo-name}/pkgs/container/{package-name}/{version-id}?tag={tag}
	htmlURL := fmt.Sprintf("https://github.com/%s/pkgs/container/%s/%d?tag=%s",
		ir.repo, ir.pkgName, versionID, tag)

	return ImageInfo{
		Repo:    ir.repo,
		Tag:     tag,
		Pushed:  latest.GetCreatedAt().Format(time.RFC3339),
		HTMLURL: htmlURL,
	}, nil
}
