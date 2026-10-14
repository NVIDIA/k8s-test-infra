// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package hack

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The stand-ins for gh, docker, helm and cosign append their argv to
// $VR_CALLS and answer from files under $VR_FIXTURES. A missing fixture file
// is a lookup that fails, the way the real tool fails on an unknown ref.
var verifyReleaseStubs = map[string]string{
	// gh run list ... --workflow <file> ...: the runs recorded for that file.
	"gh": `#!/bin/sh
printf 'gh %s\n' "$*" >> "$VR_CALLS"
while [ $# -gt 0 ]; do
  [ "$1" = --workflow ] && workflow=$2
  shift
done
exec cat "$VR_FIXTURES/gh/$workflow"
`,
	// docker buildx version (the preflight), and
	// docker buildx imagetools inspect --format '{{json .Manifest}}' <ref>.
	"docker": `#!/bin/sh
printf 'docker %s\n' "$*" >> "$VR_CALLS"
[ "$1 $2" = "buildx version" ] && exit 0
for arg; do ref=$arg; done
f="$VR_FIXTURES/docker/$(printf '%s' "$ref" | tr '/:@' '___')"
[ -f "$f" ] || { echo "ERROR: $ref: not found" >&2; exit 1; }
echo "WARNING: Error loading config file" >&2
exec cat "$f"
`,
	// helm show chart oci://.../<chart> --version <v>. Like real helm it reports
	// the pulled ref and its digest on stderr, here sha256:chart-<chart>.
	"helm": `#!/bin/sh
printf 'helm %s\n' "$*" >> "$VR_CALLS"
f="$VR_FIXTURES/helm/${3##*/}"
[ -f "$f" ] || { echo "Error: $3: not found" >&2; exit 1; }
echo "Pulled: ${3#oci://}:$5" >&2
echo "Digest: sha256:chart-${3##*/}" >&2
exec cat "$f"
`,
	// cosign verify[-attestation] ... <ref>: fails when "<subcommand> <ref>"
	// is listed in cosign-fail, and is noisy on both streams either way.
	"cosign": `#!/bin/sh
printf 'cosign %s\n' "$*" >> "$VR_CALLS"
for arg; do ref=$arg; done
if grep -qxF "$1 $ref" "$VR_FIXTURES/cosign-fail"; then
  echo "Error: no matching signatures: none of the expected identities matched" >&2
  echo "main.go:74: error during command execution" >&2
  exit 1
fi
echo "Verification for $ref --" >&2
echo '[{"critical":{"identity":{}}}]'
`,
}

// imageIndex renders what `imagetools inspect --format '{{json .Manifest}}'`
// prints for a multi-arch index: one manifest per linux architecture, plus the
// unknown/unknown attestation manifest buildx attaches, which is not a platform.
func imageIndex(t *testing.T, digest string, platformDigests map[string]string) string {
	t.Helper()

	type platform struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
	}
	type manifest struct {
		Digest   string   `json:"digest"`
		Platform platform `json:"platform"`
	}
	manifests := []manifest{}
	for _, arch := range []string{"amd64", "arm64"} {
		if d, ok := platformDigests[arch]; ok {
			manifests = append(manifests, manifest{Digest: d, Platform: platform{Architecture: arch, OS: "linux"}})
		}
	}
	manifests = append(manifests, manifest{Digest: "sha256:attestation", Platform: platform{Architecture: "unknown", OS: "unknown"}})

	out, err := json.MarshalIndent(map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.index.v1+json",
		"digest":        digest,
		"manifests":     manifests,
	}, "", "  ")
	require.NoError(t, err)
	return string(out)
}

type verifyReleaseFixture struct {
	runs       map[string]string // workflow file -> gh run list JSON
	images     map[string]string // image ref -> imagetools JSON; absent does not resolve
	charts     map[string]string // chart name -> helm show chart YAML
	cosignFail []string          // "<subcommand> <ref>" that cosign rejects
}

