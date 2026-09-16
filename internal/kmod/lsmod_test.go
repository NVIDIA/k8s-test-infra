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
	require.Contains(t, lines, "nvidia              62312448  4 gdrdrv,nvidia_fs,nvidia_modeset,nvidia_uvm")
	require.Contains(t, lines, "nvidia_uvm           3411968  0",
		"a holder-less module ends after the count, as kmod's lsmod does")
}

// The greps the GPU Operator validator runs against lsmod, verbatim. A change
// to the script's column widths would still parse, and still break them.
func TestLsmodScript_SatisfiesTheValidatorGreps(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	mods := Modules("550.163.01", true)
	mustRender(t, Options{Modules: mods, OverlayRoot: out})
	lsmod := runLsmodScript(t, out)

	// The greps for nvidia_fs and mlx5_core carry no anchor, so they also match
	// nvidia's Used-by column. Pin a row per module first, or a module could
	// lose its line and still satisfy every grep below.
	for _, mod := range mods {
		require.Regexp(t, `(?m)^`+mod.Name+`\s`, lsmod, "row for %s", mod.Name)
	}

	for _, grep := range []struct{ module, pattern string }{
		{GDRDrv, `(?m)^gdrdrv\s`},
		{NVIDIAPeermem, `(?m)^nvidia_peermem\s`},
		{NVIDIAFS, `nvidia_fs`},
		{MLX5Core, `mlx5_core`},
	} {
		require.Regexp(t, grep.pattern, lsmod, "the validator's grep for %s must match", grep.module)
	}
}

func TestLsmodScript_ReportsMirroredHostModules(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, "xfs", map[string]string{"refcnt": "2\n", "coresize": "1556480\n"})

	out := t.TempDir()
	mustRender(t, Options{
		Modules:     Modules("550.163.01", false),
		SourceRoot:  src,
		OverlayRoot: out,
	})

	require.Contains(t, runLsmodScript(t, out), "xfs                  1556480  2")
}

func TestLsmodScript_ReportsUnavailableRefcountAsNegativeTwo(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, "nounload", map[string]string{"coresize": "4096\n"})

	out := t.TempDir()
	mustRender(t, Options{SourceRoot: src, OverlayRoot: out})

	require.Regexp(t, `(?m)^nounload\s+4096\s+-2\s*$`, runLsmodScript(t, out))
}

func TestLsmodScript_SkipsEntriesWithoutCoresize(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, "built_in", map[string]string{"uevent": ""})

	out := t.TempDir()
	mustRender(t, Options{
		Modules:     Modules("550.163.01", false),
		SourceRoot:  src,
		OverlayRoot: out,
	})
	require.DirExists(t, filepath.Join(out, SysModuleRelPath, "built_in"))

	require.NotContains(t, runLsmodScript(t, out), "built_in")
}

func renderedTree(t *testing.T) string {
	t.Helper()

	out := t.TempDir()
	mustRender(t, Options{Modules: Modules("550.163.01", false), OverlayRoot: out})
	return out
}
