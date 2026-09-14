// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package hack

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

const (
	mokkaRepository          = "NVIDIA/k8s-test-infra"
	mokkaRepositoryID        = "733665780"
	mokkaWorkflowSHA         = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	mokkaSourceSHA           = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	mokkaActionID            = "123e4567-e89b-42d3-a456-426614174000"
	mokkaControlHelperOID    = "6cf473c1026e8cf83573599c7a0d86593925efda"
	mokkaControlHelperSHA256 = "3b03691a91a960b9222a58295c1b78b1245a19bf654cb6b7aab095dde2510065"
	mokkaControlHelperSize   = 8078
	mokkaControlFetchScript  = `set -Eeuo pipefail
umask 077
[[ "${GITHUB_WORKFLOW_SHA:?}" =~ ^[0-9a-f]{40}$ ]]
runner_temp="$(cd -- "${RUNNER_TEMP:?}" && pwd -P)"
workspace_dir="$(cd -- "${GITHUB_WORKSPACE:?}" && pwd -P)"
control_stage="$(mktemp -d "$runner_temp/mokka-control.XXXXXXXX")"
control_home="$(mktemp -d "$runner_temp/mokka-control-home.XXXXXXXX")"
control_xdg="$(mktemp -d "$runner_temp/mokka-control-xdg.XXXXXXXX")"
helper_stage="$(mktemp -d "$runner_temp/mokka-helper.XXXXXXXX")"
template_dir="$(mktemp -d "$runner_temp/mokka-template.XXXXXXXX")"
test -z "$(/usr/bin/find "$template_dir" -mindepth 1 -print -quit)"
control_dir="${RUNNER_TEMP:?}/mokka-control"
helper_dir="${RUNNER_TEMP:?}/mokka-helper"
test ! -e "$control_dir"
test ! -e "$helper_dir"
test "$(cd -- "$(dirname -- "$control_dir")" && pwd -P)" = "$runner_temp"
test "$(cd -- "$(dirname -- "$helper_dir")" && pwd -P)" = "$runner_temp"
for temporary_path in "$control_stage" "$control_home" "$control_xdg" "$helper_stage" "$template_dir"; do
  temporary_path="$(cd -- "$temporary_path" && pwd -P)"
  case "$temporary_path" in "$runner_temp"/*) ;; *) exit 1 ;; esac
  case "$temporary_path" in "$workspace_dir"/*) exit 1 ;; esac
done
git_bin="$(command -v git)"
test "$git_bin" = "/usr/bin/git"
test -x /usr/bin/git
git_exec_path="$(env -i PATH="/usr/bin:/bin" /usr/bin/git --exec-path)"
case "$git_exec_path" in /*) ;; *) exit 1 ;; esac
git_exec_path="$(cd -- "$git_exec_path" && pwd -P)"
test -d "$git_exec_path"
control_git() {
  env -i PATH="/usr/bin:/bin" HOME="$control_home" XDG_CONFIG_HOME="$control_xdg" GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_SYSTEM=/dev/null GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_COUNT=0 GIT_TERMINAL_PROMPT=0 GIT_ASKPASS=false SSH_ASKPASS=/bin/false GCM_INTERACTIVE=Never "$git_bin" --exec-path="$git_exec_path" -c protocol.allow=never -c protocol.https.allow=always -c protocol.version=2 -c http.followRedirects=false -c http.maxRedirects=0 -c credential.helper= -c http.https://github.com/.extraHeader= -c init.templateDir="$template_dir" -c fetch.recurseSubmodules=false "$@"
}
control_git -C "$control_stage" init --bare --quiet
test "$(control_git -C "$control_stage" rev-parse --is-bare-repository)" = "true"
test "$(control_git -C "$control_stage" config --local --name-only --list | /usr/bin/sort)" = $'core.bare\ncore.filemode\ncore.repositoryformatversion'
test "$(control_git -C "$control_stage" config --local --get core.bare)" = "true"
test "$(control_git -C "$control_stage" config --local --get core.filemode)" = "true"
test "$(control_git -C "$control_stage" config --local --get core.repositoryformatversion)" = "0"
test -z "$(control_git -C "$control_stage" remote)"
control_git -C "$control_stage" fetch --depth=1 --no-tags --no-recurse-submodules https://github.com/NVIDIA/k8s-test-infra.git "$GITHUB_WORKFLOW_SHA"
test "$(control_git -C "$control_stage" cat-file -t FETCH_HEAD)" = "commit"
control_git -C "$control_stage" update-ref --no-deref HEAD "$GITHUB_WORKFLOW_SHA"
test "$(control_git -C "$control_stage" rev-parse HEAD)" = "$GITHUB_WORKFLOW_SHA"
test "$(control_git -C "$control_stage" rev-parse FETCH_HEAD)" = "$GITHUB_WORKFLOW_SHA"
workflow_path=".github/workflows/mokka-cherry-pick.yml"
helper_path=".github/scripts/mokka-cherry-pick.sh"
mapfile -d '' -t workflow_entries < <(control_git -C "$control_stage" ls-tree -z "$GITHUB_WORKFLOW_SHA" -- "$workflow_path")
test "${#workflow_entries[@]}" -eq 1
[[ "${workflow_entries[0]}" =~ ^100644\ blob\ [0-9a-f]{40}$'\t'.github/workflows/mokka-cherry-pick.yml$ ]]
mapfile -d '' -t helper_entries < <(control_git -C "$control_stage" ls-tree -z "$GITHUB_WORKFLOW_SHA" -- "$helper_path")
test "${#helper_entries[@]}" -eq 1
test "${helper_entries[0]}" = "100755 blob 6cf473c1026e8cf83573599c7a0d86593925efda	.github/scripts/mokka-cherry-pick.sh"
test "$(control_git -C "$control_stage" cat-file -s 6cf473c1026e8cf83573599c7a0d86593925efda)" = "8078"
test "$(control_git -C "$control_stage" cat-file blob 6cf473c1026e8cf83573599c7a0d86593925efda | /usr/bin/sha256sum | /usr/bin/awk '{print $1}')" = "3b03691a91a960b9222a58295c1b78b1245a19bf654cb6b7aab095dde2510065"
control_git -C "$control_stage" cat-file blob 6cf473c1026e8cf83573599c7a0d86593925efda > "$helper_stage/mokka-cherry-pick.sh"
/usr/bin/chmod 0500 "$helper_stage/mokka-cherry-pick.sh"
test -f "$helper_stage/mokka-cherry-pick.sh"
test ! -L "$helper_stage/mokka-cherry-pick.sh"
test "$(/usr/bin/stat -c '%h:%a:%s:%F' "$helper_stage/mokka-cherry-pick.sh")" = "1:500:8078:regular file"
test ! -w "$helper_stage/mokka-cherry-pick.sh"
test -x "$helper_stage/mokka-cherry-pick.sh"
/usr/bin/mv -- "$control_stage" "$control_dir"
/usr/bin/mv -- "$helper_stage" "$helper_dir"
`
	mokkaDriverRun = `set -Eeuo pipefail
runner_temp="$(cd -- "${RUNNER_TEMP:?}" && pwd -P)"
helper_dir="$(cd -- "${RUNNER_TEMP:?}/mokka-helper" && pwd -P)"
test "$helper_dir" = "$runner_temp/mokka-helper"
helper_path="$helper_dir/mokka-cherry-pick.sh"
test -f "$helper_path"
test ! -L "$helper_path"
test "$(/usr/bin/stat -c '%h:%a:%s:%F' "$helper_path")" = "1:500:8078:regular file"
test ! -w "$helper_path"
test -x "$helper_path"
test "$(/usr/bin/git hash-object --no-filters "$helper_path")" = "6cf473c1026e8cf83573599c7a0d86593925efda"
test "$(/usr/bin/sha256sum "$helper_path" | /usr/bin/awk '{print $1}')" = "3b03691a91a960b9222a58295c1b78b1245a19bf654cb6b7aab095dde2510065"
exec /usr/bin/bash "$helper_path"
`
)

func mokkaPaths(t *testing.T) (string, string) {
	t.Helper()
	if root := os.Getenv("MOKKA_TEST_ARTIFACT_ROOT"); root != "" {
		workflow := filepath.Join(root, "mokka-cherry-pick.yml")
		script := filepath.Join(root, "mokka-cherry-pick.sh")
		require.FileExists(t, workflow, "the manual workflow is the reviewed write boundary")
		require.FileExists(t, script, "the workflow must ship its driver")
		return workflow, script
	}
	root := repoRoot(t)
	workflow := filepath.Join(root, ".github", "workflows", "mokka-cherry-pick.yml")
	script := filepath.Join(root, ".github", "scripts", "mokka-cherry-pick.sh")
	require.FileExists(t, workflow, "the manual workflow is the reviewed write boundary")
	require.FileExists(t, script, "the workflow must ship its driver")
	return workflow, script
}

func TestMokkaCherryPickDriverIsExecutable(t *testing.T) {
	_, script := mokkaPaths(t)
	info, err := os.Lstat(script)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm(), "the reviewed helper mode is part of its seal")
	require.Equal(t, os.FileMode(0), info.Mode()&os.ModeSymlink, "the reviewed helper must not be a symlink")
	require.Equal(t, int64(mokkaControlHelperSize), info.Size(), "the reviewed helper size is part of its seal")
	contents, err := os.ReadFile(script)
	require.NoError(t, err)
	require.Equal(t, mokkaControlHelperSHA256, fmt.Sprintf("%x", sha256.Sum256(contents)))
	command := exec.Command("git", "hash-object", "--no-filters", script)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Equal(t, mokkaControlHelperOID, strings.TrimSpace(string(output)))
}