// TestVerifyRelease pins hack/verify-release.sh: every check runs, each
// prints one ok or FAIL line, and any FAIL makes the exit code 1. A check
// that stops gating, or that verifies the wrong artefact or identity, changes
// a line or the recorded tool calls.
func TestVerifyRelease(t *testing.T) {
	t.Parallel()

	script := filepath.Join(repoRoot(t), "hack", "verify-release.sh")
	usage := "usage: " + script + " X.Y.Z (the release version without the leading v, e.g. 0.5.0)\n"

	const (
		nvml     = "ghcr.io/nvidia/nvml-mock"
		cp       = "ghcr.io/nvidia/mokka-control-plane"
		nvmlDig  = "sha256:nvmlindex"
		cpDig    = "sha256:cpindex"
		cpAmd64  = "sha256:cpamd64"
		cpArm64  = "sha256:cparm64"
		identity = "https://github.com/NVIDIA/k8s-test-infra/.github/workflows/"
		issuer   = "--certificate-oidc-issuer https://token.actions.githubusercontent.com "
	)

	base := func(t *testing.T) verifyReleaseFixture {
		nvmlIndex := imageIndex(t, nvmlDig, map[string]string{"amd64": "sha256:nvmlamd64", "arm64": "sha256:nvmlarm64"})
		cpIndex := imageIndex(t, cpDig, map[string]string{"amd64": cpAmd64, "arm64": cpArm64})
		return verifyReleaseFixture{
			runs: map[string]string{
				// The newest run decides, whatever the array order. This fixture lists
				// it last and the "publish run failed" case lists it first, as gh does,
				// so taking either end of the array without sorting fails a case.
				"nvml-mock-publish.yaml":           `[{"conclusion":"success","createdAt":"2026-10-14T10:00:00Z","databaseId":101}]`,
				"mokka-control-plane-publish.yaml": `[{"conclusion":"failure","createdAt":"2026-10-14T09:00:00Z","databaseId":200},{"conclusion":"success","createdAt":"2026-10-14T10:00:00Z","databaseId":201}]`,
				"helm-publish.yaml":                `[{"conclusion":"success","createdAt":"2026-10-14T10:00:00Z","databaseId":301}]`,
			},
			images: map[string]string{
				nvml + ":0.5.0":  nvmlIndex,
				nvml + ":0.5":    nvmlIndex,
				nvml + ":latest": nvmlIndex,
				cp + ":0.5.0":    cpIndex,
				cp + ":0.5":      cpIndex,
				// main builds move it; a mismatch here must not fail the release.
				cp + ":latest": imageIndex(t, "sha256:cpmain", map[string]string{"amd64": "sha256:a", "arm64": "sha256:b"}),
			},
			charts: map[string]string{
				"nvml-mock":  "apiVersion: v2\nappVersion: 0.5.0\nkubeVersion: '>= 1.28.0-0'\nname: nvml-mock\nversion: 0.5.0\n",
				"mokka-crds": "apiVersion: v2\nappVersion: \"0.5.0\"\nname: mokka-crds\nversion: 0.5.0\n",
			},
		}
	}

	runList := func(w string) string {
		return "gh run list --repo NVIDIA/k8s-test-infra --workflow " + w + " --branch v0.5.0 --json conclusion,databaseId,createdAt"
	}
	inspect := func(ref string) string {
		return "docker buildx imagetools inspect --format {{json .Manifest}} " + ref
	}
	verify := func(w, ref string) string {
		return "cosign verify --certificate-identity " + identity + w + "@refs/tags/v0.5.0 " + issuer + ref
	}
	attest := func(w, ref string) string {
		return "cosign verify-attestation --type spdxjson --certificate-identity " + identity + w + "@refs/tags/v0.5.0 " + issuer + ref
	}
	allCalls := []string{
		"docker buildx version",
		runList("nvml-mock-publish.yaml"),
		runList("mokka-control-plane-publish.yaml"),
		runList("helm-publish.yaml"),
		inspect(nvml + ":0.5.0"),
		inspect(cp + ":0.5.0"),
		inspect(nvml + ":0.5"),
		inspect(cp + ":0.5"),
		inspect(nvml + ":latest"),
		"helm show chart oci://ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock --version 0.5.0",
		"helm show chart oci://ghcr.io/nvidia/k8s-test-infra/chart/mokka-crds --version 0.5.0",
		verify("nvml-mock-publish.yaml", nvml+":0.5.0"),
		verify("mokka-control-plane-publish.yaml", cp+":0.5.0"),
		verify("helm-publish.yaml", "ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock:0.5.0"),
		verify("helm-publish.yaml", "ghcr.io/nvidia/k8s-test-infra/chart/mokka-crds:0.5.0"),
		attest("nvml-mock-publish.yaml", nvml+":0.5.0"),
		attest("mokka-control-plane-publish.yaml", cp+"@"+cpAmd64),
		attest("mokka-control-plane-publish.yaml", cp+"@"+cpArm64),
	}

	allOK := []string{
		"ok: publish run nvml-mock-publish.yaml for v0.5.0 succeeded (run 101)",
		"ok: publish run mokka-control-plane-publish.yaml for v0.5.0 succeeded (run 201)",
		"ok: publish run helm-publish.yaml for v0.5.0 succeeded (run 301)",
		"ok: ghcr.io/nvidia/nvml-mock:0.5.0 lists linux/amd64 and linux/arm64 (sha256:nvmlindex)",
		"ok: ghcr.io/nvidia/mokka-control-plane:0.5.0 lists linux/amd64 and linux/arm64 (sha256:cpindex)",
		"ok: ghcr.io/nvidia/nvml-mock:0.5 has the digest of 0.5.0",
		"ok: ghcr.io/nvidia/mokka-control-plane:0.5 has the digest of 0.5.0",
		"ok: ghcr.io/nvidia/nvml-mock:latest has the digest of 0.5.0",
		"ok: chart nvml-mock 0.5.0 has version and appVersion 0.5.0 (sha256:chart-nvml-mock)",
		"ok: chart mokka-crds 0.5.0 has version and appVersion 0.5.0 (sha256:chart-mokka-crds)",
		"ok: cosign verify ghcr.io/nvidia/nvml-mock:0.5.0, signed by nvml-mock-publish.yaml",
		"ok: cosign verify ghcr.io/nvidia/mokka-control-plane:0.5.0, signed by mokka-control-plane-publish.yaml",
		"ok: cosign verify ghcr.io/nvidia/k8s-test-infra/chart/nvml-mock:0.5.0, signed by helm-publish.yaml",
		"ok: cosign verify ghcr.io/nvidia/k8s-test-infra/chart/mokka-crds:0.5.0, signed by helm-publish.yaml",
		"ok: cosign verify-attestation spdxjson ghcr.io/nvidia/nvml-mock:0.5.0",
		"ok: cosign verify-attestation spdxjson ghcr.io/nvidia/mokka-control-plane@sha256:cpamd64 (linux/amd64)",
		"ok: cosign verify-attestation spdxjson ghcr.io/nvidia/mokka-control-plane@sha256:cparm64 (linux/arm64)",
	}
	// report is the expected stdout: allOK with each line named in fail
	// replaced by its FAIL line, then the summary.
	report := func(fail map[string]string, summary string) string {
		lines := []string{}
		for _, line := range allOK {
			if f, ok := fail[line]; ok {
				line = f
			}
			lines = append(lines, line)
		}
		return strings.Join(append(lines, summary), "\n") + "\n"
	}

	for _, tc := range []struct {
		name     string
		args     []string
		mutate   func(t *testing.T, f *verifyReleaseFixture)
		exitCode int
		stdout   string
		stderr   string // exact when usage is set, else a substring
		usage    bool
		calls    []string
	}{
		{
			name:   "all pass",
			args:   []string{"0.5.0"},
			stdout: report(nil, "summary: 17 of 17 checks passed for v0.5.0"),
			calls:  allCalls,
		},
		{name: "v-prefixed version", args: []string{"v0.5.0"}, exitCode: 2, usage: true},
		{name: "prerelease version", args: []string{"0.5.0-rc1"}, exitCode: 2, usage: true},
		{name: "no version", exitCode: 2, usage: true},
		{name: "two versions", args: []string{"0.5.0", "0.5.1"}, exitCode: 2, usage: true},
		{
			name: "publish run failed",
			args: []string{"0.5.0"},
			mutate: func(_ *testing.T, f *verifyReleaseFixture) {
				f.runs["helm-publish.yaml"] = `[{"conclusion":"failure","createdAt":"2026-10-14T10:00:00Z","databaseId":301},{"conclusion":"success","createdAt":"2026-10-14T09:00:00Z","databaseId":300}]`
			},
			exitCode: 1,
			stdout: report(map[string]string{
				"ok: publish run helm-publish.yaml for v0.5.0 succeeded (run 301)": "FAIL: publish run helm-publish.yaml for v0.5.0: success vs failure (run 301)",
			}, "summary: 1 of 17 checks failed for v0.5.0"),
			calls: allCalls,
		},
		{
			// v0.4.1 has no run under mokka-control-plane-publish.yaml: the
			// workflow file was renamed after it.
			name: "no publish run",
			args: []string{"0.5.0"},
			mutate: func(_ *testing.T, f *verifyReleaseFixture) {
				f.runs["mokka-control-plane-publish.yaml"] = `[]`
			},
			exitCode: 1,
			stdout: report(map[string]string{
				"ok: publish run mokka-control-plane-publish.yaml for v0.5.0 succeeded (run 201)": "FAIL: publish run mokka-control-plane-publish.yaml for v0.5.0: success vs no run",
			}, "summary: 1 of 17 checks failed for v0.5.0"),
			calls: allCalls,
		},
		{
			// Without an arm64 manifest there is no arm64 digest whose SBOM
			// could be verified, so that attestation fails without calling cosign.
			name: "control-plane index missing arm64",
			args: []string{"0.5.0"},
			mutate: func(t *testing.T, f *verifyReleaseFixture) {
				amd64Only := imageIndex(t, cpDig, map[string]string{"amd64": cpAmd64})
				f.images[cp+":0.5.0"] = amd64Only
				f.images[cp+":0.5"] = amd64Only
			},
			exitCode: 1,
			stdout: report(map[string]string{
				"ok: ghcr.io/nvidia/mokka-control-plane:0.5.0 lists linux/amd64 and linux/arm64 (sha256:cpindex)":        "FAIL: ghcr.io/nvidia/mokka-control-plane:0.5.0 platforms: linux/amd64 linux/arm64 vs linux/amd64",
				"ok: cosign verify-attestation spdxjson ghcr.io/nvidia/mokka-control-plane@sha256:cparm64 (linux/arm64)": "FAIL: cosign verify-attestation spdxjson ghcr.io/nvidia/mokka-control-plane:0.5.0 (linux/arm64): a linux/arm64 digest vs none in the index",
			}, "summary: 2 of 17 checks failed for v0.5.0"),
			calls: withoutCall(allCalls, attest("mokka-control-plane-publish.yaml", cp+"@"+cpArm64)),
		},
		{
			name: "X.Y digest differs",
			args: []string{"0.5.0"},
			mutate: func(t *testing.T, f *verifyReleaseFixture) {
				f.images[cp+":0.5"] = imageIndex(t, "sha256:cpstale", map[string]string{"amd64": "sha256:a", "arm64": "sha256:b"})
			},
			exitCode: 1,
			stdout: report(map[string]string{
				"ok: ghcr.io/nvidia/mokka-control-plane:0.5 has the digest of 0.5.0": "FAIL: ghcr.io/nvidia/mokka-control-plane:0.5 digest: sha256:cpindex vs sha256:cpstale",
			}, "summary: 1 of 17 checks failed for v0.5.0"),
			calls: allCalls,
		},
		{
			name: "X.Y tag missing",
			args: []string{"0.5.0"},
			mutate: func(_ *testing.T, f *verifyReleaseFixture) {
				delete(f.images, nvml+":0.5")
			},
			exitCode: 1,
			stdout: report(map[string]string{
				"ok: ghcr.io/nvidia/nvml-mock:0.5 has the digest of 0.5.0": "FAIL: ghcr.io/nvidia/nvml-mock:0.5 digest: sha256:nvmlindex vs does not resolve",
			}, "summary: 1 of 17 checks failed for v0.5.0"),
			stderr: "ERROR: ghcr.io/nvidia/nvml-mock:0.5: not found",
			calls:  allCalls,
		},
		{
			name: "nvml-mock latest digest differs",
			args: []string{"0.5.0"},
			mutate: func(t *testing.T, f *verifyReleaseFixture) {
				f.images[nvml+":latest"] = imageIndex(t, "sha256:nvmlold", map[string]string{"amd64": "sha256:a", "arm64": "sha256:b"})
			},
			exitCode: 1,
			stdout: report(map[string]string{
				"ok: ghcr.io/nvidia/nvml-mock:latest has the digest of 0.5.0": "FAIL: ghcr.io/nvidia/nvml-mock:latest digest: sha256:nvmlindex vs sha256:nvmlold",
			}, "summary: 1 of 17 checks failed for v0.5.0"),
			calls: allCalls,
		},
		{
			name: "image does not resolve",
			args: []string{"0.5.0"},
			mutate: func(_ *testing.T, f *verifyReleaseFixture) {
				delete(f.images, nvml+":0.5.0")
				f.cosignFail = append(f.cosignFail, "verify "+nvml+":0.5.0", "verify-attestation "+nvml+":0.5.0")
			},
			exitCode: 1,
			stdout: report(map[string]string{
				"ok: ghcr.io/nvidia/nvml-mock:0.5.0 lists linux/amd64 and linux/arm64 (sha256:nvmlindex)": "FAIL: ghcr.io/nvidia/nvml-mock:0.5.0 platforms: linux/amd64 linux/arm64 vs does not resolve",
				"ok: ghcr.io/nvidia/nvml-mock:0.5 has the digest of 0.5.0":                                "FAIL: ghcr.io/nvidia/nvml-mock:0.5 digest: the digest of 0.5.0 vs 0.5.0 does not resolve",
				"ok: ghcr.io/nvidia/nvml-mock:latest has the digest of 0.5.0":                             "FAIL: ghcr.io/nvidia/nvml-mock:latest digest: the digest of 0.5.0 vs 0.5.0 does not resolve",
				"ok: cosign verify ghcr.io/nvidia/nvml-mock:0.5.0, signed by nvml-mock-publish.yaml":      "FAIL: cosign verify ghcr.io/nvidia/nvml-mock:0.5.0: signature from nvml-mock-publish.yaml@refs/tags/v0.5.0 vs verification failed",
				"ok: cosign verify-attestation spdxjson ghcr.io/nvidia/nvml-mock:0.5.0":                   "FAIL: cosign verify-attestation spdxjson ghcr.io/nvidia/nvml-mock:0.5.0: SBOM attestation from nvml-mock-publish.yaml@refs/tags/v0.5.0 vs verification failed",
			}, "summary: 5 of 17 checks failed for v0.5.0"),
			stderr: "ERROR: ghcr.io/nvidia/nvml-mock:0.5.0: not found",
			calls:  allCalls,
		},
		{
			// The real v0.4.1 defect: the nvml-mock chart shipped the driver
			// version as its appVersion.
			name: "chart appVersion differs",
			args: []string{"0.5.0"},
			mutate: func(_ *testing.T, f *verifyReleaseFixture) {
				f.charts["nvml-mock"] = "apiVersion: v2\nappVersion: 550.163.01\nname: nvml-mock\nversion: 0.5.0\n"
			},
			exitCode: 1,
			stdout: report(map[string]string{
				"ok: chart nvml-mock 0.5.0 has version and appVersion 0.5.0 (sha256:chart-nvml-mock)": "FAIL: chart nvml-mock 0.5.0 version/appVersion: 0.5.0/0.5.0 vs 0.5.0/550.163.01",
			}, "summary: 1 of 17 checks failed for v0.5.0"),
			calls: allCalls,
		},
		{
			name: "chart version differs",
			args: []string{"0.5.0"},
			mutate: func(_ *testing.T, f *verifyReleaseFixture) {
				f.charts["mokka-crds"] = "apiVersion: v2\nappVersion: \"0.5.0\"\nname: mokka-crds\nversion: 0.5.0-dev\n"
			},
			exitCode: 1,
			stdout: report(map[string]string{
				"ok: chart mokka-crds 0.5.0 has version and appVersion 0.5.0 (sha256:chart-mokka-crds)": "FAIL: chart mokka-crds 0.5.0 version/appVersion: 0.5.0/0.5.0 vs 0.5.0-dev/0.5.0",
			}, "summary: 1 of 17 checks failed for v0.5.0"),
			calls: allCalls,
		},
		{
			name: "chart not published",
			args: []string{"0.5.0"},
			mutate: func(_ *testing.T, f *verifyReleaseFixture) {
				delete(f.charts, "mokka-crds")
			},
			exitCode: 1,
			stdout: report(map[string]string{
				"ok: chart mokka-crds 0.5.0 has version and appVersion 0.5.0 (sha256:chart-mokka-crds)": "FAIL: chart mokka-crds 0.5.0 version/appVersion: 0.5.0/0.5.0 vs helm show chart failed",
			}, "summary: 1 of 17 checks failed for v0.5.0"),
			stderr: "Error: oci://ghcr.io/nvidia/k8s-test-infra/chart/mokka-crds: not found",
			calls:  allCalls,
		},
		{
			name: "cosign verify fails for one artefact",
			args: []string{"0.5.0"},
			mutate: func(_ *testing.T, f *verifyReleaseFixture) {
				f.cosignFail = append(f.cosignFail, "verify ghcr.io/nvidia/k8s-test-infra/chart/mokka-crds:0.5.0")
			},
			exitCode: 1,
			stdout: report(map[string]string{
				"ok: cosign verify ghcr.io/nvidia/k8s-test-infra/chart/mokka-crds:0.5.0, signed by helm-publish.yaml": "FAIL: cosign verify ghcr.io/nvidia/k8s-test-infra/chart/mokka-crds:0.5.0: signature from helm-publish.yaml@refs/tags/v0.5.0 vs verification failed",
			}, "summary: 1 of 17 checks failed for v0.5.0"),
			stderr: "Error: no matching signatures: none of the expected identities matched",
			calls:  allCalls,
		},
		{
			name: "attestation fails for one platform digest",
			args: []string{"0.5.0"},
			mutate: func(_ *testing.T, f *verifyReleaseFixture) {
				f.cosignFail = append(f.cosignFail, "verify-attestation "+cp+"@"+cpAmd64)
			},
			exitCode: 1,
			stdout: report(map[string]string{
				"ok: cosign verify-attestation spdxjson ghcr.io/nvidia/mokka-control-plane@sha256:cpamd64 (linux/amd64)": "FAIL: cosign verify-attestation spdxjson ghcr.io/nvidia/mokka-control-plane@sha256:cpamd64 (linux/amd64): SBOM attestation from mokka-control-plane-publish.yaml@refs/tags/v0.5.0 vs verification failed",
			}, "summary: 1 of 17 checks failed for v0.5.0"),
			stderr: "Error: no matching signatures: none of the expected identities matched",
			calls:  allCalls,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fx := base(t)
			if tc.mutate != nil {
				tc.mutate(t, &fx)
			}
			exitCode, stdout, stderr, calls := runVerifyRelease(t, script, tc.args, fx)

			require.Equal(t, tc.exitCode, exitCode, "exit code; stderr: %s", stderr)
			require.Equal(t, tc.stdout, stdout, "stdout")
			switch {
			case tc.usage:
				require.Equal(t, usage, stderr, "stderr")
			case tc.stderr != "":
				require.Contains(t, stderr, tc.stderr, "stderr")
			default:
				// Tool chatter stays out of the report unless a check fails.
				require.Empty(t, stderr, "stderr")
			}
			require.ElementsMatch(t, tc.calls, calls, "tool calls")
		})
	}
}

