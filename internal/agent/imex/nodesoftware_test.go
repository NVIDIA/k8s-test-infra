// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/NVIDIA/k8s-test-infra/internal/imex"
)

// testArchive builds an IMEX-shaped archive, leaving out omit when it names a
// member.
func testArchive(t *testing.T, omit string) []byte {
	t.Helper()

	files := map[string]string{
		"usr/bin/nvidia-imex":                   "real-imex",
		"usr/bin/nvidia-imex-ctl":               "imex-ctl",
		"etc/nvidia-imex/config.cfg":            "IMEX_CMD_ENABLED=1\n",
		"LICENSE":                               "license",
		"usr/share/doc/third-party-notices.txt": "notices",
	}
	delete(files, omit)

	var compressed bytes.Buffer
	xzw, err := xz.NewWriter(&compressed)
	require.NoError(t, err)
	tw := tar.NewWriter(xzw)
	for name, contents := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     "imex-test-archive/" + name,
			Mode:     0o755,
			Size:     int64(len(contents)),
			Typeflag: tar.TypeReg,
		}))
		_, err := tw.Write([]byte(contents))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, xzw.Close())
	return compressed.Bytes()
}

// testLock pins archive for both architectures.
func testLock(archive []byte) Lock {
	sum := sha256.Sum256(archive)
	artifact := Artifact{Path: "imex/linux-x86_64/imex-test-archive.tar.xz", SHA256: hex.EncodeToString(sum[:])}
	return Lock{
		Version: "test-version",
		BaseURL: "https://example.invalid/redist",
		Artifacts: map[string]Artifact{
			"amd64": artifact,
			"arm64": artifact,
		},
	}
}

func testNodeSoftware(t *testing.T, lock Lock) *nodeSoftware {
	t.Helper()

	shim := filepath.Join(t.TempDir(), "nvidia-imex-shim")
	require.NoError(t, os.WriteFile(shim, []byte("mokka-shim"), 0o755))
	return &nodeSoftware{
		root:        t.TempDir(),
		shimPath:    shim,
		providedDir: t.TempDir(),
		arch:        "amd64",
		lock:        lock,
	}
}

// provide places archive in the provided directory under its pinned name.
func provide(t *testing.T, n *nodeSoftware, archive []byte) string {
	t.Helper()
	artifact, err := n.lock.artifact(n.arch)
	require.NoError(t, err)
	file := filepath.Join(n.providedDir, path.Base(artifact.Path))
	require.NoError(t, os.WriteFile(file, archive, 0o644))
	return file
}

func requireFileContent(t *testing.T, filename, expected string) {
	t.Helper()
	content, err := os.ReadFile(filename)
	require.NoError(t, err)
	require.Equal(t, expected, string(content))
}

// stagedDestinations lists every file stageFromArchive writes, the shim first.
func stagedDestinations() []string {
	destinations := make([]string, 0, 1+len(archiveFiles))
	destinations = append(destinations, shimDestination)
	for _, file := range archiveFiles {
		destinations = append(destinations, file.destination)
	}
	return destinations
}

// requireNothingStaged checks that no consumer finds IMEX under root:
// nvidia-imex.real, the presence signal, is absent.
func requireNothingStaged(t *testing.T, root string) {
	t.Helper()
	require.NoFileExists(t, filepath.Join(root, realDestination))
	require.False(t, Staged(root))
}

// requireWithdrawn checks that an earlier install was withdrawn without
// breaking DRA claims prepared while it was staged: their CDI specs bind the
// shim and nvidia-imex-ctl, which stay.
func requireWithdrawn(t *testing.T, root string) {
	t.Helper()
	requireNothingStaged(t, root)
	require.FileExists(t, filepath.Join(root, shimDestination))
	require.FileExists(t, filepath.Join(root, imex.BinDir, imex.CtlBinary))
}

func TestNodeSoftwarePublishesAProvidedArchive(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	n := testNodeSoftware(t, testLock(archive))
	provide(t, n, archive)

	n.stage()

	requireFileContent(t, filepath.Join(n.root, "driver/usr/bin/nvidia-imex"), "mokka-shim")
	requireFileContent(t, filepath.Join(n.root, "driver/usr/bin/nvidia-imex.real"), "real-imex")
	requireFileContent(t, filepath.Join(n.root, "driver/usr/bin/nvidia-imex-ctl"), "imex-ctl")
	requireFileContent(t, filepath.Join(n.root, "driver/etc/nvidia-imex/config.cfg"), "IMEX_CMD_ENABLED=1\n")
	requireFileContent(t, filepath.Join(n.root, "driver/usr/share/doc/nvidia-imex/LICENSE"), "license")
	requireFileContent(t, filepath.Join(n.root, "driver/usr/share/doc/nvidia-imex/third-party-notices.txt"), "notices")
}