func TestMokkaCherryPickWorkflowContract(t *testing.T) {
	workflow, _ := mokkaPaths(t)
	contents, err := os.ReadFile(workflow)
	require.NoError(t, err)
	require.NoError(t, validateMokkaWorkflowCriticalSteps(contents))
	text := string(contents)
	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal(contents, &parsed))
	require.NotContains(t, parsed, "env", "the workflow must not pass a token or other environment value to every step")
	triggers, ok := parsed["on"].(map[string]any)
	require.True(t, ok, "workflow triggers must parse as a mapping")
	require.Len(t, triggers, 1, "manual dispatch must be the only trigger")
	dispatch, ok := triggers["workflow_dispatch"].(map[string]any)
	require.True(t, ok)
	inputs, ok := dispatch["inputs"].(map[string]any)
	require.True(t, ok)
	require.Len(t, inputs, 4, "workflow must accept exactly four inputs")
	require.ElementsMatch(t, []string{"pull_request_number", "source_sha", "target_branch", "action_id"}, mapKeys(inputs))
	for name, input := range inputs {
		inputMap, ok := input.(map[string]any)
		require.True(t, ok, "input %s must be a mapping", name)
		require.Equal(t, true, inputMap["required"], "input %s must be required", name)
		require.Equal(t, "string", inputMap["type"], "input %s must be a string", name)
	}
	topPermissions, ok := parsed["permissions"].(map[string]any)
	require.True(t, ok)
	require.Empty(t, topPermissions, "top-level permissions must be empty")
	concurrency, ok := parsed["concurrency"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mokka-cherry-pick-${{ inputs.pull_request_number }}-${{ inputs.target_branch }}", concurrency["group"])
	require.Equal(t, false, concurrency["cancel-in-progress"])
	jobs, ok := parsed["jobs"].(map[string]any)
	require.True(t, ok)
	require.Len(t, jobs, 1, "workflow must have one write-capable job")
	job, ok := jobs["cherry-pick"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ubuntu-latest", job["runs-on"])
	require.EqualValues(t, 10, job["timeout-minutes"])
	jobPermissions, ok := job["permissions"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, map[string]any{"contents": "write", "pull-requests": "write"}, jobPermissions)
	steps, ok := job["steps"].([]any)
	require.True(t, ok)
	require.Len(t, steps, 4, "workflow must have exactly the reviewed four steps")
	firstStep, ok := steps[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Validate dispatch envelope", firstStep["name"])
	secondStep, ok := steps[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Fetch immutable control files without a credential", secondStep["name"])
	require.NotContains(t, secondStep, "uses", "control-file retrieval must not invoke an action with the job token")
	controlEnv, ok := secondStep["env"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, map[string]any{"GITHUB_WORKFLOW_SHA": "${{ github.workflow_sha }}"}, controlEnv)
	controlRun, ok := secondStep["run"].(string)
	require.True(t, ok)
	require.Equal(t, mokkaControlFetchScript, controlRun, "the unauthenticated control-file fetch script is a closed reviewed boundary")
	thirdStep, ok := steps[2].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Check out validated target", thirdStep["name"])
	require.Equal(t, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", thirdStep["uses"])
	targetWith, ok := thirdStep["with"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "${{ inputs.target_branch }}", targetWith["ref"])
	require.Equal(t, "target", targetWith["path"])
	require.Equal(t, true, targetWith["persist-credentials"])
	require.EqualValues(t, 1, targetWith["fetch-depth"])
	require.Equal(t, false, targetWith["submodules"])
	require.Equal(t, false, targetWith["lfs"])
	driverStep, ok := steps[3].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Cherry-pick merged source pull request", driverStep["name"])
	require.Equal(t, "target", driverStep["working-directory"])
	require.Equal(t, mokkaDriverRun, driverStep["run"])
	checkoutCount := 0
	for index, step := range steps {
		stepMap, ok := step.(map[string]any)
		require.True(t, ok)
		if _, isCheckout := stepMap["uses"]; isCheckout {
			checkoutCount++
			require.Equal(t, 2, index, "the target checkout must be the sole action invocation")
		}
	}
	require.Equal(t, 1, checkoutCount, "only the target checkout may invoke actions/checkout")

	require.Contains(t, text, "workflow_dispatch:")
	require.NotContains(t, text, "push:")
	require.NotContains(t, text, "pull_request:")
	require.Contains(t, text, "run-name: \"Mokka cherry-pick: ${{ inputs.action_id }}\"")
	require.Contains(t, text, "group: mokka-cherry-pick-${{ inputs.pull_request_number }}-${{ inputs.target_branch }}")
	require.Contains(t, text, "cancel-in-progress: false")
	require.Contains(t, text, "permissions: {}")
	for _, input := range []string{"pull_request_number", "source_sha", "target_branch", "action_id"} {
		require.Contains(t, text, "    "+input+":")
		require.Contains(t, text, "      required: true")
		require.Contains(t, text, "      type: string")
	}
	require.Contains(t, text, "contents: write")
	require.Contains(t, text, "pull-requests: write")
	require.Contains(t, text, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1")
	require.Contains(t, text, "ref: ${{ inputs.target_branch }}")
	validationStep := strings.Index(text, "name: Validate dispatch envelope")
	controlFetchStep := strings.Index(text, "name: Fetch immutable control files without a credential")
	require.GreaterOrEqual(t, validationStep, 0, "workflow must validate before checkout can run git")
	require.Greater(t, controlFetchStep, validationStep, "control-file fetch must follow dispatch-envelope validation")
	for _, format := range []string{"2147483647", "^[0-9a-f]{40}$", "[0-9a-f]{8}-[0-9a-f]{4}-4"} {
		require.Contains(t, text, format, "pre-checkout validation must enforce %q", format)
	}
	require.Contains(t, text, "GH_TOKEN: ${{ github.token }}")
	require.Contains(t, text, "GITHUB_WORKFLOW_SHA: ${{ github.workflow_sha }}")
	require.NotContains(t, text, ".mokka-control")
	require.NotContains(t, text, ".mokka-target")
	require.NotContains(t, text, "contents: read")
	require.NotContains(t, text, "pull-requests: read")

	requirePinnedActions(t, text)
}

func TestMokkaCherryPickWorkflowRejectsFailOpenStepMutations(t *testing.T) {
	workflow, _ := mokkaPaths(t)
	contents, err := os.ReadFile(workflow)
	require.NoError(t, err)
	for _, stepName := range []string{"Validate dispatch envelope", "Cherry-pick merged source pull request"} {
		t.Run(stepName, func(t *testing.T) {
			marker := "      - name: " + stepName + "\n"
			mutation := marker + "        continue-on-error: true\n"
			require.Equal(t, 1, strings.Count(string(contents), marker), "test mutation target must be unique")
			mutated := strings.Replace(string(contents), marker, mutation, 1)
			require.NotEqual(t, string(contents), mutated, "test mutation must change the workflow")
			require.ErrorContains(t, validateMokkaWorkflowCriticalSteps([]byte(mutated)), "continue-on-error")
		})
	}
}

func TestMokkaCherryPickWorkflowRejectsUnreviewedJobAndDriverKeys(t *testing.T) {
	workflow, _ := mokkaPaths(t)
	contents, err := os.ReadFile(workflow)
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		marker string
		value  string
		want   string
	}{
		{name: "job condition", marker: "  cherry-pick:\n", value: "    if: ${{ always() }}\n", want: "cherry-pick job keys"},
		{name: "job continue on error", marker: "  cherry-pick:\n", value: "    continue-on-error: true\n", want: "cherry-pick job keys"},
		{name: "target checkout condition", marker: "      - name: Check out validated target\n", value: "        if: ${{ always() }}\n", want: "target checkout step keys"},
		{name: "target checkout shadow", marker: "          path: target\n", value: "          path: mokka-control\n", want: "target checkout configuration"},
		{name: "driver condition", marker: "      - name: Cherry-pick merged source pull request\n", value: "        if: ${{ always() }}\n", want: "driver step keys"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutation := tc.marker + tc.value
			require.Equal(t, 1, strings.Count(string(contents), tc.marker), "test mutation target must be unique")
			mutated := strings.Replace(string(contents), tc.marker, mutation, 1)
			require.NotEqual(t, string(contents), mutated, "test mutation must change the workflow")
			require.ErrorContains(t, validateMokkaWorkflowCriticalSteps([]byte(mutated)), tc.want)
		})
	}
}

func TestMokkaCherryPickWorkflowRejectsControlFetchMutations(t *testing.T) {
	workflow, _ := mokkaPaths(t)
	contents, err := os.ReadFile(workflow)
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		marker string
		value  string
		want   string
		insert bool
	}{
		{name: "extra step key", marker: "      - name: Fetch immutable control files without a credential\n", value: "        uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1\n", want: "control fetch step keys", insert: true},
		{name: "continue on error", marker: "      - name: Fetch immutable control files without a credential\n", value: "        continue-on-error: true\n", want: "continue-on-error", insert: true},
		{name: "token environment", marker: "      - name: Fetch immutable control files without a credential\n        env:\n          GITHUB_WORKFLOW_SHA: ${{ github.workflow_sha }}\n", value: "      - name: Fetch immutable control files without a credential\n        env:\n          GH_TOKEN: ${{ github.token }}\n          GITHUB_WORKFLOW_SHA: ${{ github.workflow_sha }}\n", want: "control fetch environment"},
		{name: "config injection", marker: "GIT_CONFIG_COUNT=0", value: "GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=credential.helper GIT_CONFIG_VALUE_0=unsafe", want: "control fetch script"},
		{name: "environment injection", marker: "git_exec_path=\"$(env -i PATH=\"/usr/bin:/bin\" /usr/bin/git --exec-path)\"", value: "git_exec_path=\"$(env PATH=\"/usr/bin:/bin\" /usr/bin/git --exec-path)\"", want: "control fetch script"},
		{name: "unfixed git executable", marker: "test \"$git_bin\" = \"/usr/bin/git\"", value: "true", want: "control fetch script"},
		{name: "ssh prompt enabled", marker: "SSH_ASKPASS=/bin/false", value: "SSH_ASKPASS=/bin/true", want: "control fetch script"},
		{name: "gcm interaction enabled", marker: "GCM_INTERACTIVE=Never", value: "GCM_INTERACTIVE=Always", want: "control fetch script"},
		{name: "protocol downgrade", marker: "protocol.version=2", value: "protocol.version=1", want: "control fetch script"},
		{name: "redirect enabled", marker: "http.followRedirects=false", value: "http.followRedirects=true", want: "control fetch script"},
		{name: "redirect count enabled", marker: "http.maxRedirects=0", value: "http.maxRedirects=1", want: "control fetch script"},
		{name: "submodule recursion enabled", marker: "fetch.recurseSubmodules=false", value: "fetch.recurseSubmodules=true", want: "control fetch script"},
		{name: "submodule fetch option removed", marker: "--no-recurse-submodules", value: "", want: "control fetch script"},
		{name: "unsafe template directory", marker: "init.templateDir=\"$template_dir\"", value: "init.templateDir=/unsafe", want: "control fetch script"},
		{name: "nonempty template accepted", marker: "test -z \"$(/usr/bin/find \"$template_dir\" -mindepth 1 -print -quit)\"", value: "true", want: "control fetch script"},
		{name: "temporary path escapes runner", marker: "case \"$temporary_path\" in \"$runner_temp\"/*)", value: "true", want: "control fetch script"},
		{name: "control path escapes runner", marker: "dirname -- \"$control_dir\")\" && pwd -P)\" = \"$runner_temp\"", value: "true", want: "control fetch script"},
		{name: "wrong workflow SHA", marker: "control_git -C \"$control_stage\" fetch --depth=1 --no-tags --no-recurse-submodules https://github.com/NVIDIA/k8s-test-infra.git \"$GITHUB_WORKFLOW_SHA\"", value: "control_git -C \"$control_stage\" fetch --depth=1 --no-tags --no-recurse-submodules https://github.com/NVIDIA/k8s-test-infra.git HEAD", want: "control fetch script"},
		{name: "wrong fetched object", marker: "cat-file -t FETCH_HEAD)\" = \"commit\"", value: "cat-file -t FETCH_HEAD)\" = \"blob\"", want: "control fetch script"},
		{name: "workflow mode", marker: "^100644\\ blob", value: "^100755\\ blob", want: "control fetch script"},
		{name: "wrong helper blob", marker: "test \"${helper_entries[0]}\" = \"100755 blob " + mokkaControlHelperOID + "\t.github/scripts/mokka-cherry-pick.sh\"", value: "test \"${helper_entries[0]}\" = \"100755 blob e9949e908ceff74bace83934019edf9e8f2ba80f\t.github/scripts/mokka-cherry-pick.sh\"", want: "control fetch script"},
		{name: "wrong helper SHA-256", marker: "test \"$(control_git -C \"$control_stage\" cat-file blob " + mokkaControlHelperOID + " | /usr/bin/sha256sum | /usr/bin/awk '{print $1}')\" = \"" + mokkaControlHelperSHA256 + "\"", value: "test \"$(control_git -C \"$control_stage\" cat-file blob " + mokkaControlHelperOID + " | /usr/bin/sha256sum | /usr/bin/awk '{print $1}')\" = \"dd4514b9fc84bfd05eb3a6cee6ad454bbc76bb65798e32fe9954b5e879d7e73f\"", want: "control fetch script"},
		{name: "wrong helper size", marker: "test \"$(control_git -C \"$control_stage\" cat-file -s " + mokkaControlHelperOID + ")\" = \"" + strconv.Itoa(mokkaControlHelperSize) + "\"", value: "test \"$(control_git -C \"$control_stage\" cat-file -s " + mokkaControlHelperOID + ")\" = \"" + strconv.Itoa(mokkaControlHelperSize+1) + "\"", want: "control fetch script"},
		{name: "helper symlink mode", marker: "100755 blob " + mokkaControlHelperOID, value: "120000 blob " + mokkaControlHelperOID, want: "control fetch script"},
		{name: "non-bare control repository", marker: "init --bare --quiet", value: "init --quiet", want: "control fetch script"},
		{name: "extra local configuration allowed", marker: "config --local --name-only --list | /usr/bin/sort", value: "config --local --name-only --get-regexp '^core\\.' | /usr/bin/sort", want: "control fetch script"},
		{name: "tree helper extraction", marker: "control_git -C \"$control_stage\" cat-file blob " + mokkaControlHelperOID + " > \"$helper_stage/mokka-cherry-pick.sh\"", value: "control_git -C \"$control_stage\" checkout --quiet > \"$helper_stage/mokka-cherry-pick.sh\"", want: "control fetch script"},
		{name: "unexpected git command", marker: "          control_git -C \"$control_stage\" init --bare --quiet\n", value: "          control_git -C \"$control_stage\" status\n", want: "control fetch script", insert: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, 1, strings.Count(string(contents), tc.marker), "test mutation target must be unique")
			replacement := tc.value
			if tc.insert {
				replacement = tc.marker + tc.value
			}
			mutated := strings.Replace(string(contents), tc.marker, replacement, 1)
			require.NotEqual(t, string(contents), mutated, "test mutation must change the workflow")
			require.ErrorContains(t, validateMokkaWorkflowCriticalSteps([]byte(mutated)), tc.want)
		})
	}
}

