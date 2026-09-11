// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package kmod

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func mustRender(t *testing.T, o Options) []string {
	t.Helper()

	skipped, err := Render(o)
	require.NoError(t, err)
	return skipped
}

func writeHostModule(t *testing.T, root, name string, attrs map[string]string, holders ...string) {
	t.Helper()

	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "holders"), 0o755))

	for attr, content := range attrs {
		require.NoError(t, os.WriteFile(filepath.Join(dir, attr), []byte(content), 0o644))
	}
	for _, h := range holders {
		require.NoError(t, os.Symlink(filepath.Join("..", "..", h), filepath.Join(dir, "holders", h)))
	}
}

func TestRender_WritesTheSimulatedModules(t *testing.T) {
	t.Parallel()

	out := t.TempDir()

	mustRender(t, Options{Modules: Modules("550.163.01"), Output: out})

	nvidia := filepath.Join(out, SysModuleRelPath, NVIDIA)
	requireFileContent(t, filepath.Join(nvidia, "refcnt"), "1\n")
	requireFileContent(t, filepath.Join(nvidia, "coresize"), "62312448\n")
	requireFileContent(t, filepath.Join(nvidia, "initstate"), "live\n")
	requireFileContent(t, filepath.Join(nvidia, "version"), "550.163.01\n")

	target, err := os.Readlink(filepath.Join(nvidia, "holders", NVIDIAUVM))
	require.NoError(t, err)
	require.Equal(t, filepath.Join("..", "..", NVIDIAUVM), target)

	uvm := filepath.Join(out, SysModuleRelPath, NVIDIAUVM)
	requireFileContent(t, filepath.Join(uvm, "refcnt"), "0\n")
	require.NoFileExists(t, filepath.Join(uvm, "version"))
	require.DirExists(t, filepath.Join(uvm, "holders"))
}

func TestRender_MirrorsLoadedHostModules(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, "xfs", map[string]string{
		"refcnt": "2\n", "coresize": "1556480\n", "initstate": "live\n",
	})

	out := t.TempDir()
	mustRender(t, Options{
		Modules:    Modules("550.163.01"),
		SourceRoot: src,
		Output:     out,
	})

	requireFileContent(t, filepath.Join(out, SysModuleRelPath, "xfs", "refcnt"), "2\n")
	requireFileContent(t, filepath.Join(out, SysModuleRelPath, "xfs", "coresize"), "1556480\n")
}

func TestRender_PreservesARealNVIDIAModule(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, NVIDIA, map[string]string{
		"coresize": "999\n",
		"refcnt":   "7\n",
		"version":  "570.86.15\n",
	})
	params := filepath.Join(src, NVIDIA, "parameters")
	require.NoError(t, os.MkdirAll(params, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(params, "NVreg_EnableGpuFirmware"), []byte("1\n"), 0o644))

	out := t.TempDir()
	mustRender(t, Options{
		Modules:         Modules("550.163.01"),
		SourceRoot:      src,
		Output:          out,
		HostProcModules: "nvidia 999 7 nvidia_modeset, Live 0x0000000000000000\n",
	})

	nvidia := filepath.Join(out, SysModuleRelPath, NVIDIA)
	requireFileContent(t, filepath.Join(nvidia, "coresize"), "999\n")
	requireFileContent(t, filepath.Join(nvidia, "refcnt"), "7\n")
	requireFileContent(t, filepath.Join(nvidia, "version"), "570.86.15\n")
	requireFileContent(t, filepath.Join(nvidia, "parameters", "NVreg_EnableGpuFirmware"), "1\n")
}

func TestRender_ReplacesADepartedRealNVIDIAModuleWithTheFallback(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, NVIDIA, map[string]string{"coresize": "999\n"})
	params := filepath.Join(src, NVIDIA, "parameters")
	require.NoError(t, os.MkdirAll(params, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(params, "NVreg_EnableGpuFirmware"), []byte("1\n"), 0o644))

	out := t.TempDir()
	opts := Options{Modules: Modules("550.163.01"), SourceRoot: src, Output: out}
	mustRender(t, opts)

	require.NoError(t, os.RemoveAll(filepath.Join(src, NVIDIA)))
	mustRender(t, opts)

	nvidia := filepath.Join(out, SysModuleRelPath, NVIDIA)
	requireFileContent(t, filepath.Join(nvidia, "coresize"), "62312448\n")
	require.NoDirExists(t, filepath.Join(nvidia, "parameters"))
}

