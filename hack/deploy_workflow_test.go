package hack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDeployWorkflowRestoresIssuesPrsJSON asserts that the deploy.yaml
// workflow contains a step that restores the previous issues_prs.json
// from the live site. PR-B's runIssuesPRsPhase cache fallback depends
// on this step actually running.
//
// Mutation check: removing the restore line from deploy.yaml causes
// this assertion to fail. We grep for the canonical URL and the
// fallback echo so a partial revert is also caught.
func TestDeployWorkflowRestoresIssuesPrsJSON(t *testing.T) {
	t.Parallel()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	workflowPath := filepath.Join(repoRoot, ".github", "workflows", "deploy.yaml")

	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read %s: %v", workflowPath, err)
	}
	content := string(data)

	requirements := []string{
		`"https://nvidia.github.io/k8s-test-infra/data/issues_prs.json"`,
		`mv /tmp/issues_prs.json ./public/data/issues_prs.json`,
		`echo '{"repos":{}}' > ./public/data/issues_prs.json`,
	}
	for _, want := range requirements {
		if !strings.Contains(content, want) {
			t.Fatalf("deploy.yaml missing required token: %q", want)
		}
	}

	// Negative assertion: should NOT have a schedule trigger (Q7
	// guardrail; checked here too as a layered defense in case the
	// dedicated lint workflow is somehow disabled).
	if strings.Contains(content, "schedule:") {
		// Allow if marker is present.
		if !strings.Contains(content, "ADR-allowed-schedule:") {
			t.Fatalf("deploy.yaml has schedule: without ADR marker; see hack/lint_deploy_schedule.sh")
		}
	}
}