func TestMokkaCherryPickWorkflowRejectsDriverIntegrityMutations(t *testing.T) {
	workflow, _ := mokkaPaths(t)
	contents, err := os.ReadFile(workflow)
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		marker string
		value  string
	}{
		{name: "helper path escapes runner", marker: "test \"$helper_dir\" = \"$runner_temp/mokka-helper\"", value: "true"},
		{name: "helper symlink allowed", marker: "test ! -L \"$helper_path\"", value: "true"},
		{name: "helper mode changed", marker: fmt.Sprintf("test \"$(/usr/bin/stat -c '%%h:%%a:%%s:%%F' \"$helper_path\")\" = \"1:500:%d:regular file\"", mokkaControlHelperSize), value: fmt.Sprintf("test \"$(/usr/bin/stat -c '%%h:%%a:%%s:%%F' \"$helper_path\")\" = \"1:755:%d:regular file\"", mokkaControlHelperSize)},
		{name: "helper hard link allowed", marker: fmt.Sprintf("test \"$(/usr/bin/stat -c '%%h:%%a:%%s:%%F' \"$helper_path\")\" = \"1:500:%d:regular file\"", mokkaControlHelperSize), value: fmt.Sprintf("test \"$(/usr/bin/stat -c '%%h:%%a:%%s:%%F' \"$helper_path\")\" = \"2:500:%d:regular file\"", mokkaControlHelperSize)},
		{name: "helper size changed", marker: fmt.Sprintf("test \"$(/usr/bin/stat -c '%%h:%%a:%%s:%%F' \"$helper_path\")\" = \"1:500:%d:regular file\"", mokkaControlHelperSize), value: fmt.Sprintf("test \"$(/usr/bin/stat -c '%%h:%%a:%%s:%%F' \"$helper_path\")\" = \"1:500:%d:regular file\"", mokkaControlHelperSize+1)},
		{name: "helper git blob changed", marker: "test \"$(/usr/bin/git hash-object --no-filters \"$helper_path\")\" = \"" + mokkaControlHelperOID + "\"", value: "test \"$(/usr/bin/git hash-object --no-filters \"$helper_path\")\" = \"be85133406dea4328da8b4d617c8cebc17810dce\""},
		{name: "helper git filters allowed", marker: "hash-object --no-filters", value: "hash-object"},
		{name: "helper digest changed", marker: "test \"$(/usr/bin/sha256sum \"$helper_path\" | /usr/bin/awk '{print $1}')\" = \"" + mokkaControlHelperSHA256 + "\"", value: "test \"$(/usr/bin/sha256sum \"$helper_path\" | /usr/bin/awk '{print $1}')\" = \"35aaf3f5a2e47fda2b0790ace4b77260f324a31fd2592e66cb00b0de045563a1\""},
		{name: "unfixed bash executable", marker: "exec /usr/bin/bash \"$helper_path\"", value: "exec bash \"$helper_path\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, 1, strings.Count(string(contents), tc.marker), "test mutation target must be unique")
			mutated := strings.Replace(string(contents), tc.marker, tc.value, 1)
			require.NotEqual(t, string(contents), mutated, "test mutation must change the workflow")
			require.ErrorContains(t, validateMokkaWorkflowCriticalSteps([]byte(mutated)), "driver run")
		})
	}
}

func TestMokkaCherryPickWorkflowRejectsExtraDriverEnvironment(t *testing.T) {
	workflow, _ := mokkaPaths(t)
	contents, err := os.ReadFile(workflow)
	require.NoError(t, err)
	marker := "        env:\n          GH_TOKEN: ${{ github.token }}\n"
	mutation := "        env:\n          UNREVIEWED_VALUE: unsafe\n          GH_TOKEN: ${{ github.token }}\n"
	mutated := strings.Replace(string(contents), marker, mutation, 1)
	require.NotEqual(t, string(contents), mutated, "test mutation must change the workflow")
	require.ErrorContains(t, validateMokkaWorkflowCriticalSteps([]byte(mutated)), "driver environment")
}

func TestMokkaCherryPickWorkflowRejectsTokenInheritanceOutsideDriver(t *testing.T) {
	workflow, _ := mokkaPaths(t)
	contents, err := os.ReadFile(workflow)
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		marker string
		value  string
		want   string
	}{
		{name: "workflow environment", marker: "permissions: {}\n", value: "env:\n  GH_TOKEN: ${{ github.token }}\n\npermissions: {}\n", want: "workflow environment"},
		{name: "job environment", marker: "  cherry-pick:\n", value: "  cherry-pick:\n    env:\n      GH_TOKEN: ${{ github.token }}\n", want: "cherry-pick job keys"},
		{name: "validation token environment", marker: "      - name: Validate dispatch envelope\n        env:\n          GITHUB_WORKFLOW_SHA: ${{ github.workflow_sha }}\n", value: "      - name: Validate dispatch envelope\n        env:\n          GH_TOKEN: ${{ github.token }}\n          GITHUB_WORKFLOW_SHA: ${{ github.workflow_sha }}\n", want: "validation environment"},
		{name: "control token environment", marker: "      - name: Fetch immutable control files without a credential\n        env:\n          GITHUB_WORKFLOW_SHA: ${{ github.workflow_sha }}\n", value: "      - name: Fetch immutable control files without a credential\n        env:\n          GH_TOKEN: ${{ github.token }}\n          GITHUB_WORKFLOW_SHA: ${{ github.workflow_sha }}\n", want: "control fetch environment"},
		{name: "checkout token environment", marker: "      - name: Check out validated target\n", value: "      - name: Check out validated target\n        env:\n          GH_TOKEN: ${{ github.token }}\n", want: "target checkout step keys"},
		{name: "checkout token override", marker: "          ref: ${{ inputs.target_branch }}\n", value: "          ref: ${{ inputs.target_branch }}\n          token: ${{ github.token }}\n", want: "target checkout configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, 1, strings.Count(string(contents), tc.marker), "test mutation target must be unique")
			mutated := strings.Replace(string(contents), tc.marker, tc.value, 1)
			require.NotEqual(t, string(contents), mutated, "test mutation must change the workflow")
			require.ErrorContains(t, validateMokkaWorkflowCriticalSteps([]byte(mutated)), tc.want)
		})
	}
}

func validateMokkaWorkflowCriticalSteps(contents []byte) error {
	criticalSteps, err := mokkaCriticalWorkflowSteps(contents)
	if err != nil {
		return err
	}
	if err := validateMokkaRequiredWorkflowSteps(criticalSteps); err != nil {
		return err
	}
	if err := validateMokkaValidationStep(criticalSteps["Validate dispatch envelope"]); err != nil {
		return err
	}
	if err := validateMokkaControlFetchStep(criticalSteps["Fetch immutable control files without a credential"]); err != nil {
		return err
	}
	if err := validateMokkaTargetCheckoutStep(criticalSteps["Check out validated target"]); err != nil {
		return err
	}
	if err := validateMokkaDriverEnvironment(criticalSteps["Cherry-pick merged source pull request"]); err != nil {
		return err
	}
	if criticalSteps["Cherry-pick merged source pull request"]["working-directory"] != "target" {
		return errors.New("driver working directory must be the target checkout")
	}
	if criticalSteps["Cherry-pick merged source pull request"]["run"] != mokkaDriverRun {
		return errors.New("driver run must recheck and execute only the reviewed helper")
	}
	return nil
}

func validateMokkaRequiredWorkflowSteps(criticalSteps map[string]map[string]any) error {
	for _, name := range []string{"Validate dispatch envelope", "Fetch immutable control files without a credential", "Check out validated target", "Cherry-pick merged source pull request"} {
		step, ok := criticalSteps[name]
		if !ok {
			return fmt.Errorf("missing critical step %q", name)
		}
		if value, exists := step["continue-on-error"]; exists {
			if value != false {
				return fmt.Errorf("critical step %q enables continue-on-error", name)
			}
		}
	}
	return nil
}

func mokkaCriticalWorkflowSteps(contents []byte) (map[string]map[string]any, error) {
	steps, err := mokkaCherryPickWorkflowSteps(contents)
	if err != nil {
		return nil, err
	}
	criticalSteps := map[string]map[string]any{}
	for _, rawStep := range steps {
		name, step, err := mokkaWorkflowStep(rawStep)
		if err != nil {
			return nil, err
		}
		if err := addMokkaCriticalWorkflowStep(criticalSteps, name, step); err != nil {
			return nil, err
		}
	}
	return criticalSteps, nil
}

func mokkaCherryPickWorkflowSteps(contents []byte) ([]any, error) {
	var parsed map[string]any
	if err := yaml.Unmarshal(contents, &parsed); err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}
	if _, exists := parsed["env"]; exists {
		return nil, errors.New("workflow environment is not allowed")
	}
	jobs, ok := parsed["jobs"].(map[string]any)
	if !ok {
		return nil, errors.New("workflow jobs are not a mapping")
	}
	job, ok := jobs["cherry-pick"].(map[string]any)
	if !ok {
		return nil, errors.New("cherry-pick job is not a mapping")
	}
	if err := requireMokkaWorkflowKeys("cherry-pick job", job, "permissions", "runs-on", "steps", "timeout-minutes"); err != nil {
		return nil, err
	}
	steps, ok := job["steps"].([]any)
	if !ok {
		return nil, errors.New("cherry-pick steps are not a sequence")
	}
	return steps, nil
}

func mokkaWorkflowStep(rawStep any) (string, map[string]any, error) {
	step, ok := rawStep.(map[string]any)
	if !ok {
		return "", nil, errors.New("cherry-pick step is not a mapping")
	}
	name, _ := step["name"].(string)
	if value, exists := step["continue-on-error"]; exists && value != false {
		return "", nil, fmt.Errorf("critical step %q enables continue-on-error", name)
	}
	return name, step, nil
}

func addMokkaCriticalWorkflowStep(criticalSteps map[string]map[string]any, name string, step map[string]any) error {
	var scope string
	var expectedKeys []string
	switch name {
	case "Validate dispatch envelope":
		scope = "validation step"
		expectedKeys = []string{"env", "name", "run"}
	case "Fetch immutable control files without a credential":
		scope = "control fetch step"
		expectedKeys = []string{"env", "name", "run"}
	case "Check out validated target":
		scope = "target checkout step"
		expectedKeys = []string{"name", "uses", "with"}
	case "Cherry-pick merged source pull request":
		scope = "driver step"
		expectedKeys = []string{"env", "name", "run", "working-directory"}
	default:
		return nil
	}
	if err := requireMokkaWorkflowKeys(scope, step, expectedKeys...); err != nil {
		return err
	}
	criticalSteps[name] = step
	return nil
}

func validateMokkaValidationStep(step map[string]any) error {
	env, ok := step["env"].(map[string]any)
	if !ok || len(env) != 1 || env["GITHUB_WORKFLOW_SHA"] != "${{ github.workflow_sha }}" {
		return errors.New("validation environment must contain exactly the reviewed GITHUB_WORKFLOW_SHA value")
	}
	return nil
}

func validateMokkaControlFetchStep(step map[string]any) error {
	env, ok := step["env"].(map[string]any)
	if !ok || len(env) != 1 || env["GITHUB_WORKFLOW_SHA"] != "${{ github.workflow_sha }}" {
		return errors.New("control fetch environment must contain exactly the reviewed GITHUB_WORKFLOW_SHA value")
	}
	run, ok := step["run"].(string)
	if !ok || run != mokkaControlFetchScript {
		return errors.New("control fetch script is not the closed reviewed script")
	}
	return nil
}

func validateMokkaTargetCheckoutStep(step map[string]any) error {
	if step["uses"] != "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1" {
		return errors.New("target checkout action is not the reviewed pin")
	}
	with, ok := step["with"].(map[string]any)
	if !ok || len(with) != 6 ||
		with["ref"] != "${{ inputs.target_branch }}" ||
		with["path"] != "target" ||
		with["persist-credentials"] != true ||
		with["fetch-depth"] != float64(1) ||
		with["submodules"] != false ||
		with["lfs"] != false {
		return errors.New("target checkout configuration is not the reviewed credential boundary")
	}
	return nil
}

func requireMokkaWorkflowKeys(scope string, values map[string]any, expected ...string) error {
	if len(values) != len(expected) {
		return fmt.Errorf("%s keys are not exact", scope)
	}
	for _, key := range expected {
		if _, ok := values[key]; !ok {
			return fmt.Errorf("%s keys are not exact", scope)
		}
	}
	return nil
}

func validateMokkaDriverEnvironment(driver map[string]any) error {
	driverEnv, ok := driver["env"].(map[string]any)
	if !ok || len(driverEnv) != 2 {
		return errors.New("driver environment must contain exactly GH_TOKEN and GITHUB_WORKFLOW_SHA")
	}
	if driverEnv["GH_TOKEN"] != "${{ github.token }}" {
		return errors.New("driver environment must contain the reviewed GH_TOKEN value")
	}
	if driverEnv["GITHUB_WORKFLOW_SHA"] != "${{ github.workflow_sha }}" {
		return errors.New("driver environment must contain the reviewed GITHUB_WORKFLOW_SHA value")
	}
	return nil
}

func requirePinnedActions(t *testing.T, workflow string) {
	t.Helper()
	for _, line := range strings.Split(workflow, "\n") {
		if !strings.Contains(line, "uses:") {
			continue
		}
		at := strings.LastIndex(line, "@")
		require.Positive(t, at, "action reference must be pinned: %s", line)
		pin := strings.Fields(strings.TrimSpace(line[at+1:]))[0]
		require.Len(t, pin, 40, "action reference must be a full SHA: %s", line)
		for _, c := range pin {
			require.True(t, c >= '0' && c <= '9' || c >= 'a' && c <= 'f', "action pin must be lowercase hex: %s", line)
		}
	}
}

