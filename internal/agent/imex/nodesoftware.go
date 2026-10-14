// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"net/url"
	"os"
	"path"
	"path/filepath"

	"go.uber.org/zap"
)

// DefaultProvidedDir is where the node agent looks for the pinned IMEX archive,
// under the file name NVIDIA publishes it with.
const DefaultProvidedDir = "/opt/mokka/imex"

// nodeSoftware stages the IMEX daemon and control tool into the driver tree,
// where the NVIDIA DRA driver finds them as it would on a real node.
type nodeSoftware struct {
	root        string
	shimPath    string
	providedDir string
	arch        string
	lock        Lock

	// publishedDigest is the SHA-256 of the archive this process last
	// published, so a resync that changes nothing does not rewrite the tree.
	publishedDigest string
	// lastOutcome is the outcome last logged, so a resync that changes
	// nothing does not log it again.
	lastOutcome string
}

// stage publishes IMEX from the provided archive, or removes it when no
// verified archive is available. It never fails: a node without IMEX keeps
// simulating everything else, and the DRA ComputeDomain daemon fails on it the
// way it fails on a real node without the IMEX package.
func (n *nodeSoftware) stage() {
	artifact, err := n.lock.artifact(n.arch)
	if err != nil {
		n.unavailable("unsupported-arch", "no pinned IMEX archive for this architecture", zap.Error(err))
		return
	}
	file := path.Base(artifact.Path)
	candidate := filepath.Join(n.providedDir, file)

	valid, err := fileHasDigest(candidate, artifact.SHA256)
	switch {
	case err != nil:
		n.unavailable("unreadable:"+candidate, "cannot read the provided IMEX archive",
			zap.String("archive", candidate), zap.Error(err))
	case valid:
		if err := n.publish(candidate, artifact.SHA256); err != nil {
			n.unavailable("publish-failed:"+candidate, "cannot stage the provided IMEX archive",
				zap.String("archive", candidate), zap.Error(err))
			return
		}
		n.report("published:"+candidate, func() {
			zap.L().Info("staged IMEX node software",
				zap.String("simulator", name), zap.String("version", n.lock.Version), zap.String("archive", candidate))
		})
	case exists(candidate):
		n.unavailable("mismatch:"+candidate, "provided IMEX archive does not match the pinned SHA-256; ignoring it",
			zap.String("archive", candidate), zap.String("sha256", artifact.SHA256))
	default:
		n.unavailable("missing", "IMEX node software not provided; ComputeDomain daemons on this node fail until it is",
			n.provideFields(artifact, candidate)...)
	}
}

// publish stages archive unless this process already published an archive
// with that digest and its files are all still there.
func (n *nodeSoftware) publish(archive, digest string) error {
	if n.publishedDigest == digest && fullyPublished(n.root) {
		return nil
	}
	n.publishedDigest = ""
	if err := stageFromArchive(n.root, n.shimPath, archive, digest); err != nil {
		return err
	}
	n.publishedDigest = digest
	return nil
}

// unavailable withdraws any staged IMEX and logs why, once per outcome.
func (n *nodeSoftware) unavailable(outcome, msg string, fields ...zap.Field) {
	n.publishedDigest = ""
	if err := withdrawNodeSoftware(n.root); err != nil {
		zap.L().Error("cannot withdraw IMEX node software", zap.String("simulator", name), zap.Error(err))
	}
	n.report(outcome, func() {
		zap.L().Warn(msg, append([]zap.Field{zap.String("simulator", name)}, fields...)...)
	})
}

// remove withdraws staged IMEX when the node stops simulating it.
func (n *nodeSoftware) remove() error {
	n.lastOutcome = ""
	n.publishedDigest = ""
	return withdrawNodeSoftware(n.root)
}

func (n *nodeSoftware) report(outcome string, log func()) {
	if outcome == n.lastOutcome {
		return
	}
	n.lastOutcome = outcome
	log()
}

// provideFields tells an operator exactly what to provide and where.
func (n *nodeSoftware) provideFields(artifact Artifact, candidate string) []zap.Field {
	source, err := url.JoinPath(n.lock.BaseURL, artifact.Path)
	if err != nil {
		source = artifact.Path
	}
	return []zap.Field{
		zap.String("version", n.lock.Version),
		zap.String("arch", n.arch),
		zap.String("archive", candidate),
		zap.String("sha256", artifact.SHA256),
		zap.String("source", source),
	}
}

func exists(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil
}