func TestRender_PrunesRemovedFallbackHolders(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	mustRender(t, Options{
		Modules: []Module{
			{Name: NVIDIA, Holders: []string{NVIDIAUVM}},
			{Name: NVIDIAUVM},
		},
		Output: out,
	})

	mustRender(t, Options{
		Modules: []Module{{Name: NVIDIA}},
		Output:  out,
	})

	require.NoFileExists(t, filepath.Join(out, SysModuleRelPath, NVIDIA, "holders", NVIDIAUVM))
}

func TestRender_MirrorsBuiltInModulesAndTheirParameters(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, "built_in", map[string]string{"uevent": ""})
	params := filepath.Join(src, "built_in", "parameters")
	require.NoError(t, os.MkdirAll(params, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(params, "nested"), []byte("Y\n"), 0o644))

	out := t.TempDir()
	mustRender(t, Options{SourceRoot: src, Output: out})

	requireFileContent(t, filepath.Join(out, SysModuleRelPath, "built_in", "parameters", "nested"), "Y\n")
	require.NoFileExists(t, filepath.Join(out, SysModuleRelPath, "built_in", "coresize"),
		"only a loadable module has coresize, which is how lsmod tells the two apart")
}

func TestRender_MirrorsOnlyTheAttributesTheHostExposes(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, "nounload", map[string]string{"coresize": "4096\n"})

	out := t.TempDir()
	mustRender(t, Options{SourceRoot: src, Output: out})

	requireFileContent(t, filepath.Join(out, SysModuleRelPath, "nounload", "coresize"), "4096\n")
	require.NoFileExists(t, filepath.Join(out, SysModuleRelPath, "nounload", "refcnt"))
}

func TestRender_SkipsAttributesItCannotRead(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root reads a write-only file, so the premise does not hold")
	}

	src := t.TempDir()
	writeHostModule(t, src, "xfs", map[string]string{"coresize": "4096\n"})
	require.NoError(t, os.WriteFile(filepath.Join(src, "xfs", "writeonly"), []byte("x"), 0o200))
	require.NoError(t, os.Mkdir(filepath.Join(src, "xfs", "unlistable"), 0o000))

	out := t.TempDir()
	skipped := mustRender(t, Options{SourceRoot: src, Output: out})

	requireFileContent(t, filepath.Join(out, SysModuleRelPath, "xfs", "coresize"), "4096\n")
	require.NoFileExists(t, filepath.Join(out, SysModuleRelPath, "xfs", "writeonly"))

	require.Contains(t, skipped, filepath.Join(src, "xfs", "writeonly"),
		"an unreadable attribute must be reported, not dropped in silence")
	require.Contains(t, skipped, filepath.Join(src, "xfs", "unlistable"))
}

func TestRender_CompletesAHostNVIDIADirectoryThatCarriesNoAttributes(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, NVIDIA), 0o755))

	out := t.TempDir()
	mods := Modules("550.163.01")
	mustRender(t, Options{Modules: mods, SourceRoot: src, Output: out})

	served := filepath.Join(out, SysModuleRelPath, NVIDIA)
	requireFileContent(t, filepath.Join(served, "refcnt"), "1\n")
	requireFileContent(t, filepath.Join(served, "coresize"), "62312448\n")
	requireFileContent(t, filepath.Join(served, "version"), "550.163.01\n")
	require.FileExists(t, filepath.Join(served, "initstate"))

	require.Contains(t, ProcModules("", mods), "nvidia 62312448 1 nvidia_uvm,",
		"proc/modules advertises nvidia, so the sysfs entry must answer for it")
}

func TestRender_AgreesWithProcModulesWhenTheHostListsNoLine(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, NVIDIA, map[string]string{"refcnt": "42\n"})

	out := t.TempDir()
	mods := Modules("550.163.01")
	mustRender(t, Options{Modules: mods, SourceRoot: src, Output: out})

	served := filepath.Join(out, SysModuleRelPath, NVIDIA)
	requireFileContent(t, filepath.Join(served, "refcnt"), "1\n")
	requireFileContent(t, filepath.Join(served, "coresize"), "62312448\n")
	require.FileExists(t, filepath.Join(served, "holders", NVIDIAUVM))

	require.Contains(t, ProcModules("", mods), "nvidia 62312448 1 nvidia_uvm,",
		"the sysfs refcnt and holders must match the line proc/modules advertises")
}