func TestMokkaCherryPickWorkflowPrecheckRejectsMalformedEnvelopes(t *testing.T) {
	workflow, _ := mokkaPaths(t)
	precheck := mokkaWorkflowPrecheck(t, workflow)
	for _, tc := range []struct {
		name   string
		inputs map[string]any
		env    map[string]string
	}{
		{name: "missing input", inputs: map[string]any{"pull_request_number": "1", "source_sha": mokkaSourceSHA, "target_branch": "main"}},
		{name: "extra input", inputs: map[string]any{"pull_request_number": "1", "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": mokkaActionID, "extra": "x"}},
		{name: "wrong type", inputs: map[string]any{"pull_request_number": 1, "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": mokkaActionID}},
		{name: "leading zero", inputs: map[string]any{"pull_request_number": "01", "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": mokkaActionID}},
		{name: "graphql overflow", inputs: map[string]any{"pull_request_number": "2147483648", "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": mokkaActionID}},
		{name: "enormous number", inputs: map[string]any{"pull_request_number": "999999999999999999999999999999999999999999", "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": mokkaActionID}},
		{name: "pull request number trailing newline", inputs: map[string]any{"pull_request_number": "1\n", "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": mokkaActionID}},
		{name: "uppercase SHA", inputs: map[string]any{"pull_request_number": "1", "source_sha": strings.ToUpper(mokkaSourceSHA), "target_branch": "main", "action_id": mokkaActionID}},
		{name: "source SHA trailing newline", inputs: map[string]any{"pull_request_number": "1", "source_sha": mokkaSourceSHA + "\n", "target_branch": "main", "action_id": mokkaActionID}},
		{name: "non-v4 UUID", inputs: map[string]any{"pull_request_number": "1", "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": "123e4567-e89b-12d3-a456-426614174000"}},
		{name: "action ID trailing newline", inputs: map[string]any{"pull_request_number": "1", "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": mokkaActionID + "\n"}},
		{name: "wrong target", inputs: map[string]any{"pull_request_number": "1", "source_sha": mokkaSourceSHA, "target_branch": "release", "action_id": mokkaActionID}},
		{name: "target branch trailing newline", inputs: map[string]any{"pull_request_number": "1", "source_sha": mokkaSourceSHA, "target_branch": "main\n", "action_id": mokkaActionID}},
		{name: "wrong repository", inputs: validMokkaInputs(), env: map[string]string{"GITHUB_REPOSITORY": "fork/repository"}},
		{name: "wrong repository ID", inputs: validMokkaInputs(), env: map[string]string{"GITHUB_REPOSITORY_ID": "1"}},
		{name: "wrong workflow SHA", inputs: validMokkaInputs(), env: map[string]string{"GITHUB_WORKFLOW_SHA": "not-a-sha"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Errorf(t, runMokkaPrecheck(t, precheck, tc.inputs, tc.env), "invalid pre-checkout envelope accepted: %#v", tc.inputs)
		})
	}
	require.NoError(t, runMokkaPrecheck(t, precheck, validMokkaInputs(), nil))
}

func TestMokkaCherryPickWorkflowPrecheckRejectsConcatenatedEventDocuments(t *testing.T) {
	workflow, _ := mokkaPaths(t)
	precheck := mokkaWorkflowPrecheck(t, workflow)
	valid := mokkaEventPayload(t, validMokkaInputs())
	invalid := []byte(`{}`)
	for _, tc := range []struct {
		name    string
		payload []byte
	}{
		{name: "rejected then accepted", payload: mokkaConcatenatedJSON(invalid, valid)},
		{name: "accepted then rejected", payload: mokkaConcatenatedJSON(valid, invalid)},
		{name: "two accepted documents", payload: mokkaConcatenatedJSON(valid, valid)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, runMokkaPrecheckWithEventPayload(t, precheck, tc.payload, nil), "the precheck accepted more than one top-level event document")
		})
	}
}

func TestMokkaCherryPickRejectsInvalidInputsBeforeGit(t *testing.T) {
	_, script := mokkaPaths(t)
	for _, tc := range []struct {
		name   string
		inputs map[string]any
	}{
		{name: "missing input", inputs: map[string]any{"pull_request_number": "1", "source_sha": mokkaSourceSHA, "target_branch": "main"}},
		{name: "extra input", inputs: map[string]any{"pull_request_number": "1", "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": mokkaActionID, "other": "x"}},
		{name: "non string", inputs: map[string]any{"pull_request_number": 1, "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": mokkaActionID}},
		{name: "leading zero", inputs: map[string]any{"pull_request_number": "01", "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": mokkaActionID}},
		{name: "graphql overflow", inputs: map[string]any{"pull_request_number": "2147483648", "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": mokkaActionID}},
		{name: "enormous number", inputs: map[string]any{"pull_request_number": "999999999999999999999999999999999999999999", "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": mokkaActionID}},
		{name: "pull request number trailing newline", inputs: map[string]any{"pull_request_number": "1\n", "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": mokkaActionID}},
		{name: "uppercase sha", inputs: map[string]any{"pull_request_number": "1", "source_sha": strings.ToUpper(mokkaSourceSHA), "target_branch": "main", "action_id": mokkaActionID}},
		{name: "source SHA trailing newline", inputs: map[string]any{"pull_request_number": "1", "source_sha": mokkaSourceSHA + "\n", "target_branch": "main", "action_id": mokkaActionID}},
		{name: "non v4 UUID", inputs: map[string]any{"pull_request_number": "1", "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": "123e4567-e89b-12d3-a456-426614174000"}},
		{name: "action ID trailing newline", inputs: map[string]any{"pull_request_number": "1", "source_sha": mokkaSourceSHA, "target_branch": "main", "action_id": mokkaActionID + "\n"}},
		{name: "other target", inputs: map[string]any{"pull_request_number": "1", "source_sha": mokkaSourceSHA, "target_branch": "release", "action_id": mokkaActionID}},
		{name: "target branch trailing newline", inputs: map[string]any{"pull_request_number": "1", "source_sha": mokkaSourceSHA, "target_branch": "main\n", "action_id": mokkaActionID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runMokkaDriver(t, script, tc.inputs, nil)
			require.Error(t, result.err)
			require.Empty(t, result.gitLog, "invalid input made a git call: %s", result.gitLog)
			require.Empty(t, mokkaGitHubMutationCapableCalls(result.ghCallLog), "invalid input made a mutation-capable GitHub call: %s", result.ghCallLog)
		})
	}
	for name, env := range map[string]map[string]string{
		"wrong repository":    {"GITHUB_REPOSITORY": "fork/repository"},
		"wrong repository ID": {"GITHUB_REPOSITORY_ID": "1"},
		"wrong workflow SHA":  {"GITHUB_WORKFLOW_SHA": "not-a-sha"},
	} {
		t.Run(name, func(t *testing.T) {
			result := runMokkaDriver(t, script, validMokkaInputs(), env)
			require.Error(t, result.err)
			require.Empty(t, result.gitLog, "invalid environment made a git call: %s", result.gitLog)
			require.Empty(t, mokkaGitHubMutationCapableCalls(result.ghCallLog), "invalid environment made a mutation-capable GitHub call: %s", result.ghCallLog)
		})
	}
}

func TestMokkaCherryPickRejectsConcatenatedEventDocumentsBeforeGit(t *testing.T) {
	_, script := mokkaPaths(t)
	valid := mokkaEventPayload(t, validMokkaInputs())
	invalid := []byte(`{}`)
	for _, tc := range []struct {
		name    string
		payload []byte
	}{
		{name: "rejected then accepted", payload: mokkaConcatenatedJSON(invalid, valid)},
		{name: "accepted then rejected", payload: mokkaConcatenatedJSON(valid, invalid)},
		{name: "two accepted documents", payload: mokkaConcatenatedJSON(valid, valid)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runMokkaDriverWithEventPayload(t, script, tc.payload, nil)
			require.Error(t, result.err)
			require.Contains(t, result.output, "invalid event inputs", "the event parser did not reject the document boundary")
			require.Empty(t, result.gitLog, "a concatenated event made a git call: %s", result.gitLog)
			require.Empty(t, mokkaGitHubMutationCapableCalls(result.ghCallLog), "a concatenated event made a mutation-capable GitHub call: %s", result.ghCallLog)
		})
	}
}

func TestMokkaCherryPickCollisionsAndConflictDoNotMutate(t *testing.T) {
	_, script := mokkaPaths(t)
	inputs := validMokkaInputs()
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "existing branch", env: map[string]string{"FAKE_BRANCH": "exists"}},
		{name: "branch forbidden", env: map[string]string{"FAKE_BRANCH": "forbidden"}},
		{name: "branch rate limited", env: map[string]string{"FAKE_BRANCH": "rate-limited"}},
		{name: "branch service failure", env: map[string]string{"FAKE_BRANCH": "failure"}},
		{name: "branch response embeds 404", env: map[string]string{"FAKE_BRANCH": "embedded"}},
		{name: "existing pull request", env: map[string]string{"FAKE_PULL": "exists"}},
		{name: "conflict", env: map[string]string{"FAKE_GIT_CONFLICT": "1"}, want: "cherry-pick --abort"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runMokkaDriver(t, script, inputs, tc.env)
			require.Error(t, result.err)
			if tc.name == "conflict" {
				require.True(t, mokkaCommandLogContainsExact(result.gitLog, "cherry-pick", "--abort"))
			} else {
				require.False(t, mokkaCommandLogContainsCommand(result.gitLog, "cherry-pick"))
			}
			require.False(t, mokkaCommandLogContainsCommand(result.gitLog, "push"))
			require.Empty(t, mokkaGitHubMutationCapableCalls(result.ghCallLog), "collision or conflict made a mutation-capable GitHub call: %s", result.ghCallLog)
		})
	}
}

func TestMokkaCherryPickCreatesCorrelatedCommitInRealRepository(t *testing.T) {
	_, script := mokkaPaths(t)
	repository := newMokkaRealRepository(t, false)
	targetBaseSHA := strings.TrimSpace(runMokkaGit(t, repository.target, "rev-parse", "HEAD"))
	result := runMokkaRealDriver(t, script, repository)
	require.NoError(t, result.err, result.output)
	producedHeadSHA := strings.TrimSpace(runMokkaGit(t, repository.target, "rev-parse", "HEAD"))
	require.Equal(t, mokkaExpectedRealGitHubCallLog(repository.sourceSHA, targetBaseSHA, producedHeadSHA), strings.TrimSpace(result.ghCallLog))

	message := runMokkaGit(t, repository.target, "show", "-s", "--format=%B", "HEAD")
	require.Contains(t, message, "source change\n")
	require.Contains(t, message, "Mokka-Source-SHA: "+repository.sourceSHA+"\n")
	require.Contains(t, message, "Mokka-Action-ID: "+mokkaActionID+"\n")
	lowerMessage := strings.ToLower(message)
	require.Equal(t, 1, strings.Count(lowerMessage, "mokka-source-sha:"))
	require.Equal(t, 1, strings.Count(lowerMessage, "mokka-action-id:"))
	committer := strings.TrimSpace(runMokkaGit(t, repository.target, "show", "-s", "--format=%cn <%ce>", "HEAD"))
	require.Equal(t, "mokka[bot] <mokka[bot]@users.noreply.github.com>", committer)
}

func TestMokkaCherryPickConflictAbortsRealRepositoryState(t *testing.T) {
	_, script := mokkaPaths(t)
	repository := newMokkaRealRepository(t, true)
	targetHead := strings.TrimSpace(runMokkaGit(t, repository.target, "rev-parse", "HEAD"))
	result := runMokkaRealDriver(t, script, repository)
	require.Error(t, result.err)
	require.Empty(t, mokkaGitHubMutationCapableCallsForSource(result.ghCallLog, repository.sourceSHA), "real conflict made a mutation-capable GitHub call: %s", result.ghCallLog)

	cherryPickHead := exec.Command("git", "rev-parse", "--verify", "--quiet", "CHERRY_PICK_HEAD")
	cherryPickHead.Dir = repository.target
	require.Error(t, cherryPickHead.Run())
	require.NoFileExists(t, filepath.Join(repository.target, ".git", "CHERRY_PICK_HEAD"))
	require.Empty(t, strings.TrimSpace(runMokkaGit(t, repository.target, "status", "--porcelain")))
	require.Equal(t, targetHead, strings.TrimSpace(runMokkaGit(t, repository.target, "rev-parse", "HEAD")))
	derivedRef := exec.Command("git", "--git-dir", repository.remote, "show-ref", "--verify", "--quiet", "refs/heads/mokka/cherry-pick/"+mokkaActionID)
	require.Error(t, derivedRef.Run())
	contents, err := os.ReadFile(filepath.Join(repository.target, "conflict.txt"))
	require.NoError(t, err)
	for _, marker := range []string{"<<<<<<<", "=======", ">>>>>>>"} {
		require.NotContains(t, string(contents), marker)
	}
}