// runVerifyRelease runs the script with the stubbed tools first on PATH and an
// empty TMPDIR, and returns its exit code, stdout, stderr and the tool calls
// the stubs recorded.
func runVerifyRelease(t *testing.T, script string, args []string, fx verifyReleaseFixture) (int, string, string, []string) {
	t.Helper()

	tools, fixtures, scratch := t.TempDir(), t.TempDir(), t.TempDir()
	for name, body := range verifyReleaseStubs {
		require.NoError(t, os.WriteFile(filepath.Join(tools, name), []byte(body), 0o755))
	}
	write := func(dir, name, body string) {
		require.NoError(t, os.MkdirAll(filepath.Join(fixtures, dir), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(fixtures, dir, name), []byte(body), 0o644))
	}
	for w, body := range fx.runs {
		write("gh", w, body)
	}
	for ref, body := range fx.images {
		write("docker", strings.NewReplacer("/", "_", ":", "_", "@", "_").Replace(ref), body)
	}
	for c, body := range fx.charts {
		write("helm", c, body)
	}
	write(".", "cosign-fail", strings.Join(fx.cosignFail, "\n")+"\n")
	callLog := filepath.Join(tools, "calls")

	var stdout, stderr bytes.Buffer
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(),
		"PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"),
		"VR_CALLS="+callLog, "VR_FIXTURES="+fixtures, "TMPDIR="+scratch)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	exitCode := 0
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr, "running %s", script)
		exitCode = exitErr.ExitCode()
	}

	// The script keeps its scratch files under TMPDIR and must remove them.
	left, err := os.ReadDir(scratch)
	require.NoError(t, err)
	require.Empty(t, left, "scratch files left behind")

	logged, err := os.ReadFile(callLog)
	if errors.Is(err, fs.ErrNotExist) {
		return exitCode, stdout.String(), stderr.String(), []string{}
	}
	require.NoError(t, err)
	return exitCode, stdout.String(), stderr.String(), strings.Split(strings.TrimSuffix(string(logged), "\n"), "\n")
}