// Stage re-runs on every resync; republishing an unchanged archive would
// rewrite the tree, and withdraw IMEX briefly, for nothing.
func TestNodeSoftwareDoesNotRepublishTheSameArchive(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	n := testNodeSoftware(t, testLock(archive))
	provide(t, n, archive)
	n.stage()
	before := statStaged(t, n.root)

	n.stage()

	after := statStaged(t, n.root)
	for i := range before {
		require.True(t, os.SameFile(before[i], after[i]), "%s was rewritten", before[i].Name())
	}
}

// A restarted node agent finds its predecessor's install. Withdrawing it to
// stage the same files again would fail every container that mounts IMEX
// through the CDI spec in the meantime.
func TestNodeSoftwareKeepsAnIdenticalInstallAcrossARestart(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	n := testNodeSoftware(t, testLock(archive))
	provide(t, n, archive)
	n.stage()
	before := statStaged(t, n.root)

	restarted := *n
	restarted.publishedDigest, restarted.lastOutcome = "", ""
	restarted.stage()

	after := statStaged(t, n.root)
	for i := range before {
		require.True(t, os.SameFile(before[i], after[i]), "%s was rewritten", before[i].Name())
	}
}

func TestNodeSoftwareReplacesADifferentInstallAfterARestart(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	n := testNodeSoftware(t, testLock(archive))
	provide(t, n, archive)
	n.stage()
	require.NoError(t, os.WriteFile(filepath.Join(n.root, "driver/usr/bin/nvidia-imex-ctl"), []byte("older"), 0o755))

	restarted := *n
	restarted.publishedDigest, restarted.lastOutcome = "", ""
	restarted.stage()

	requireFileContent(t, filepath.Join(n.root, "driver/usr/bin/nvidia-imex-ctl"), "imex-ctl")
}

// A file missing from the tree makes the next stage publish everything again.
func TestNodeSoftwareRepublishesAnIncompleteTree(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	n := testNodeSoftware(t, testLock(archive))
	provide(t, n, archive)
	n.stage()
	require.NoError(t, os.Remove(filepath.Join(n.root, "driver/usr/bin/nvidia-imex-ctl")))

	n.stage()

	requireFileContent(t, filepath.Join(n.root, "driver/usr/bin/nvidia-imex-ctl"), "imex-ctl")
}

// A failed publication withdraws IMEX, including an install that worked:
// nvidia-imex.real must never stand over a set it did not come with.
func TestPublishFailureWithdrawsAnEarlierInstall(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	lock := testLock(archive)
	n := testNodeSoftware(t, lock)
	file := provide(t, n, archive)
	n.stage()
	require.FileExists(t, filepath.Join(n.root, shimDestination))

	err := stageFromArchive(n.root, filepath.Join(t.TempDir(), "no-shim"), file, lock.Artifacts["amd64"].SHA256)

	require.ErrorContains(t, err, "publish nvidia-imex shim")
	requireWithdrawn(t, n.root)
}

func statStaged(t *testing.T, root string) []os.FileInfo {
	t.Helper()
	destinations := stagedDestinations()
	infos := make([]os.FileInfo, 0, len(destinations))
	for _, rel := range destinations {
		info, err := os.Stat(filepath.Join(root, rel))
		require.NoError(t, err)
		infos = append(infos, info)
	}
	return infos
}

func TestNodeSoftwareStagesNothingWithoutAProvidedArchive(t *testing.T) {
	t.Parallel()

	n := testNodeSoftware(t, testLock(testArchive(t, "")))

	n.stage()

	requireNothingStaged(t, n.root)
}

// An archive under the pinned name but with other bytes is a different IMEX,
// an older one or a corrupt copy, and must never reach the driver tree.
func TestNodeSoftwareIgnoresAnArchiveThatDoesNotMatchThePin(t *testing.T) {
	t.Parallel()

	n := testNodeSoftware(t, testLock(testArchive(t, "")))
	provide(t, n, testArchive(t, "LICENSE"))

	n.stage()

	requireNothingStaged(t, n.root)
}

func TestNodeSoftwareRejectsAnIncompleteArchive(t *testing.T) {
	t.Parallel()

	incomplete := testArchive(t, "usr/bin/nvidia-imex-ctl")
	n := testNodeSoftware(t, testLock(incomplete))
	provide(t, n, incomplete)

	n.stage()

	requireNothingStaged(t, n.root)
}

