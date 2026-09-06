// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kmod

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func runLsmodScript(t *testing.T, treeRoot string) string {
	t.Helper()

	script := strings.ReplaceAll(LsmodScript, "/sys/module/", filepath.Join(treeRoot, SysModuleRelPath)+"/")
	path := filepath.Join(t.TempDir(), "lsmod")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))

	out, err := exec.Command("sh", path).CombinedOutput()
	require.NoError(t, err, "script failed: %s", out)
	return string(out)
}

func TestLsmodScript_PrintsTheKmodTable(t *testing.T) {
	t.Parallel()

	out := runLsmodScript(t, renderedTree(t))

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Equal(t, "Module                  Size  Used by", lines[0])
	require.Contains(t, lines, "nvidia              62312448  1 nvidia_uvm")
	require.Contains(t, lines, "nvidia_uvm           3411968  0",
		"a holder-less module ends after the count, as kmod's lsmod does")
}

func TestLsmodScript_ReportsMirroredHostModules(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, "xfs", map[string]string{"refcnt": "2\n", "coresize": "1556480\n"})

	out := t.TempDir()
	mustRender(t, Options{
		Modules:    Modules("550.163.01"),
		SourceRoot: src,
		Output:     out,
	})

	require.Contains(t, runLsmodScript(t, out), "xfs                  1556480  2")
}

func TestLsmodScript_ReportsUnavailableRefcountAsNegativeTwo(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, "nounload", map[string]string{"coresize": "4096\n"})

	out := t.TempDir()
	mustRender(t, Options{SourceRoot: src, Output: out})

	require.Regexp(t, `(?m)^nounload\s+4096\s+-2\s*$`, runLsmodScript(t, out))
}

func TestLsmodScript_SkipsEntriesWithoutCoresize(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, "built_in", map[string]string{"uevent": ""})

	out := t.TempDir()
	mustRender(t, Options{
		Modules:    Modules("550.163.01"),
		SourceRoot: src,
		Output:     out,
	})
	require.DirExists(t, filepath.Join(out, SysModuleRelPath, "built_in"))

	require.NotContains(t, runLsmodScript(t, out), "built_in")
}

func renderedTree(t *testing.T) string {
	t.Helper()

	out := t.TempDir()
	mustRender(t, Options{Modules: Modules("550.163.01"), Output: out})
	return out
}