// withoutCall returns calls minus the one call a case expects not to happen.
func withoutCall(calls []string, drop string) []string {
	out := []string{}
	for _, c := range calls {
		if c != drop {
			out = append(out, c)
		}
	}
	return out
}

// TestVerifyReleaseMissingTool pins the preflight: a missing tool, or docker
// without the buildx plugin, stops the script with exit 2 before any check
// runs. Without it every check that needs the tool would print a FAIL whose
// real cause is only on stderr.
func TestVerifyReleaseMissingTool(t *testing.T) {
	t.Parallel()

	script := filepath.Join(repoRoot(t), "hack", "verify-release.sh")
	bash, err := exec.LookPath("bash")
	require.NoError(t, err)

	const (
		works    = "#!/bin/sh\nexit 0\n"
		noBuildx = "#!/bin/sh\n[ \"$1\" = buildx ] && exit 1\nexit 0\n"
	)
	for _, tc := range []struct {
		name   string
		tools  map[string]string
		stderr string
	}{
		{
			name:   "cosign missing",
			tools:  map[string]string{"gh": works, "docker": works, "helm": works, "jq": works},
			stderr: script + ": missing required tools: cosign\n",
		},
		{
			name:   "docker without buildx",
			tools:  map[string]string{"gh": works, "docker": noBuildx, "helm": works, "cosign": works, "jq": works},
			stderr: script + ": missing required tools: docker-buildx\n",
		},
		{
			name:   "cosign missing and docker without buildx",
			tools:  map[string]string{"gh": works, "docker": noBuildx, "helm": works, "jq": works},
			stderr: script + ": missing required tools: docker-buildx cosign\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// PATH holds only the stubs: nothing before the preflight needs
			// another command.
			tools := t.TempDir()
			for name, body := range tc.tools {
				require.NoError(t, os.WriteFile(filepath.Join(tools, name), []byte(body), 0o755))
			}
			var stdout, stderr bytes.Buffer
			cmd := exec.Command(bash, script, "0.5.0")
			cmd.Env = append(os.Environ(), "PATH="+tools)
			cmd.Stdout, cmd.Stderr = &stdout, &stderr

			var exitErr *exec.ExitError
			require.ErrorAs(t, cmd.Run(), &exitErr)
			require.Equal(t, 2, exitErr.ExitCode(), "exit code; stderr: %s", stderr.String())
			require.Empty(t, stdout.String(), "stdout")
			require.Equal(t, tc.stderr, stderr.String(), "stderr")
		})
	}
}