func TestNodeSoftwareWithdrawsIMEXWhenTheArchiveGoesAway(t *testing.T) {
	t.Parallel()

	archive := testArchive(t, "")
	n := testNodeSoftware(t, testLock(archive))
	file := provide(t, n, archive)
	n.stage()
	require.FileExists(t, filepath.Join(n.root, shimDestination))

	require.NoError(t, os.Remove(file))
	n.stage()

	requireWithdrawn(t, n.root)
}

// stagedSimulator returns a simulator whose host holds everything a
// successful Stage writes, node software included.
func stagedSimulator(t *testing.T) *Simulator {
	t.Helper()

	archive := testArchive(t, "")
	sim := New(testHost(t), Options{
		Architecture: "amd64",
		ShimPath:     testNodeSoftware(t, Lock{}).shimPath,
		ProvidedDir:  t.TempDir(),
		Lock:         testLock(archive),
	})
	provide(t, &sim.nodeSoftware, archive)
	sim.nodeSoftware.stage()
	require.FileExists(t, filepath.Join(sim.nodeSoftware.root, shimDestination))
	seedSurface(t, sim.host)
	return sim
}

func requireNoIMEX(t *testing.T, sim *Simulator) {
	t.Helper()
	requireWithdrawn(t, sim.nodeSoftware.root)
	requireNoSurface(t, sim.host)
}

// Discard withdraws IMEX but keeps the files a DRA claim's CDI spec binds, so
// a container of a claim prepared while IMEX was staged still restarts.
func TestDiscardKeepsWhatDRAClaimsMount(t *testing.T) {
	t.Parallel()

	sim := stagedSimulator(t)

	require.NoError(t, sim.Discard(t.Context()))

	requireNoIMEX(t, sim)
}

// IMEX follows the profile: a node whose profile is not multi-node NVLink
// loses all of IMEX at the next Stage, not only at shutdown.
func TestStageWithoutIMEXRemovesTheWholeSurface(t *testing.T) {
	t.Parallel()

	sim := stagedSimulator(t)

	require.NoError(t, sim.Stage(t.Context(), testState(false)))

	require.True(t, sim.Ready())
	requireNoIMEX(t, sim)
}

// A node whose kernel surface cannot be staged becomes a node without IMEX:
// Stage removes the node software too, and stays ready.
func TestStageThatCannotStageTheSurfaceRemovesEverything(t *testing.T) {
	t.Parallel()

	sim := stagedSimulator(t)
	sim.procDevicesPath = filepath.Join(t.TempDir(), "missing")

	require.NoError(t, sim.Stage(t.Context(), testState(true)))

	require.True(t, sim.Ready())
	requireNoIMEX(t, sim)
}

// A resync re-runs Stage with nothing changed. Logging the same outcome each
// time would bury the one message that tells an operator what to provide.
func TestNodeSoftwareLogsEachOutcomeOnce(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(restore)

	archive := testArchive(t, "")
	n := testNodeSoftware(t, testLock(archive))

	n.stage()
	n.stage()
	missing := logs.FilterMessageSnippet("not provided").All()
	require.Len(t, missing, 1)
	require.Equal(t, zapcore.WarnLevel, missing[0].Level)
	fields := missing[0].ContextMap()
	require.Equal(t, "test-version", fields["version"])
	require.Equal(t, "https://example.invalid/redist/imex/linux-x86_64/imex-test-archive.tar.xz", fields["source"])
	require.Equal(t, filepath.Join(n.providedDir, "imex-test-archive.tar.xz"), fields["archive"])
	require.Equal(t, "amd64", fields["arch"])
	require.Equal(t, n.lock.Artifacts["amd64"].SHA256, fields["sha256"])

	provide(t, n, archive)
	n.stage()
	n.stage()
	require.Len(t, logs.FilterMessage("staged IMEX node software").All(), 1)
}

// Only the archive counts, under the name NVIDIA publishes it with: binaries
// dropped into the provided directory are not IMEX the agent verified.
func TestNodeSoftwareIgnoresLooseFilesInTheProvidedDirectory(t *testing.T) {
	t.Parallel()

	n := testNodeSoftware(t, testLock(testArchive(t, "")))
	for _, name := range []string{"nvidia-imex", "nvidia-imex-ctl", "imex.tar.xz"} {
		require.NoError(t, os.WriteFile(filepath.Join(n.providedDir, name), []byte("loose"), 0o755))
	}

	n.stage()

	requireNothingStaged(t, n.root)
}