func TestMokkaCherryPickConcurrentBranchCreatePreservesCompetingRealRef(t *testing.T) {
	_, script := mokkaPaths(t)
	repository := newMokkaRealRepository(t, false)
	targetBaseSHA := strings.TrimSpace(runMokkaGit(t, repository.target, "rev-parse", "HEAD"))
	result := runMokkaRealDriverWithEnv(t, script, repository, map[string]string{"MOKKA_REAL_PUSH_RACE": "1"})
	require.Error(t, result.err, "the create-only push must reject a concurrently-created branch")
	require.Equal(t, mokkaExpectedPreMutationGitHubCallLog(repository.sourceSHA), strings.TrimSpace(result.ghCallLog))
	require.Empty(t, mokkaGitHubMutationCapableCallsForSource(result.ghCallLog, repository.sourceSHA), "the rejected push made a mutation-capable GitHub call: %s", result.ghCallLog)

	head := "mokka/cherry-pick/" + mokkaActionID
	competingSHA := strings.TrimSpace(runMokkaGit(t, repository.target, "--git-dir", repository.remote, "rev-parse", "refs/heads/"+head))
	require.Equal(t, targetBaseSHA, competingSHA, "the rejected push must not replace the competing branch")
}

func TestMokkaCherryPickRejectsIneligibleSourceBeforeGit(t *testing.T) {
	_, script := mokkaPaths(t)
	for _, source := range []string{
		"pull-read-failure",
		"pull-malformed",
		"pull-root-array",
		"pull-missing-state",
		"pull-state-wrong-type",
		"pull-commits-missing",
		"pull-commits-null",
		"pull-commits-string",
		"pull-commits-zero",
		"pull-commits-fractional",
		"pull-commits-two",
		"open",
		"unmerged-with-merged-at",
		"wrong-sha",
		"fork-head",
		"fork-base",
		"commit-read-failure",
		"commit-malformed",
		"commit-root-array",
		"commit-missing-sha",
		"commit-wrong-sha",
		"commit-sha-number",
		"commit-missing-parents",
		"commit-parents-null",
		"commit-parents-object",
		"zero-parents",
		"two-parents",
		"parent-null",
		"parent-string",
		"parent-missing-sha",
		"parent-sha-null",
		"parent-sha-number",
		"parent-sha-uppercase",
		"parent-sha-short",
		"parent-sha-trailing-newline",
	} {
		t.Run(source, func(t *testing.T) {
			result := runMokkaDriver(t, script, validMokkaInputs(), map[string]string{"FAKE_SOURCE": source})
			require.Error(t, result.err)
			require.Empty(t, result.gitLog, "ineligible source made a git call: %s", result.gitLog)
			require.Empty(t, mokkaGitHubMutationCapableCalls(result.ghCallLog), "ineligible source made a mutation-capable GitHub call: %s", result.ghCallLog)
		})
	}
}

func TestMokkaCherryPickRejectsConcatenatedProviderDocumentsBeforeGit(t *testing.T) {
	_, script := mokkaPaths(t)
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "source pull request rejected then accepted", env: map[string]string{"FAKE_SOURCE": "pull-rejected-then-accepted-documents"}, want: "source pull request is not eligible"},
		{name: "source pull request accepted then rejected", env: map[string]string{"FAKE_SOURCE": "pull-accepted-then-rejected-documents"}, want: "source pull request is not eligible"},
		{name: "source commit rejected then accepted", env: map[string]string{"FAKE_SOURCE": "commit-rejected-then-accepted-documents"}, want: "source commit must match the requested SHA and have one canonical parent"},
		{name: "source commit accepted then rejected", env: map[string]string{"FAKE_SOURCE": "commit-accepted-then-rejected-documents"}, want: "source commit must match the requested SHA and have one canonical parent"},
		{name: "derived pull request rejected then accepted", env: map[string]string{"FAKE_PULL": "rejected-then-accepted-documents"}, want: "derived pull request already exists"},
		{name: "derived pull request accepted then rejected", env: map[string]string{"FAKE_PULL": "accepted-then-rejected-documents"}, want: "derived pull request already exists"},
		{name: "derived pull request read failure", env: map[string]string{"FAKE_PULL": "read-failure"}, want: "derived pull request lookup failed"},
		{name: "derived pull request malformed JSON", env: map[string]string{"FAKE_PULL": "malformed"}, want: "derived pull request already exists"},
		{name: "derived pull request wrong root type", env: map[string]string{"FAKE_PULL": "wrong-root-type"}, want: "derived pull request already exists"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runMokkaDriver(t, script, validMokkaInputs(), tc.env)
			require.Error(t, result.err)
			require.Contains(t, result.output, tc.want)
			require.Empty(t, result.gitLog, "a rejected provider read made a git call: %s", result.gitLog)
			require.Empty(t, mokkaGitHubMutationCapableCalls(result.ghCallLog), "a rejected provider read made a mutation-capable GitHub call: %s", result.ghCallLog)
		})
	}
}

func TestMokkaCherryPickRejectsInvalidCreatedPullRequestResponse(t *testing.T) {
	_, script := mokkaPaths(t)
	for _, response := range []string{
		"malformed",
		"root-array",
		"missing-number",
		"string-number",
		"zero-number",
		"negative-number",
		"floating-number",
		"null-number",
		"boolean-number",
		"overflow-number",
		"missing-url",
		"url-number",
		"false-draft",
		"wrong-head",
		"wrong-head-sha",
		"wrong-base",
	} {
		t.Run(response, func(t *testing.T) {
			result := runMokkaDriver(t, script, validMokkaInputs(), map[string]string{"FAKE_CREATED_PR": response})
			require.Error(t, result.err)
			require.Equal(t, []string{mokkaExpectedCreatePullRequestCall()}, mokkaGitHubMutationCapableCalls(result.ghCallLog))
		})
	}
}

func TestMokkaCherryPickRejectsConcatenatedCreatedPullRequestDocuments(t *testing.T) {
	_, script := mokkaPaths(t)
	for _, response := range []string{
		"rejected-then-accepted-documents",
		"accepted-then-rejected-documents",
	} {
		t.Run(response, func(t *testing.T) {
			result := runMokkaDriver(t, script, validMokkaInputs(), map[string]string{"FAKE_CREATED_PR": response})
			require.Error(t, result.err)
			require.Contains(t, result.output, "pull request response is not the requested draft", "the create-response parser did not reject the document boundary")
			require.Equal(t, []string{mokkaExpectedCreatePullRequestCall()}, mokkaGitHubMutationCapableCalls(result.ghCallLog), "a rejected create response must not cause a PATCH")
		})
	}
}

func TestMokkaCherryPickSuccessUsesBoundedWritesAndEvidence(t *testing.T) {
	_, script := mokkaPaths(t)
	result := runMokkaDriver(t, script, validMokkaInputs(), nil)
	require.NoError(t, result.err, result.output)
	head := "mokka/cherry-pick/" + mokkaActionID
	require.True(t, mokkaSuccessMutationLogsAreExact(result.gitLog, result.ghCallLog), "unexpected successful mutation log:\ngit:\n%s\ngh calls:\n%s", result.gitLog, result.ghCallLog)
	require.Equal(t, mokkaExpectedSuccessGitHubCallLog(), strings.TrimSpace(result.ghCallLog), "success must issue only the reviewed GitHub calls with the reviewed raw field values")
	wrongOrderLines := mokkaLogLines(result.gitLog)
	require.Len(t, wrongOrderLines, 9)
	wrongOrderLines[1], wrongOrderLines[2] = wrongOrderLines[2], wrongOrderLines[1]
	wrongOrder := strings.Join(wrongOrderLines, "\n")
	require.False(t, mokkaSuccessMutationLogsAreExact(wrongOrder, result.ghCallLog), "the command order outside the pipeline pair is part of the mutation contract")
	require.False(t, mokkaSuccessMutationLogsAreExact(result.gitLog+"\n"+mokkaGitHubCallLedger("push", "--porcelain", "--atomic", "--force-with-lease=refs/heads/other:", "origin", "HEAD:refs/heads/other")+"\n", result.ghCallLog))
	require.False(t, mokkaSuccessMutationLogsAreExact(result.gitLog, result.ghCallLog+"\n"+mokkaGitHubCallLedger("api", "--method", "POST", "/repos/"+mokkaRepository+"/pulls")))
	require.False(t, mokkaCommandLogContainsArg(result.gitLog, "--force"))
	require.False(t, mokkaCommandLogContainsArgSubstring(result.gitLog, "--force-with-lease=refs/heads/"+head+":cccc"))
	require.False(t, mokkaCommandLogContainsArgSubstring(result.gitLog, "refs/heads/main"))
	require.False(t, mokkaCommandLogContainsCommand(result.gitLog, "tag"))
	require.Equal(t, []string{mokkaExpectedCreatePullRequestCall(), mokkaExpectedEvidencePatchCall()}, mokkaGitHubMutationCapableCalls(result.ghCallLog))
}

func TestMokkaCherryPickUsesCreatedPullRequestNumberForEvidencePatch(t *testing.T) {
	_, script := mokkaPaths(t)
	const createdPullRequestNumber = "123"
	result := runMokkaDriver(t, script, validMokkaInputs(), map[string]string{"FAKE_CREATED_PR_NUMBER": createdPullRequestNumber})
	require.NoError(t, result.err, result.output)
	require.Equal(t, mokkaExpectedSuccessGitHubCallLogFor(createdPullRequestNumber), strings.TrimSpace(result.ghCallLog))
	require.Equal(t, []string{
		mokkaExpectedCreatePullRequestCall(),
		mokkaExpectedEvidencePatchCallFor(createdPullRequestNumber, createdPullRequestNumber),
	}, mokkaGitHubMutationCapableCalls(result.ghCallLog))
}

func TestMokkaCherryPickFakeRejectsPatchToWrongPullRequest(t *testing.T) {
	_, script := mokkaPaths(t)
	contents, err := os.ReadFile(script)
	require.NoError(t, err)
	marker := `"/repos/$repository/pulls/$pull_request_number_created"`
	require.Equal(t, 1, strings.Count(string(contents), marker), "test mutation target must be unique")
	mutated := strings.Replace(string(contents), marker, `"/repos/$repository/pulls/99"`, 1)
	mutatedScript := filepath.Join(t.TempDir(), "mokka-cherry-pick.sh")
	writeMokkaFake(t, mutatedScript, mutated)
	result := runMokkaDriver(t, mutatedScript, validMokkaInputs(), map[string]string{"FAKE_CREATED_PR_NUMBER": "123"})
	require.Error(t, result.err, "fake gh must fail closed for a PATCH to the wrong pull request")
	require.Equal(t, []string{
		mokkaExpectedCreatePullRequestCall(),
		mokkaExpectedEvidencePatchCallFor("99", "123"),
	}, mokkaGitHubMutationCapableCalls(result.ghCallLog))
}

func TestMokkaCherryPickGitFakeRejectsUnreviewedCommands(t *testing.T) {
	_, script := mokkaPaths(t)
	contents, err := os.ReadFile(script)
	require.NoError(t, err)
	marker := `source_pull_request="$(gh api "/repos/$repository/pulls/$pull_request_number")"`
	mutation := "git tag v0.11.0\n" + marker
	mutated := strings.Replace(string(contents), marker, mutation, 1)
	require.NotEqual(t, string(contents), mutated, "test mutation must change the driver")
	mutatedScript := filepath.Join(t.TempDir(), "mokka-cherry-pick.sh")
	writeMokkaFake(t, mutatedScript, mutated)
	result := runMokkaDriver(t, mutatedScript, validMokkaInputs(), nil)
	require.Error(t, result.err, "fake git must fail closed for an unreviewed command")
	require.Equal(t, []string{mokkaGitHubCallLedger("tag", "v0.11.0")}, mokkaLogLines(result.gitLog))
	require.Empty(t, result.ghCallLog, "the unreviewed Git command must fail before a GitHub call")
}