func TestRender_RequiresAnOutput(t *testing.T) {
	t.Parallel()

	_, err := Render(Options{Modules: Modules("550.163.01")})
	require.Error(t, err, "an empty Output would render into the working directory")
}

func TestRender_RecoversHostHoldersFromTheProcModulesLine(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, NVIDIA), 0o755))

	out := t.TempDir()
	mustRender(t, Options{
		Modules:         Modules("550.163.01"),
		SourceRoot:      src,
		Output:          out,
		HostProcModules: "nvidia 999 2 nvidia_modeset,nvidia_uvm, Live 0x0000000000000000\n",
	})

	served := filepath.Join(out, SysModuleRelPath, NVIDIA)
	requireFileContent(t, filepath.Join(served, "refcnt"), "2\n")
	require.FileExists(t, filepath.Join(served, "holders", "nvidia_modeset"))
	require.FileExists(t, filepath.Join(served, "holders", NVIDIAUVM))
}

func TestRender_ReconcilesAHostHoldersDirectoryThatLostALink(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, NVIDIA, map[string]string{"coresize": "999\n", "refcnt": "2\n"}, NVIDIAUVM)

	out := t.TempDir()
	mustRender(t, Options{
		Modules:         Modules("550.163.01"),
		SourceRoot:      src,
		Output:          out,
		HostProcModules: "nvidia 999 2 nvidia_modeset,nvidia_uvm, Live 0x0000000000000000\n",
	})

	holders := filepath.Join(out, SysModuleRelPath, NVIDIA, "holders")
	require.FileExists(t, filepath.Join(holders, NVIDIAUVM))
	require.FileExists(t, filepath.Join(holders, "nvidia_modeset"),
		"the proc line names two holders, so the mirror's single link is not the whole set")
}

func TestRender_KeepsMirroredHoldersWhenTheProcLineOmitsDependencies(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, NVIDIA, map[string]string{"coresize": "999\n", "refcnt": "2\n"}, NVIDIAUVM)

	out := t.TempDir()
	mustRender(t, Options{
		Modules:         Modules("550.163.01"),
		SourceRoot:      src,
		Output:          out,
		HostProcModules: "nvidia 999 2\n",
	})

	require.FileExists(t, filepath.Join(out, SysModuleRelPath, NVIDIA, "holders", NVIDIAUVM),
		"an absent dependency field means the holders are unknown, not that there are none")
}

func TestRender_ReconcilesHostHoldersIdempotently(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, NVIDIA, map[string]string{"coresize": "999\n", "refcnt": "2\n"}, NVIDIAUVM)

	opts := Options{
		Modules:         Modules("550.163.01"),
		SourceRoot:      src,
		Output:          t.TempDir(),
		HostProcModules: "nvidia 999 2 nvidia_modeset,nvidia_uvm, Live 0x0000000000000000\n",
	}
	mustRender(t, opts)
	mustRender(t, opts)

	holders := filepath.Join(opts.Output, SysModuleRelPath, NVIDIA, "holders")
	entries, err := os.ReadDir(holders)
	require.NoError(t, err)
	require.Len(t, entries, 2, "a second pass must not duplicate or drop a holder")
}

func TestRender_FillsAHostGapFromTheProcModulesLine(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root reads a write-only file, so the premise does not hold")
	}

	src := t.TempDir()
	writeHostModule(t, src, NVIDIA, map[string]string{"coresize": "999\n"})
	require.NoError(t, os.WriteFile(filepath.Join(src, NVIDIA, "refcnt"), []byte("7\n"), 0o200))

	out := t.TempDir()
	mustRender(t, Options{
		Modules:         Modules("550.163.01"),
		SourceRoot:      src,
		Output:          out,
		HostProcModules: "nvidia 999 7 nvidia_modeset, Live 0x0000000000000000\n",
	})

	served := filepath.Join(out, SysModuleRelPath, NVIDIA)
	requireFileContent(t, filepath.Join(served, "refcnt"), "7\n")
	requireFileContent(t, filepath.Join(served, "coresize"), "999\n")
}

func TestRender_MirrorsHostHoldersAsSymlinks(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, "mlx5_core", map[string]string{"refcnt": "1\n"}, "mlx5_ib")

	out := t.TempDir()
	mustRender(t, Options{SourceRoot: src, Output: out})

	target, err := os.Readlink(filepath.Join(out, SysModuleRelPath, "mlx5_core", "holders", "mlx5_ib"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join("..", "..", "mlx5_ib"), target)
}

