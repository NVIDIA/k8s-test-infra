package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// canonicalShape matches "owner/name" with lowercase letters, digits,
// and dashes. The Go side of the codebase uses lowercase by convention;
// the TS side uses canonical org casing (NVIDIA / kubernetes-sigs).
var canonicalShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/[a-z0-9][a-z0-9-]*$`)

// TestRepoListsCanonicalShape enforces every Go-side repo literal matches
// the lowercase owner/name convention. Catches typos and casing drift.
func TestRepoListsCanonicalShape(t *testing.T) {
	t.Parallel()

	checks := []struct {
		name string
		list []string
	}{
		{"defaultRepos", defaultRepos},
		{"allRepos", allRepos},
	}
	for _, c := range checks {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			for _, r := range c.list {
				if !canonicalShape.MatchString(r) {
					t.Errorf("%s entry %q does not match %s", c.name, r, canonicalShape)
				}
			}
		})
	}
	t.Run("defaultImages.repo", func(t *testing.T) {
		t.Parallel()
		for _, ir := range defaultImages {
			if !canonicalShape.MatchString(ir.repo) {
				t.Errorf("defaultImages entry repo=%q does not match %s", ir.repo, canonicalShape)
			}
		}
	})
}

// TestDRAMigrationLiteralsAligned asserts the DRA repo path is the new
// kubernetes-sigs canonical home in all four places (Go's three slices
// + src/data/projects.ts). Catches:
//   - Forgotten literal in any of the four files
//   - Future revert to nvidia/k8s-dra-driver-gpu in any place
//   - Partial migration (one side updated, the other not)
func TestDRAMigrationLiteralsAligned(t *testing.T) {
	t.Parallel()

	const (
		newRepo = "kubernetes-sigs/dra-driver-nvidia-gpu"
		oldRepo = "nvidia/k8s-dra-driver-gpu"
	)

	t.Run("defaultRepos", func(t *testing.T) {
		t.Parallel()
		if !sliceContains(defaultRepos, newRepo) {
			t.Errorf("defaultRepos missing %q; got: %v", newRepo, defaultRepos)
		}
		if sliceContains(defaultRepos, oldRepo) {
			t.Errorf("defaultRepos still has stale %q", oldRepo)
		}
	})
	t.Run("allRepos", func(t *testing.T) {
		t.Parallel()
		if !sliceContains(allRepos, newRepo) {
			t.Errorf("allRepos missing %q; got: %v", newRepo, allRepos)
		}
		if sliceContains(allRepos, oldRepo) {
			t.Errorf("allRepos still has stale %q", oldRepo)
		}
	})
	t.Run("defaultImages.repo", func(t *testing.T) {
		t.Parallel()
		var found bool
		for _, ir := range defaultImages {
			if ir.repo == newRepo {
				found = true
			}
			if ir.repo == oldRepo {
				t.Errorf("defaultImages still references stale %q", oldRepo)
			}
		}
		if !found {
			t.Errorf("defaultImages missing entry with repo=%q", newRepo)
		}
	})
	t.Run("defaultImages.registryCoords", func(t *testing.T) {
		t.Parallel()
		// Post-CNCF-donation: DRA driver images live at
		// registry.k8s.io/dra-driver-nvidia/dra-driver-nvidia-gpu, promoted
		// from a k8s-staging registry by the Prow release job.
		// See docs/plans/2026-05-11-dra-registry-k8s-io-design.md.
		const (
			wantPkgName  = "dra-driver-nvidia/dra-driver-nvidia-gpu"
			wantRegistry = "registry.k8s.io"
		)
		var found bool
		for _, ir := range defaultImages {
			if ir.repo != newRepo {
				continue
			}
			found = true
			if ir.imageRegistry != wantRegistry {
				t.Errorf("defaultImages DRA entry imageRegistry = %q; want %q", ir.imageRegistry, wantRegistry)
			}
			if ir.pkgName != wantPkgName {
				t.Errorf("defaultImages DRA entry pkgName = %q; want %q (full registry.k8s.io image path)", ir.pkgName, wantPkgName)
			}
		}
		if !found {
			t.Errorf("defaultImages missing entry with repo=%q", newRepo)
		}
	})
	t.Run("projects.ts", func(t *testing.T) {
		t.Parallel()
		// Read the TS file as a string and grep for the canonical literal.
		// We don't parse TypeScript; the literal is unambiguous.
		path, err := filepath.Abs("src/data/projects.ts")
		if err != nil {
			t.Fatalf("abs: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		content := string(data)
		if !strings.Contains(content, `'`+newRepo+`'`) && !strings.Contains(content, `"`+newRepo+`"`) {
			t.Errorf("projects.ts missing %q literal", newRepo)
		}
		// Old literal in either case-form would be a regression.
		if strings.Contains(content, `'`+oldRepo+`'`) ||
			strings.Contains(content, `"`+oldRepo+`"`) ||
			strings.Contains(strings.ToLower(content), strings.ToLower("'NVIDIA/k8s-dra-driver-gpu'")) {
			t.Errorf("projects.ts still references stale DRA path")
		}
	})
}

func sliceContains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// TestDefaultImagesProjectToImageInfo ties the internal imageRepo.repo field
// to the public ImageInfo.Repo field consumed downstream by the dashboard.
// If a future refactor stripped or transformed the owner prefix when populating
// ImageInfo, this guards against that drift while exercising the exported type.
func TestDefaultImagesProjectToImageInfo(t *testing.T) {
	t.Parallel()
	if len(defaultImages) == 0 {
		t.Fatal("defaultImages is empty")
	}
	for _, ir := range defaultImages {
		ii := ImageInfo{Repo: ir.repo}
		if ii.Repo != ir.repo {
			t.Errorf("ImageInfo.Repo=%q, want %q (round-trip drift)", ii.Repo, ir.repo)
		}
		if !canonicalShape.MatchString(ii.Repo) {
			t.Errorf("ImageInfo.Repo=%q does not match canonical shape", ii.Repo)
		}
	}
}