func TestMokkaCherryPickFakeRejectsUnreviewedGitHubMutations(t *testing.T) {
	_, script := mokkaPaths(t)
	contents, err := os.ReadFile(script)
	require.NoError(t, err)
	marker := `source_pull_request="$(gh api "/repos/$repository/pulls/$pull_request_number")"`
	for _, tc := range []struct {
		name string
		args []string
		line string
	}{
		{name: "pull request merge", args: []string{"pr", "merge", "99"}, line: "gh pr merge 99"},
		{name: "pull request approval", args: []string{"pr", "review", "99", "--approve"}, line: "gh pr review 99 --approve"},
		{name: "review API post", args: []string{"api", "--method", "POST", "/repos/" + mokkaRepository + "/pulls/99/reviews"}, line: "gh api --method POST \"/repos/" + mokkaRepository + "/pulls/99/reviews\""},
		{name: "tag ref API post", args: []string{"api", "--method", "POST", "/repos/" + mokkaRepository + "/git/refs", "--raw-field", "ref=refs/tags/v0.11.0"}, line: "gh api --method POST \"/repos/" + mokkaRepository + "/git/refs\" --raw-field \"ref=refs/tags/v0.11.0\""},
		{name: "ruleset API post", args: []string{"api", "--method", "POST", "/repos/" + mokkaRepository + "/rulesets"}, line: "gh api --method POST \"/repos/" + mokkaRepository + "/rulesets\""},
		{name: "workflow API put", args: []string{"api", "--method", "PUT", "/repos/" + mokkaRepository + "/actions/workflows/mokka-cherry-pick.yml/enable"}, line: "gh api --method PUT \"/repos/" + mokkaRepository + "/actions/workflows/mokka-cherry-pick.yml/enable\""},
		{name: "API delete", args: []string{"api", "--method", "DELETE", "/repos/" + mokkaRepository + "/git/refs/heads/main"}, line: "gh api --method DELETE \"/repos/" + mokkaRepository + "/git/refs/heads/main\""},
		{name: "pull request ready", args: []string{"pr", "ready", "99"}, line: "gh pr ready 99"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := strings.Replace(string(contents), marker, tc.line+"\n"+marker, 1)
			require.NotEqual(t, string(contents), mutated, "test mutation must change the driver")
			mutatedScript := filepath.Join(t.TempDir(), "mokka-cherry-pick.sh")
			writeMokkaFake(t, mutatedScript, mutated)
			result := runMokkaDriver(t, mutatedScript, validMokkaInputs(), nil)
			require.Error(t, result.err, "fake gh must fail closed for %q", tc.line)
			require.Empty(t, result.gitLog, "unreviewed GitHub mutation reached git: %s", result.gitLog)
			require.Equal(t, []string{mokkaGitHubCallLedger(tc.args...)}, mokkaGitHubMutationCapableCalls(result.ghCallLog))
		})
	}
}

func TestMokkaCherryPickPullRequestCreateFailureStopsWithoutRetry(t *testing.T) {
	_, script := mokkaPaths(t)
	result := runMokkaDriver(t, script, validMokkaInputs(), map[string]string{"FAKE_CREATED_PR": "failure"})
	require.Error(t, result.err)
	require.Equal(t, []string{mokkaExpectedCreatePullRequestCall()}, mokkaGitHubMutationCapableCalls(result.ghCallLog))
}

func TestMokkaCherryPickConcurrentBranchCreateStopsBeforePullRequest(t *testing.T) {
	_, script := mokkaPaths(t)
	result := runMokkaDriver(t, script, validMokkaInputs(), map[string]string{"FAKE_PUSH_RACE": "1"})
	require.Error(t, result.err)
	head := "mokka/cherry-pick/" + mokkaActionID
	expectedPush := mokkaGitHubCallLedger("push", "--porcelain", "--atomic", "--force-with-lease=refs/heads/"+head+":", "origin", "HEAD:refs/heads/"+head)
	require.Equal(t, []string{expectedPush}, mokkaCommandLogCalls(result.gitLog, "push"), "the race path must attempt the reviewed create-only push exactly once")
	require.Equal(t, mokkaExpectedPreMutationGitHubCallLog(mokkaSourceSHA), strings.TrimSpace(result.ghCallLog))
	require.Empty(t, mokkaGitHubMutationCapableCalls(result.ghCallLog))
}

func TestMokkaCherryPickPatchFailureLeavesCorrelatedDraftForInvestigation(t *testing.T) {
	_, script := mokkaPaths(t)
	result := runMokkaDriver(t, script, validMokkaInputs(), map[string]string{"FAKE_PATCH_FAILURE": "1"})
	require.Error(t, result.err)
	require.Contains(t, result.output, "MOKKA_CHERRY_PICK_MANUAL_INVESTIGATION action_id="+mokkaActionID)
	require.Contains(t, result.output, "pull_request_url=https://github.com/NVIDIA/k8s-test-infra/pull/99")
	require.Equal(t, []string{mokkaExpectedCreatePullRequestCall(), mokkaExpectedEvidencePatchCall()}, mokkaGitHubMutationCapableCalls(result.ghCallLog))
}

type mokkaRunResult struct {
	err       error
	output    string
	gitLog    string
	ghCallLog string
}

func validMokkaInputs() map[string]any {
	return map[string]any{
		"pull_request_number": "1",
		"source_sha":          mokkaSourceSHA,
		"target_branch":       "main",
		"action_id":           mokkaActionID,
	}
}

func mokkaEventPayload(t *testing.T, inputs map[string]any) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"inputs": inputs})
	require.NoError(t, err)
	return payload
}