func TestRender_PrunesModulesTheSourceNoLongerHas(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, "xfs", map[string]string{"refcnt": "1\n"})
	out := t.TempDir()

	mustRender(t, Options{SourceRoot: src, Output: out})
	require.DirExists(t, filepath.Join(out, SysModuleRelPath, "xfs"))

	require.NoError(t, os.RemoveAll(filepath.Join(src, "xfs")))
	mustRender(t, Options{SourceRoot: src, Output: out})
	require.NoDirExists(t, filepath.Join(out, SysModuleRelPath, "xfs"))
}

func TestRender_PrunesNestedEntriesTheSourceNoLongerHas(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	params := filepath.Join(src, "xfs", "parameters")
	require.NoError(t, os.MkdirAll(params, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(params, "old"), []byte("old\n"), 0o644))

	out := t.TempDir()
	mustRender(t, Options{SourceRoot: src, Output: out})

	require.NoError(t, os.Remove(filepath.Join(params, "old")))
	require.NoError(t, os.WriteFile(filepath.Join(params, "new"), []byte("new\n"), 0o644))
	mustRender(t, Options{SourceRoot: src, Output: out})

	mirrored := filepath.Join(out, SysModuleRelPath, "xfs", "parameters")
	require.NoFileExists(t, filepath.Join(mirrored, "old"))
	requireFileContent(t, filepath.Join(mirrored, "new"), "new\n")
}

func TestRender_PrunesAModuleItCannotMirror(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	writeHostModule(t, src, "xfs", map[string]string{"coresize": "4096\n"})

	out := t.TempDir()
	mustRender(t, Options{SourceRoot: src, Output: out})
	require.DirExists(t, filepath.Join(out, SysModuleRelPath, "xfs"))

	require.NoError(t, os.RemoveAll(filepath.Join(src, "xfs")))
	require.NoError(t, os.Symlink(filepath.Join(src, "gone"), filepath.Join(src, "xfs")))
	skipped := mustRender(t, Options{SourceRoot: src, Output: out})

	require.NoDirExists(t, filepath.Join(out, SysModuleRelPath, "xfs"))
	require.Equal(t, []string{filepath.Join(src, "xfs")}, skipped,
		"a module that vanishes from the served tree must be named")
}

func TestRender_FallsBackWhenAHostNVIDIAModuleVanishesMidPass(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	require.NoError(t, os.Symlink(filepath.Join(src, "gone"), filepath.Join(src, NVIDIA)))

	out := t.TempDir()
	mustRender(t, Options{
		Modules:    Modules("550.163.01"),
		SourceRoot: src,
		Output:     out,
	})

	nvidia := filepath.Join(out, SysModuleRelPath, NVIDIA)
	requireFileContent(t, filepath.Join(nvidia, "refcnt"), "1\n")
	requireFileContent(t, filepath.Join(nvidia, "coresize"), "62312448\n")
}

func TestRender_MirrorsTheHostFileMode(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(src, "xfs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "xfs", "coresize"), []byte("4096\n"), 0o444))

	out := t.TempDir()
	mustRender(t, Options{SourceRoot: src, Output: out})

	info, err := os.Stat(filepath.Join(out, SysModuleRelPath, "xfs", "coresize"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o444), info.Mode().Perm())
}

func TestClear_EmptiesTheTreeButKeepsTheDirectory(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	mustRender(t, Options{Modules: Modules("550.163.01"), Output: out})

	root := filepath.Join(out, SysModuleRelPath)
	before, err := os.Stat(root)
	require.NoError(t, err)

	require.NoError(t, Clear(out))

	after, err := os.Stat(root)
	require.NoError(t, err, "the mount source must remain")
	require.True(t, os.SameFile(before, after), "a served container holds this inode")

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestClear_OnAnAbsentTreeIsNotAnError(t *testing.T) {
	t.Parallel()

	require.NoError(t, Clear(t.TempDir()))
}

func TestRender_IdempotentRerender(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	opts := Options{Modules: Modules("550.163.01"), Output: out}

	mustRender(t, opts)
	mustRender(t, opts)

	requireFileContent(t, filepath.Join(out, SysModuleRelPath, NVIDIA, "refcnt"), "1\n")
	target, err := os.Readlink(filepath.Join(out, SysModuleRelPath, NVIDIA, "holders", NVIDIAUVM))
	require.NoError(t, err)
	require.Equal(t, filepath.Join("..", "..", NVIDIAUVM), target)
}

func requireFileContent(t *testing.T, path, want string) {
	t.Helper()

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, want, string(got))
}
