// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nvidiakmod_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	nvidiakmod "github.com/NVIDIA/k8s-test-infra/shims/nvidia-kmod"
)

const (
	mockLibrary = "the mock NVML"
	nodeProfile = "the node's profile"
)

// The driver container starts the daemon after NVIDIA's installer and before it
// exposes its root, and the stand-in then copies what the binds serve under the
// binds: what an operand finds in a root that has lost them.
func TestPersistencedStandInCopiesTheMockNVMLUnderItsBinds(t *testing.T) {
	t.Parallel()

	run := newPersistencedRun(t)

	out := run.succeed(t)

	require.NotContains(t, out, "warning")
	require.Equal(t, mockLibrary, fileContent(t, run.covered(run.library)))
	require.Equal(t, nodeProfile, fileContent(t, filepath.Join(run.covered(run.profile), "config.yaml")))
	require.Equal(t, "t4", fileContent(t, filepath.Join(run.covered(run.profile), "machine-type")))
	require.Equal(t, mockLibrary, fileContent(t, run.library), "the binds keep serving the live root")
}

// The copies only matter in a race the driver may never run into, so failing
// to make them leaves the installation to go on as before, with a warning.
func TestPersistencedStandInWarnsWhenItCannotCopy(t *testing.T) {
	t.Parallel()

	run := newPersistencedRun(t)
	require.NoError(t, os.Remove(run.library))

	out := run.succeed(t)

	require.Contains(t, out, "warning")
	require.Contains(t, out, "not started", "the daemon's stand-in still reports as it always does")
}

// persistencedRun is the staged stand-in run against a live root in a temporary
// tree, with mount primitives that need no privileges: the view of the root
// without its submounts shows covered, the root as the binds cover it.
type persistencedRun struct {
	script, library, profile, under, bin string
}

func newPersistencedRun(t *testing.T) persistencedRun {
	t.Helper()

	root := t.TempDir()
	run := persistencedRun{
		script:  filepath.Join(root, "stand-ins", "nvidia-persistenced"),
		library: filepath.Join(root, "live", "usr", "lib64", "libnvidia-ml.so.1"),
		profile: filepath.Join(root, "live", "config"),
		under:   filepath.Join(root, "covered"),
		bin:     filepath.Join(root, "bin"),
	}

	writeTestFile(t, run.script, sourceContent(t, nvidiakmod.PersistencedName), 0o755)
	writeTestFile(t, run.library, mockLibrary, 0o755)
	writeTestFile(t, filepath.Join(run.profile, "config.yaml"), nodeProfile, 0o644)
	writeTestFile(t, filepath.Join(run.profile, "machine-type"), "t4", 0o644)

	// What runc leaves under the binds: an empty file and an empty directory.
	writeTestFile(t, run.covered(run.library), "", 0o644)
	require.NoError(t, os.MkdirAll(run.covered(run.profile), 0o755))

	writeTestFile(t, filepath.Join(run.bin, "unshare"), `#!/bin/sh
while [ $# -gt 0 ]; do
  case $1 in
    --propagation) shift 2 ;;
    --*) shift ;;
    *) break ;;
  esac
done
exec "$@"
`, 0o755)
	writeTestFile(t, filepath.Join(run.bin, "mount"), fmt.Sprintf(`#!/bin/sh
[ "$1" = --bind ] && [ "$2" = / ] || exit 1
rmdir "$3" && ln -s %q "$3"
`, run.under), 0o755)
	writeTestFile(t, filepath.Join(run.bin, "umount"), "#!/bin/sh\nrm \"$1\" && mkdir \"$1\"\n", 0o755)

	return run
}

// covered is where path is in the root as the binds cover it.
func (r persistencedRun) covered(path string) string {
	return filepath.Join(r.under, path)
}

// succeed starts the stand-in as the driver container does, and returns what
// it printed.
func (r persistencedRun) succeed(t *testing.T) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "sh", r.script, "--persistence-mode")
	cmd.Env = append(os.Environ(),
		"MOKKA_NVML="+r.library,
		"MOKKA_NVML_PROFILE="+r.profile,
		"PATH="+r.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)

	return string(out)
}

func writeTestFile(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), perm))
}

func sourceContent(t *testing.T, name string) string {
	t.Helper()

	data, err := nvidiakmod.Sources.ReadFile(name)
	require.NoError(t, err)

	return string(data)
}

func fileContent(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(data)
}