func mokkaConcatenatedJSON(documents ...[]byte) []byte {
	var payload []byte
	for _, document := range documents {
		payload = append(payload, document...)
		payload = append(payload, '\n')
	}
	return payload
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func mokkaSuccessMutationLogsAreExact(gitLog, ghCallLog string) bool {
	head := "mokka/cherry-pick/" + mokkaActionID
	expectedGit := []string{
		mokkaGitHubCallLedger("rev-parse", "HEAD"),
		mokkaGitHubCallLedger("config", "user.name", "mokka[bot]"),
		mokkaGitHubCallLedger("config", "user.email", "mokka[bot]@users.noreply.github.com"),
		mokkaGitHubCallLedger("fetch", "origin", mokkaSourceSHA),
		mokkaGitHubCallLedger("cherry-pick", mokkaSourceSHA),
		mokkaGitHubCallLedger("show", "-s", "--format=%B", "HEAD"),
		mokkaGitHubCallLedger("commit", "--amend", "--file", "-", "--trailer", "Mokka-Source-SHA: "+mokkaSourceSHA, "--trailer", "Mokka-Action-ID: "+mokkaActionID),
		mokkaGitHubCallLedger("rev-parse", "HEAD"),
		mokkaGitHubCallLedger("push", "--porcelain", "--atomic", "--force-with-lease=refs/heads/"+head+":", "origin", "HEAD:refs/heads/"+head),
	}
	return mokkaSuccessGitLogIsExact(mokkaLogLines(gitLog), expectedGit) &&
		mokkaGitHubCallLogIsExact(ghCallLog, mokkaExpectedSuccessGitHubCallLog())
}

func mokkaSuccessGitLogIsExact(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range 5 {
		if actual[index] != expected[index] {
			return false
		}
	}
	// The show-to-amend pipeline starts two processes. Its log append order is
	// not stable, but every command before and after this pair remains ordered.
	if !(actual[5] == expected[5] && actual[6] == expected[6] || actual[5] == expected[6] && actual[6] == expected[5]) {
		return false
	}
	return actual[7] == expected[7] && actual[8] == expected[8]
}

func mokkaLogLines(log string) []string {
	trimmed := strings.TrimSpace(log)
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func mokkaExpectedEvidenceFor(sourceSHA, targetBaseSHA, producedHeadSHA, createdPullRequestNumber string) string {
	head := "mokka/cherry-pick/" + mokkaActionID
	// Command substitution removes the final newline from evidence_payload. The
	// digest must therefore cover the exact bytes that the driver supplies.
	payload := fmt.Sprintf("action_id: %s\nsource_pull_request: 1\nsource_sha: %s\ntarget_branch: main\ntarget_base_sha: %s\nproduced_head_sha: %s\nworkflow_commit_sha: %s\nhead_branch: %s\npull_request_url: https://github.com/NVIDIA/k8s-test-infra/pull/%s", mokkaActionID, sourceSHA, targetBaseSHA, producedHeadSHA, mokkaWorkflowSHA, head, createdPullRequestNumber)
	digest := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("<!-- mokka-cherry-pick-evidence/v1\n%s\nsha256: %x\n-->\n", payload, digest)
}

func mokkaExpectedSuccessGitHubCallLog() string {
	return mokkaExpectedSuccessGitHubCallLogFor("99")
}

func mokkaExpectedSuccessGitHubCallLogFor(createdPullRequestNumber string) string {
	return strings.Join([]string{
		mokkaExpectedPreMutationGitHubCallLog(mokkaSourceSHA),
		mokkaExpectedCreatePullRequestCall(),
		mokkaExpectedEvidencePatchCallFor(createdPullRequestNumber, createdPullRequestNumber),
	}, "\n")
}

func mokkaExpectedRealGitHubCallLog(sourceSHA, targetBaseSHA, producedHeadSHA string) string {
	return strings.Join([]string{
		mokkaExpectedPreMutationGitHubCallLog(sourceSHA),
		mokkaExpectedCreatePullRequestCall(),
		mokkaGitHubCallLedger("api", "--method", "PATCH", "/repos/"+mokkaRepository+"/pulls/99", "--raw-field", "body="+mokkaExpectedEvidenceFor(sourceSHA, targetBaseSHA, producedHeadSHA, "99")),
	}, "\n")
}

func mokkaExpectedPreMutationGitHubCallLog(sourceSHA string) string {
	head := "mokka/cherry-pick/" + mokkaActionID
	return strings.Join([]string{
		mokkaGitHubCallLedger("api", "/repos/"+mokkaRepository+"/pulls/1"),
		mokkaGitHubCallLedger("api", "/repos/"+mokkaRepository+"/commits/"+sourceSHA),
		mokkaGitHubCallLedger("api", "--include", "/repos/"+mokkaRepository+"/git/ref/heads/"+head),
		mokkaGitHubCallLedger("api", "/repos/"+mokkaRepository+"/pulls?state=all&per_page=1&head=NVIDIA:"+head+"&base=main"),
	}, "\n")
}

func mokkaExpectedCreatePullRequestCall() string {
	head := "mokka/cherry-pick/" + mokkaActionID
	return mokkaGitHubCallLedger("api", "--method", "POST", "/repos/"+mokkaRepository+"/pulls", "--raw-field", "title=Mokka: cherry-pick #1 to main", "--raw-field", "head="+head, "--raw-field", "base=main", "-F", "draft=true", "--raw-field", "body=<!-- mokka-cherry-pick-action-id: "+mokkaActionID+" -->")
}

func mokkaExpectedEvidencePatchCall() string {
	return mokkaExpectedEvidencePatchCallFor("99", "99")
}

func mokkaExpectedEvidencePatchCallFor(endpointPullRequestNumber, evidencePullRequestNumber string) string {
	return mokkaGitHubCallLedger("api", "--method", "PATCH", "/repos/"+mokkaRepository+"/pulls/"+endpointPullRequestNumber, "--raw-field", "body="+mokkaExpectedEvidenceFor(mokkaSourceSHA, "dddddddddddddddddddddddddddddddddddddddd", "cccccccccccccccccccccccccccccccccccccccc", evidencePullRequestNumber))
}

func mokkaGitHubCallLedger(args ...string) string {
	fields := make([]string, 0, len(args)+1)
	fields = append(fields, strconv.Itoa(len(args)))
	for _, argument := range args {
		fields = append(fields, fmt.Sprintf("%d:%s", len(argument), base64.StdEncoding.EncodeToString([]byte(argument))))
	}
	return strings.Join(fields, "|")
}

func mokkaGitHubCallLogIsExact(actual, expected string) bool {
	return strings.TrimSpace(actual) == expected
}

func mokkaGitHubMutationCapableCalls(log string) []string {
	return mokkaGitHubMutationCapableCallsForSource(log, mokkaSourceSHA)
}

func mokkaGitHubMutationCapableCallsForSource(log, sourceSHA string) []string {
	var mutations []string
	for _, line := range mokkaLogLines(log) {
		args, ok := mokkaGitHubCallArgs(line)
		if !ok || !mokkaApprovedGitHubReadCallForSource(args, sourceSHA) {
			mutations = append(mutations, line)
		}
	}
	return mutations
}

func mokkaGitHubCallArgs(line string) ([]string, bool) {
	fields := strings.Split(line, "|")
	if len(fields) == 0 {
		return nil, false
	}
	count, err := strconv.Atoi(fields[0])
	if err != nil || count < 0 || len(fields) != count+1 {
		return nil, false
	}
	args := make([]string, 0, count)
	for _, field := range fields[1:] {
		argument, ok := mokkaGitHubCallArg(field)
		if !ok {
			return nil, false
		}
		args = append(args, argument)
	}
	return args, true
}

func mokkaGitHubCallArg(field string) (string, bool) {
	lengthText, encoded, found := strings.Cut(field, ":")
	if !found {
		return "", false
	}
	length, err := strconv.Atoi(lengthText)
	if err != nil || length < 0 {
		return "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != length {
		return "", false
	}
	return string(decoded), true
}

func mokkaCommandLogContainsExact(log string, expected ...string) bool {
	for _, line := range mokkaLogLines(log) {
		args, ok := mokkaGitHubCallArgs(line)
		if ok && slicesEqual(args, expected) {
			return true
		}
	}
	return false
}

func mokkaCommandLogContainsCommand(log, command string) bool {
	for _, line := range mokkaLogLines(log) {
		args, ok := mokkaGitHubCallArgs(line)
		if ok && len(args) > 0 && args[0] == command {
			return true
		}
	}
	return false
}

func mokkaCommandLogCalls(log, command string) []string {
	var calls []string
	for _, line := range mokkaLogLines(log) {
		args, ok := mokkaGitHubCallArgs(line)
		if ok && len(args) > 0 && args[0] == command {
			calls = append(calls, line)
		}
	}
	return calls
}

func mokkaCommandLogContainsArg(log, expected string) bool {
	for _, line := range mokkaLogLines(log) {
		args, ok := mokkaGitHubCallArgs(line)
		if !ok {
			continue
		}
		for _, argument := range args {
			if argument == expected {
				return true
			}
		}
	}
	return false
}

func mokkaCommandLogContainsArgSubstring(log, expected string) bool {
	for _, line := range mokkaLogLines(log) {
		args, ok := mokkaGitHubCallArgs(line)
		if !ok {
			continue
		}
		for _, argument := range args {
			if strings.Contains(argument, expected) {
				return true
			}
		}
	}
	return false
}

func mokkaApprovedGitHubReadCallForSource(args []string, sourceSHA string) bool {
	head := "mokka/cherry-pick/" + mokkaActionID
	return slicesEqual(args, []string{"api", "/repos/" + mokkaRepository + "/pulls/1"}) ||
		slicesEqual(args, []string{"api", "/repos/" + mokkaRepository + "/commits/" + sourceSHA}) ||
		slicesEqual(args, []string{"api", "--include", "/repos/" + mokkaRepository + "/git/ref/heads/" + head}) ||
		slicesEqual(args, []string{"api", "/repos/" + mokkaRepository + "/pulls?state=all&per_page=1&head=NVIDIA:" + head + "&base=main"})
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func runMokkaDriver(t *testing.T, script string, inputs map[string]any, extraEnv map[string]string) mokkaRunResult {
	t.Helper()
	return runMokkaDriverWithEventPayload(t, script, mokkaEventPayload(t, inputs), extraEnv)
}

func runMokkaDriverWithEventPayload(t *testing.T, script string, payload []byte, extraEnv map[string]string) mokkaRunResult {
	t.Helper()
	dir := t.TempDir()
	eventPath := filepath.Join(dir, "event.json")
	require.NoError(t, os.WriteFile(eventPath, payload, 0o600))
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.Mkdir(bin, 0o755))
	gitLog := filepath.Join(dir, "git.log")
	ghCallLog := filepath.Join(dir, "gh-calls.log")
	writeMokkaFake(t, filepath.Join(bin, "git"), `#!/usr/bin/env bash
set -Eeuo pipefail
git_args=("$@")
record_call() {
  local record="$#"
  for argument in "$@"; do
    encoded="$(printf '%s' "$argument" | base64 | tr -d '\n')"
    printf -v record '%s|%s:%s' "$record" "${#argument}" "$encoded"
  done
  printf '%s\n' "$record" >> "$GIT_LOG"
}
matches() {
  local -a expected=("$@")
  local index
  [[ ${#git_args[@]} -eq ${#expected[@]} ]] || return 1
  for ((index = 0; index < ${#expected[@]}; index++)); do
    [[ "${git_args[index]}" == "${expected[index]}" ]] || return 1
  done
}
record_call "${git_args[@]}"
readonly source_sha="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
readonly action_id="123e4567-e89b-42d3-a456-426614174000"
readonly head_branch="mokka/cherry-pick/$action_id"
if matches rev-parse HEAD; then
  if [[ -f "$GIT_AFTER_PICK" ]]; then echo cccccccccccccccccccccccccccccccccccccccc; else echo dddddddddddddddddddddddddddddddddddddddd; fi
elif matches config user.name 'mokka[bot]'; then
  exit 0
elif matches config user.email 'mokka[bot]@users.noreply.github.com'; then
  exit 0
elif matches fetch origin "$source_sha"; then
  exit 0
elif matches cherry-pick "$source_sha"; then
  if [[ "${FAKE_GIT_CONFLICT:-}" == 1 ]]; then exit 1; fi
  touch "$GIT_AFTER_PICK"
elif matches cherry-pick --abort; then
  exit 0
elif matches show -s --format=%B HEAD; then
  exit 0
elif matches commit --amend --file - --trailer "Mokka-Source-SHA: $source_sha" --trailer "Mokka-Action-ID: $action_id"; then
  exit 0
elif matches push --porcelain --atomic "--force-with-lease=refs/heads/$head_branch:" origin "HEAD:refs/heads/$head_branch"; then
  [[ "${FAKE_PUSH_RACE:-}" != 1 ]] || exit 1
else
  printf 'unexpected git call shape\n' >&2
  exit 1
fi
`)
	writeMokkaFake(t, filepath.Join(bin, "gh"), `#!/usr/bin/env bash
set -Eeuo pipefail
gh_args=("$@")
record_call() {
  local record="$#"
  for argument in "$@"; do
    encoded="$(printf '%s' "$argument" | base64 | tr -d '\n')"
    printf -v record '%s|%s:%s' "$record" "${#argument}" "$encoded"
  done
  printf '%s\n' "$record" >> "$GH_CALL_LOG"
}
matches() {
  local -a expected=("$@")
  local index
  [[ ${#gh_args[@]} -eq ${#expected[@]} ]] || return 1
  for ((index = 0; index < ${#expected[@]}; index++)); do
    [[ "${gh_args[index]}" == "${expected[index]}" ]] || return 1
  done
}
record_call "${gh_args[@]}"
readonly repository="NVIDIA/k8s-test-infra"
readonly action_id="123e4567-e89b-42d3-a456-426614174000"
readonly source_sha="bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
readonly head_branch="mokka/cherry-pick/$action_id"
readonly created_pr_number="${FAKE_CREATED_PR_NUMBER:-99}"
evidence_payload="$(printf 'action_id: %s\nsource_pull_request: 1\nsource_sha: %s\ntarget_branch: main\ntarget_base_sha: dddddddddddddddddddddddddddddddddddddddd\nproduced_head_sha: cccccccccccccccccccccccccccccccccccccccc\nworkflow_commit_sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nhead_branch: %s\npull_request_url: https://github.com/NVIDIA/k8s-test-infra/pull/%s' "$action_id" "$source_sha" "$head_branch" "$created_pr_number")"
evidence_digest="$(printf '%s' "$evidence_payload" | shasum -a 256 | awk '{print $1}')"
readonly evidence=$'<!-- mokka-cherry-pick-evidence/v1\n'"$evidence_payload"$'\nsha256: '"$evidence_digest"$'\n-->\n'
if matches api "/repos/$repository/pulls/1"; then
  case "${FAKE_SOURCE:-}" in
    pull-read-failure) exit 1 ;;
    pull-malformed) printf '{\n' ;;
    pull-root-array) echo '[]' ;;
    pull-rejected-then-accepted-documents)
      printf '%s\n%s\n' '{}' '{"state":"closed","merged":true,"commits":1,"merge_commit_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}'
      ;;
    pull-accepted-then-rejected-documents)
      printf '%s\n%s\n' '{"state":"closed","merged":true,"commits":1,"merge_commit_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}' '{}'
      ;;
    pull-missing-state) echo '{"merged":true,"commits":1,"merge_commit_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}' ;;
    pull-state-wrong-type) echo '{"state":1,"merged":true,"commits":1,"merge_commit_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}' ;;
    pull-commits-missing) echo '{"state":"closed","merged":true,"merge_commit_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}' ;;
    pull-commits-null) echo '{"state":"closed","merged":true,"commits":null,"merge_commit_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}' ;;
    pull-commits-string) echo '{"state":"closed","merged":true,"commits":"1","merge_commit_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}' ;;
    pull-commits-zero) echo '{"state":"closed","merged":true,"commits":0,"merge_commit_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}' ;;
    pull-commits-fractional) echo '{"state":"closed","merged":true,"commits":1.5,"merge_commit_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}' ;;
    pull-commits-two) echo '{"state":"closed","merged":true,"commits":2,"merge_commit_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}' ;;
    open) echo '{"state":"open","merged":true,"commits":1,"merge_commit_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}' ;;
    unmerged-with-merged-at) echo '{"state":"closed","merged":false,"commits":1,"merged_at":"2026-09-13T00:00:00Z","merge_commit_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}' ;;
    wrong-sha) echo '{"state":"closed","merged":true,"commits":1,"merge_commit_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}' ;;
    fork-head) echo '{"state":"closed","merged":true,"commits":1,"merge_commit_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head":{"repo":{"full_name":"fork/source"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}' ;;
    fork-base) echo '{"state":"closed","merged":true,"commits":1,"merge_commit_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"fork/base"}}}' ;;
    *) echo '{"state":"closed","merged":true,"commits":1,"merge_commit_sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}' ;;
  esac
elif matches api "/repos/$repository/commits/$source_sha"; then
  case "${FAKE_SOURCE:-}" in
    commit-read-failure) exit 1 ;;
    commit-malformed) printf '{\n' ;;
    commit-root-array) echo '[]' ;;
    commit-rejected-then-accepted-documents)
      printf '%s\n%s\n' '{}' '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":[{"sha":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}]}'
      ;;
    commit-accepted-then-rejected-documents)
      printf '%s\n%s\n' '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":[{"sha":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}]}' '{}'
      ;;
    pull-commits-two) echo '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":[{"sha":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}]}' ;;
    commit-missing-sha) echo '{"parents":[{"sha":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}]}' ;;
    commit-wrong-sha) echo '{"sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","parents":[{"sha":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}]}' ;;
    commit-sha-number) echo '{"sha":1,"parents":[{"sha":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}]}' ;;
    commit-missing-parents) echo '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}' ;;
    commit-parents-null) echo '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":null}' ;;
    commit-parents-object) echo '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":{}}' ;;
    zero-parents) echo '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":[]}' ;;
    two-parents) echo '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":[{"sha":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"},{"sha":"ffffffffffffffffffffffffffffffffffffffff"}]}' ;;
    parent-null) echo '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":[null]}' ;;
    parent-string) echo '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":["eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"]}' ;;
    parent-missing-sha) echo '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":[{}]}' ;;
    parent-sha-null) echo '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":[{"sha":null}]}' ;;
    parent-sha-number) echo '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":[{"sha":1}]}' ;;
    parent-sha-uppercase) echo '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":[{"sha":"EEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEEE"}]}' ;;
    parent-sha-short) echo '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":[{"sha":"eeee"}]}' ;;
    parent-sha-trailing-newline) echo '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":[{"sha":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\n"}]}' ;;
    *) echo '{"sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","parents":[{"sha":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}]}' ;;
  esac
elif matches api --include "/repos/$repository/git/ref/heads/$head_branch"; then
  case "${FAKE_BRANCH:-}" in
    exists) echo '{}' ;;
    forbidden) echo 'HTTP/2.0 403 Forbidden' >&2; exit 1 ;;
    rate-limited) echo 'HTTP/2.0 429 Too Many Requests' >&2; exit 1 ;;
    failure) echo 'HTTP/2.0 500 Internal Server Error' >&2; exit 1 ;;
    embedded) echo 'HTTP/2.0 500 Internal Server Error: unexpected HTTP 404 text' >&2; exit 1 ;;
    *) printf 'HTTP/2.0 404 Not Found\ncontent-type: application/json\n\n{}\n' >&2; exit 1 ;;
  esac
elif matches api "/repos/$repository/pulls?state=all&per_page=1&head=NVIDIA:$head_branch&base=main"; then
  case "${FAKE_PULL:-}" in
    exists) echo '[{"number":98}]' ;;
    read-failure) exit 1 ;;
    malformed) printf '{\n' ;;
    wrong-root-type) echo '{}' ;;
    rejected-then-accepted-documents) printf '%s\n%s\n' '{}' '[]' ;;
    accepted-then-rejected-documents) printf '%s\n%s\n' '[]' '{}' ;;
    *) echo '[]' ;;
  esac
elif matches api --method POST "/repos/$repository/pulls" --raw-field "title=Mokka: cherry-pick #1 to main" --raw-field "head=$head_branch" --raw-field "base=main" -F "draft=true" --raw-field "body=<!-- mokka-cherry-pick-action-id: $action_id -->"; then
  case "${FAKE_CREATED_PR:-}" in
    failure) exit 1 ;;
    malformed) printf '{\n' ;;
    root-array) echo '[]' ;;
    rejected-then-accepted-documents)
      printf '{}\n'
      printf '{"number":%s,"html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/%s","draft":true,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}\n' "$created_pr_number" "$created_pr_number"
      ;;
    accepted-then-rejected-documents)
      printf '{"number":%s,"html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/%s","draft":true,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}\n' "$created_pr_number" "$created_pr_number"
      printf '{}\n'
      ;;
    missing-number) echo '{"html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/99","draft":true,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}' ;;
    string-number) echo '{"number":"99","html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/99","draft":true,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}' ;;
    zero-number) echo '{"number":0,"html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/0","draft":true,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}' ;;
    negative-number) echo '{"number":-1,"html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/-1","draft":true,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}' ;;
    floating-number) echo '{"number":99.5,"html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/99.5","draft":true,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}' ;;
    null-number) echo '{"number":null,"html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/null","draft":true,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}' ;;
    boolean-number) echo '{"number":true,"html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/true","draft":true,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}' ;;
    overflow-number) echo '{"number":2147483648,"html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/2147483648","draft":true,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}' ;;
    missing-url) echo '{"number":99,"draft":true,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}' ;;
    url-number) echo '{"number":99,"html_url":99,"draft":true,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}' ;;
    false-draft) echo '{"number":99,"html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/99","draft":false,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}' ;;
    wrong-head) echo '{"number":99,"html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/99","draft":true,"head":{"ref":"other","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}' ;;
    wrong-head-sha) echo '{"number":99,"html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/99","draft":true,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"dddddddddddddddddddddddddddddddddddddddd"},"base":{"ref":"main"}}' ;;
    wrong-base) echo '{"number":99,"html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/99","draft":true,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"other"}}' ;;
    *) printf '{"number":%s,"html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/%s","draft":true,"head":{"ref":"mokka/cherry-pick/123e4567-e89b-42d3-a456-426614174000","sha":"cccccccccccccccccccccccccccccccccccccccc"},"base":{"ref":"main"}}\n' "$created_pr_number" "$created_pr_number" ;;
  esac
elif matches api --method PATCH "/repos/$repository/pulls/$created_pr_number" --raw-field "body=$evidence"; then
  [[ "${FAKE_PATCH_FAILURE:-}" != 1 ]] || exit 1
else
  printf 'unexpected gh call shape\n' >&2
  exit 1
fi
`)

	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"GITHUB_EVENT_PATH="+eventPath,
		"GITHUB_REPOSITORY="+mokkaRepository,
		"GITHUB_REPOSITORY_ID="+mokkaRepositoryID,
		"GITHUB_WORKFLOW_SHA="+mokkaWorkflowSHA,
		"GIT_LOG="+gitLog,
		"GH_CALL_LOG="+ghCallLog,
		"GIT_AFTER_PICK="+filepath.Join(dir, "after-pick"),
	)
	for key, value := range extraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	gitBytes, gitReadErr := os.ReadFile(gitLog)
	if os.IsNotExist(gitReadErr) {
		gitBytes = nil
	} else {
		require.NoError(t, gitReadErr)
	}
	ghCallBytes, ghCallReadErr := os.ReadFile(ghCallLog)
	if os.IsNotExist(ghCallReadErr) {
		ghCallBytes = nil
	} else {
		require.NoError(t, ghCallReadErr)
	}
	return mokkaRunResult{err: err, output: string(output), gitLog: string(gitBytes), ghCallLog: string(ghCallBytes)}
}

type mokkaRealRepository struct {
	target    string
	remote    string
	sourceSHA string
}

func newMokkaRealRepository(t *testing.T, conflict bool) mokkaRealRepository {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "target")
	runMokkaGit(t, root, "init", "--bare", remote)
	runMokkaGit(t, root, "init", source)
	runMokkaGit(t, source, "config", "user.name", "source author")
	runMokkaGit(t, source, "config", "user.email", "source@example.invalid")
	require.NoError(t, os.WriteFile(filepath.Join(source, "conflict.txt"), []byte("base\n"), 0o600))
	runMokkaGit(t, source, "add", "conflict.txt")
	runMokkaGit(t, source, "commit", "-m", "base")
	runMokkaGit(t, source, "branch", "-M", "main")
	runMokkaGit(t, source, "remote", "add", "origin", remote)
	runMokkaGit(t, source, "push", "origin", "main")
	runMokkaGit(t, root, "clone", "--branch", "main", remote, target)

	if conflict {
		require.NoError(t, os.WriteFile(filepath.Join(target, "conflict.txt"), []byte("target\n"), 0o600))
		runMokkaGit(t, target, "add", "conflict.txt")
		runMokkaGit(t, target, "-c", "user.name=target author", "-c", "user.email=target@example.invalid", "commit", "-m", "target change")
		require.NoError(t, os.WriteFile(filepath.Join(source, "conflict.txt"), []byte("source\n"), 0o600))
		runMokkaGit(t, source, "add", "conflict.txt")
	} else {
		require.NoError(t, os.WriteFile(filepath.Join(source, "source.txt"), []byte("source\n"), 0o600))
		runMokkaGit(t, source, "add", "source.txt")
	}
	sourceMessage := "source change"
	if !conflict {
		sourceMessage += "\n\nMokka-Source-SHA: ffffffffffffffffffffffffffffffffffffffff\nMokka-Action-ID: 00000000-0000-4000-8000-000000000000\nmokka-source-sha: eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee\nMOKKA-ACTION-ID: 11111111-1111-4111-8111-111111111111"
	}
	runMokkaGit(t, source, "commit", "-m", sourceMessage)
	sourceSHA := strings.TrimSpace(runMokkaGit(t, source, "rev-parse", "HEAD"))
	runMokkaGit(t, source, "push", "origin", "HEAD:source")
	return mokkaRealRepository{target: target, remote: remote, sourceSHA: sourceSHA}
}

