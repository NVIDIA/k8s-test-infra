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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestMokkaCherryPickNodeWorkflowContract(t *testing.T) {
	root := repoRoot(t)
	workflowPath := filepath.Join(root, ".github", "workflows", "mokka-cherry-pick.yml")
	actionPath := filepath.Join(root, ".github", "actions", "repo-automation", "action.yml")
	modePath := filepath.Join(root, ".github", "actions", "repo-automation", "src", "modes", "mokka-cherry-pick.js")
	evidencePath := filepath.Join(root, ".github", "actions", "repo-automation", "src", "mokka-evidence.js")

	workflow, err := os.ReadFile(workflowPath)
	require.NoError(t, err)
	action, err := os.ReadFile(actionPath)
	require.NoError(t, err)
	mode, err := os.ReadFile(modePath)
	require.NoError(t, err)
	evidence, err := os.ReadFile(evidencePath)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal(workflow, &parsed))
	require.NotContains(t, parsed, "env", "the workflow must not expose the token to all steps")
	require.Equal(t, map[string]any{}, parsed["permissions"])
	triggers, ok := parsed["on"].(map[string]any)
	require.True(t, ok)
	require.Len(t, triggers, 1)
	dispatch, ok := triggers["workflow_dispatch"].(map[string]any)
	require.True(t, ok)
	inputs, ok := dispatch["inputs"].(map[string]any)
	require.True(t, ok)
	require.ElementsMatch(t, []string{"action_id", "pull_request_number", "source_sha", "target_branch", "workflow_commit_sha"}, mapKeys(inputs))
	require.Len(t, inputs, 5, "Mokka must keep the exact five-input dispatch contract")
	for name, rawInput := range inputs {
		input, ok := rawInput.(map[string]any)
		require.True(t, ok, "input %s must be a mapping", name)
		require.Equal(t, true, input["required"], "input %s must be required", name)
		require.Equal(t, "string", input["type"], "input %s must be a string", name)
	}

	jobs, ok := parsed["jobs"].(map[string]any)
	require.True(t, ok)
	require.Len(t, jobs, 1)
	job, ok := jobs["cherry-pick"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "${{ vars.REPOSITORY_AUTOMATION_MOKKA_ENABLED == 'true' && github.ref == 'refs/heads/main' && github.sha == inputs.workflow_commit_sha }}", job["if"])
	require.Equal(t, map[string]any{"contents": "write", "pull-requests": "write"}, job["permissions"])
	steps, ok := job["steps"].([]any)
	require.True(t, ok)
	require.Len(t, steps, 6, "the workflow must verify reviewed automation before it checks out the target")

	validateRef, ok := steps[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Validate reviewed automation ref", validateRef["name"])
	require.Equal(t, "bash", validateRef["shell"])
	require.Equal(t, map[string]any{"REVIEWED_SHA": "${{ vars.REPOSITORY_AUTOMATION_MOKKA_REVIEWED_SHA }}"}, validateRef["env"])
	require.Contains(t, validateRef["run"], "^[0-9a-f]{40}$")

	trustedCheckout, ok := steps[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Check out trusted automation", trustedCheckout["name"])
	require.Equal(t, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", trustedCheckout["uses"])
	require.Equal(t, map[string]any{
		"fetch-depth":         float64(1),
		"lfs":                 false,
		"path":                "control",
		"persist-credentials": false,
		"ref":                 "${{ github.sha }}",
		"submodules":          false,
	}, trustedCheckout["with"])

	reviewedCheckout, ok := steps[2].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Check out reviewed automation", reviewedCheckout["name"])
	require.Equal(t, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", reviewedCheckout["uses"])
	require.Equal(t, map[string]any{
		"fetch-depth":         float64(1),
		"lfs":                 false,
		"path":                "reviewed",
		"persist-credentials": false,
		"ref":                 "${{ vars.REPOSITORY_AUTOMATION_MOKKA_REVIEWED_SHA }}",
		"submodules":          false,
	}, reviewedCheckout["with"])

	verifyAutomation, ok := steps[3].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Verify reviewed automation", verifyAutomation["name"])
	require.Equal(t, "bash", verifyAutomation["shell"])
	for _, path := range []string{
		".github/workflows/mokka-cherry-pick.yml",
		".github/actions/repo-automation/action.yml",
		".github/actions/repo-automation/dist/index.js",
	} {
		require.Contains(t, verifyAutomation["run"], path)
	}
	require.Contains(t, verifyAutomation["run"], "cmp")

	targetCheckout, ok := steps[4].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Check out validated target", targetCheckout["name"])
	require.Equal(t, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", targetCheckout["uses"])
	require.Equal(t, map[string]any{
		"fetch-depth":         float64(1),
		"lfs":                 false,
		"path":                "target",
		"persist-credentials": true,
		"ref":                 "${{ inputs.target_branch }}",
		"submodules":          false,
	}, targetCheckout["with"])

	driver, ok := steps[5].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Cherry-pick merged source pull request", driver["name"])
	require.Equal(t, "./control/.github/actions/repo-automation", driver["uses"])
	require.NotContains(t, driver, "run", "the Node action must replace the shell driver")
	require.Equal(t, map[string]any{"GITHUB_TOKEN": "${{ github.token }}"}, driver["env"])
	require.Equal(t, map[string]any{
		"action_id":           "${{ inputs.action_id }}",
		"dry-run":             "false",
		"mode":                "mokka-cherry-pick",
		"pull_request_number": "${{ inputs.pull_request_number }}",
		"source_sha":          "${{ inputs.source_sha }}",
		"target-branch":       "${{ inputs.target_branch }}",
		"working-directory":   "target",
	}, driver["with"])

	workflowText := string(workflow)
	require.NotContains(t, workflowText, ".github/scripts/mokka-cherry-pick.sh")
	require.NotContains(t, workflowText, "sha256sum")
	require.NotContains(t, workflowText, "hash-object")
	require.NotContains(t, workflowText, "exec /usr/bin/bash")
	require.Contains(t, string(action), "source_sha:")
	require.Contains(t, string(action), "action_id:")
	require.Contains(t, string(action), "working-directory:")

	contract := string(mode) + "\n" + string(evidence)
	for _, required := range []string{
		"NVIDIA/k8s-test-infra", "733665780", "2147483647", "mokka/cherry-pick/",
		"Mokka: cherry-pick #", "mokka-cherry-pick-action-id", "mokka-cherry-pick-evidence/v1",
		"source_pull_request", "source_sha", "target_branch", "target_base_sha",
		"produced_head_sha", "workflow_commit_sha", "head_branch", "pull_request_url", "sha256",
		"force-with-lease", "cherry-pick", "--abort", "draft", "merged", "parents",
	} {
		require.Contains(t, contract, required, "Node contract must preserve %q", required)
	}
	for _, forbidden := range []string{"pulls.merge", "submitReview", "refs/tags/", "rulesets"} {
		require.NotContains(t, contract, forbidden)
	}
}

func TestMokkaCherryPickHasNoShellDriver(t *testing.T) {
	root := repoRoot(t)
	require.NoFileExists(t, filepath.Join(root, ".github", "scripts", "mokka-cherry-pick.sh"))

	index, err := os.ReadFile(filepath.Join(root, ".github", "actions", "repo-automation", "src", "index.js"))
	require.NoError(t, err)
	gitAdapter, err := os.ReadFile(filepath.Join(root, ".github", "actions", "repo-automation", "src", "git.js"))
	require.NoError(t, err)

	require.Contains(t, string(index), "mokka-cherry-pick")
	require.Contains(t, string(index), "GITHUB_REPOSITORY_ID")
	require.Contains(t, string(index), "GITHUB_WORKFLOW_SHA")
	require.Contains(t, string(gitAdapter), "execFile")
	require.Contains(t, string(gitAdapter), "shell: false")
	require.NotContains(t, string(index), "exec(")
	require.NotContains(t, string(gitAdapter), "exec(")
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func TestMokkaCherryPickSourcesDoNotReferenceRemovedDriver(t *testing.T) {
	root := repoRoot(t)
	for _, relativePath := range []string{
		filepath.Join(".github", "actions", "repo-automation", "action.yml"),
		filepath.Join(".github", "actions", "repo-automation", "src", "index.js"),
		filepath.Join(".github", "actions", "repo-automation", "src", "modes", "mokka-cherry-pick.js"),
		filepath.Join(".github", "workflows", "mokka-cherry-pick.yml"),
	} {
		contents, err := os.ReadFile(filepath.Join(root, relativePath))
		require.NoError(t, err)
		require.NotContains(t, string(contents), "mokka-cherry-pick.sh", relativePath)
	}
}
