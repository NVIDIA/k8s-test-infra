// Tests for the registry.k8s.io image-tag fetcher (and its dispatcher
// integration). The fetcher reads the latest GitHub release for the source
// repo and uses its tag_name/published_at as the image-tag/push-date pair,
// on the assumption that the kubernetes-sigs release process pushes the
// image when the release publishes.
//
// Spec: docs/plans/2026-05-11-dra-registry-k8s-io-design.md.
package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-github/v55/github"
)

// newReleasesStub returns an httptest server that serves the given JSON
// payload at /repos/{owner}/{name}/releases and 404s anything else. The
// returned *github.Client is rebased onto that server's URL so the fetcher
// hits the stub instead of api.github.com.
func newReleasesStub(t *testing.T, owner, name, body string) *github.Client {
	t.Helper()
	wantPath := "/repos/" + owner + "/" + name + "/releases"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, wantPath) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	client := github.NewClient(nil)
	client.BaseURL = mustParseURL(t, srv.URL+"/")
	return client
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	return u
}

func TestFetchLatestRegistryK8sTag_HappyPath(t *testing.T) {
	t.Parallel()

	// GitHub returns releases newest-first. The fetcher must take index 0.
	body := `[
		{"tag_name": "v25.12.0", "published_at": "2026-02-12T14:55:44Z"},
		{"tag_name": "v25.8.1",  "published_at": "2025-12-17T17:06:25Z"},
		{"tag_name": "v25.8.0",  "published_at": "2025-10-20T20:11:17Z"}
	]`
	client := newReleasesStub(t, "kubernetes-sigs", "dra-driver-nvidia-gpu", body)

	ir := imageRepo{
		repo:          "kubernetes-sigs/dra-driver-nvidia-gpu",
		pkgName:       "dra-driver-nvidia/dra-driver-nvidia-gpu",
		imageRegistry: "registry.k8s.io",
	}
	var got ImageInfo
	got, err := fetchLatestRegistryK8sTag(context.Background(), client, ir)
	if err != nil {
		t.Fatalf("fetchLatestRegistryK8sTag: %v", err)
	}
	if got.Tag != "v25.12.0" {
		t.Errorf("Tag = %q; want %q", got.Tag, "v25.12.0")
	}
	if got.Pushed != "2026-02-12T14:55:44Z" {
		t.Errorf("Pushed = %q; want %q", got.Pushed, "2026-02-12T14:55:44Z")
	}
	if got.ImageType != "release" {
		t.Errorf("ImageType = %q; want %q (SemVer tag must classify as release)", got.ImageType, "release")
	}
	wantHTMLURL := "https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/releases/tag/v25.12.0"
	if got.HTMLURL != wantHTMLURL {
		t.Errorf("HTMLURL = %q; want %q", got.HTMLURL, wantHTMLURL)
	}
	// CommitURL for a SemVer tag should be the release URL (delegated to buildCommitURL).
	if got.CommitURL != wantHTMLURL {
		t.Errorf("CommitURL = %q; want %q (release URL for SemVer)", got.CommitURL, wantHTMLURL)
	}
}

// TestFetchLatestImageTag_Dispatch asserts the dispatcher routes by
// imageRegistry. We stub /repos/.../releases (registry.k8s.io path) and
// /orgs/.../packages/... (GHCR path) on the same server and watch which one
// is hit per imageRegistry value.
func TestFetchLatestImageTag_Dispatch(t *testing.T) {
	t.Parallel()

	var (
		ghcrHit     bool
		releasesHit bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/orgs/") && strings.Contains(r.URL.Path, "/packages/"):
			ghcrHit = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[{"id":1,"created_at":"2026-05-01T00:00:00Z","metadata":{"container":{"tags":["v1.0.0"]}}}]`)
		case strings.HasPrefix(r.URL.Path, "/repos/") && strings.HasSuffix(r.URL.Path, "/releases"):
			releasesHit = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[{"tag_name":"v25.12.0","published_at":"2026-02-12T14:55:44Z"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	client := github.NewClient(nil)
	client.BaseURL = mustParseURL(t, srv.URL+"/")

	cases := []struct {
		name          string
		ir            imageRepo
		wantGHCR      bool
		wantReleases  bool
		wantErrSubstr string
	}{
		{
			name:         "empty registry routes to GHCR",
			ir:           imageRepo{repo: "nvidia/k8s-device-plugin", pkgName: "k8s-device-plugin", imageRegistry: ""},
			wantGHCR:     true,
			wantReleases: false,
		},
		{
			name:         "ghcr.io explicitly routes to GHCR",
			ir:           imageRepo{repo: "nvidia/k8s-device-plugin", pkgName: "k8s-device-plugin", imageRegistry: "ghcr.io"},
			wantGHCR:     true,
			wantReleases: false,
		},
		{
			name:         "registry.k8s.io routes to releases",
			ir:           imageRepo{repo: "kubernetes-sigs/dra-driver-nvidia-gpu", pkgName: "dra-driver-nvidia/dra-driver-nvidia-gpu", imageRegistry: "registry.k8s.io"},
			wantGHCR:     false,
			wantReleases: true,
		},
		{
			name:          "unknown registry returns error",
			ir:            imageRepo{repo: "nvidia/foo", pkgName: "foo", imageRegistry: "quay.io"},
			wantGHCR:      false,
			wantReleases:  false,
			wantErrSubstr: "unknown imageRegistry",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ghcrHit, releasesHit = false, false
			_, err := fetchLatestImageTag(context.Background(), client, tc.ir)

			if tc.wantErrSubstr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("err = %v; want non-nil containing %q", err, tc.wantErrSubstr)
				}
				return
			}
			if ghcrHit != tc.wantGHCR {
				t.Errorf("ghcrHit = %v; want %v", ghcrHit, tc.wantGHCR)
			}
			if releasesHit != tc.wantReleases {
				t.Errorf("releasesHit = %v; want %v", releasesHit, tc.wantReleases)
			}
		})
	}
}

func TestFetchLatestRegistryK8sTag_NoReleases(t *testing.T) {
	t.Parallel()

	client := newReleasesStub(t, "kubernetes-sigs", "dra-driver-nvidia-gpu", `[]`)

	ir := imageRepo{
		repo:          "kubernetes-sigs/dra-driver-nvidia-gpu",
		pkgName:       "dra-driver-nvidia/dra-driver-nvidia-gpu",
		imageRegistry: "registry.k8s.io",
	}
	_, err := fetchLatestRegistryK8sTag(context.Background(), client, ir)
	if err == nil {
		t.Fatalf("err = nil; want non-nil for empty releases response")
	}
	if !strings.Contains(err.Error(), "no releases found") {
		t.Fatalf("err = %v; want it to contain 'no releases found'", err)
	}
}