func runMokkaRealDriver(t *testing.T, script string, repository mokkaRealRepository) mokkaRunResult {
	t.Helper()
	return runMokkaRealDriverWithEnv(t, script, repository, nil)
}

func runMokkaRealDriverWithEnv(t *testing.T, script string, repository mokkaRealRepository, extraEnv map[string]string) mokkaRunResult {
	t.Helper()
	dir := t.TempDir()
	eventPath := filepath.Join(dir, "event.json")
	inputs := validMokkaInputs()
	inputs["source_sha"] = repository.sourceSHA
	payload, err := json.Marshal(map[string]any{"inputs": inputs})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(eventPath, payload, 0o600))
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.Mkdir(bin, 0o755))
	ghCallLog := filepath.Join(dir, "gh-calls.log")
	writeMokkaFake(t, filepath.Join(bin, "gh"), `#!/usr/bin/env bash
set -Eeuo pipefail
gh_args=("$@")
record_call() {
  local record="$#"
  for argument in "$@"; do
    encoded="$(printf '%s' "$argument" | base64 | tr -d '\n')"
    printf -v record '%s|%s:%s' "$record" "${#argument}" "$encoded"
  done
  printf '%s\n' "$record" >> "$GH_CALL_LOG"
}
matches() {
  local -a expected=("$@")
  local index
  [[ ${#gh_args[@]} -eq ${#expected[@]} ]] || return 1
  for ((index = 0; index < ${#expected[@]}; index++)); do
    [[ "${gh_args[index]}" == "${expected[index]}" ]] || return 1
  done
}
record_call "${gh_args[@]}"
readonly repository="NVIDIA/k8s-test-infra"
readonly action_id="$MOKKA_REAL_ACTION_ID"
readonly source_sha="$MOKKA_REAL_SOURCE_SHA"
readonly head_branch="mokka/cherry-pick/$action_id"
evidence_payload="$(printf 'action_id: %s\nsource_pull_request: 1\nsource_sha: %s\ntarget_branch: main\ntarget_base_sha: %s\nproduced_head_sha: %s\nworkflow_commit_sha: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nhead_branch: %s\npull_request_url: https://github.com/NVIDIA/k8s-test-infra/pull/99' "$action_id" "$source_sha" "$MOKKA_REAL_TARGET_BASE_SHA" "$(git -C "$MOKKA_REAL_TARGET" rev-parse HEAD)" "$head_branch")"
evidence_digest="$(printf '%s' "$evidence_payload" | sha256sum | awk '{print $1}')"
readonly evidence=$'<!-- mokka-cherry-pick-evidence/v1\n'"$evidence_payload"$'\nsha256: '"$evidence_digest"$'\n-->\n'
if matches api "/repos/$repository/pulls/1"; then
  printf '{"state":"closed","merged":true,"commits":1,"merge_commit_sha":"%s","head":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}},"base":{"repo":{"full_name":"NVIDIA/k8s-test-infra"}}}\n' "$source_sha"
elif matches api "/repos/$repository/commits/$source_sha"; then
  printf '{"sha":"%s","parents":[{"sha":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}]}\n' "$source_sha"
elif matches api --include "/repos/$repository/git/ref/heads/$head_branch"; then
  if [[ "${MOKKA_REAL_PUSH_RACE:-}" == "1" ]]; then
    git --git-dir "$MOKKA_REAL_REMOTE" update-ref "refs/heads/$head_branch" "$MOKKA_REAL_COMPETING_SHA"
  fi
  printf 'HTTP/2.0 404 Not Found\ncontent-type: application/json\n\n{}\n' >&2
  exit 1
elif matches api "/repos/$repository/pulls?state=all&per_page=1&head=NVIDIA:$head_branch&base=main"; then
  printf '[]\n'
elif matches api --method POST "/repos/$repository/pulls" --raw-field "title=Mokka: cherry-pick #1 to main" --raw-field "head=$head_branch" --raw-field "base=main" -F "draft=true" --raw-field "body=<!-- mokka-cherry-pick-action-id: $action_id -->"; then
  head_sha="$(git -C "$MOKKA_REAL_TARGET" rev-parse HEAD)"
  printf '{"number":99,"html_url":"https://github.com/NVIDIA/k8s-test-infra/pull/99","draft":true,"head":{"ref":"%s","sha":"%s"},"base":{"ref":"main"}}\n' "$head_branch" "$head_sha"
elif matches api --method PATCH "/repos/$repository/pulls/99" --raw-field "body=$evidence"; then
  exit 0
else
  printf 'unexpected gh call shape\n' >&2
  exit 1
fi
`)
	home := filepath.Join(dir, "home")
	require.NoError(t, os.Mkdir(home, 0o700))
	targetBaseSHA := strings.TrimSpace(runMokkaGit(t, repository.target, "rev-parse", "HEAD"))
	cmd := exec.Command("bash", script)
	cmd.Dir = repository.target
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GITHUB_EVENT_PATH="+eventPath,
		"GITHUB_REPOSITORY="+mokkaRepository,
		"GITHUB_REPOSITORY_ID="+mokkaRepositoryID,
		"GITHUB_WORKFLOW_SHA="+mokkaWorkflowSHA,
		"GH_CALL_LOG="+ghCallLog,
		"MOKKA_REAL_SOURCE_SHA="+repository.sourceSHA,
		"MOKKA_REAL_TARGET="+repository.target,
		"MOKKA_REAL_TARGET_BASE_SHA="+targetBaseSHA,
		"MOKKA_REAL_REMOTE="+repository.remote,
		"MOKKA_REAL_COMPETING_SHA="+targetBaseSHA,
		"MOKKA_REAL_ACTION_ID="+mokkaActionID,
	)
	for key, value := range extraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	output, err := cmd.CombinedOutput()
	ghCallBytes, ghCallReadErr := os.ReadFile(ghCallLog)
	if os.IsNotExist(ghCallReadErr) {
		ghCallBytes = nil
	} else {
		require.NoError(t, ghCallReadErr)
	}
	return mokkaRunResult{err: err, output: string(output), ghCallLog: string(ghCallBytes)}
}

func runMokkaGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	require.NoErrorf(t, err, "git %s failed:\n%s", strings.Join(arguments, " "), output)
	return string(output)
}

func writeMokkaFake(t *testing.T, path, contents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o755))
}

func mokkaWorkflowPrecheck(t *testing.T, workflow string) string {
	t.Helper()
	contents, err := os.ReadFile(workflow)
	require.NoError(t, err)
	var parsed struct {
		Jobs map[string]struct {
			Steps []struct {
				Name string `json:"name"`
				Run  string `json:"run"`
			} `json:"steps"`
		} `json:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(contents, &parsed))
	for _, job := range parsed.Jobs {
		for _, step := range job.Steps {
			if step.Name == "Validate dispatch envelope" {
				return step.Run
			}
		}
	}
	t.Fatal("workflow has no pre-checkout dispatch-envelope validation step")
	return ""
}

func runMokkaPrecheck(t *testing.T, precheck string, inputs map[string]any, extraEnv map[string]string) error {
	t.Helper()
	return runMokkaPrecheckWithEventPayload(t, precheck, mokkaEventPayload(t, inputs), extraEnv)
}

func runMokkaPrecheckWithEventPayload(t *testing.T, precheck string, payload []byte, extraEnv map[string]string) error {
	t.Helper()
	dir := t.TempDir()
	eventPath := filepath.Join(dir, "event.json")
	require.NoError(t, os.WriteFile(eventPath, payload, 0o600))
	cmd := exec.Command("bash", "-c", precheck)
	cmd.Env = append(os.Environ(),
		"GITHUB_EVENT_PATH="+eventPath,
		"GITHUB_REPOSITORY="+mokkaRepository,
		"GITHUB_REPOSITORY_ID="+mokkaRepositoryID,
		"GITHUB_WORKFLOW_SHA="+mokkaWorkflowSHA,
	)
	for key, value := range extraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	return cmd.Run()
}
