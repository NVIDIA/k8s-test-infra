// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"context"
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
	// download fills the node cache when no source holds the archive. nil
	// while the IMEXDownload feature gate is off.
	download *downloader

	// publishedDigest is the SHA-256 of the archive this process last
	// published, so a resync that changes nothing does not rewrite the tree.
	publishedDigest string
}

// source is a directory the archive may be found in, by its NVIDIA file name.
type source struct {
	location string
	dir      string
}

// stage publishes IMEX from the first source holding the archive the lock
// pins: the provided directory, then the node cache. An archive that does not
// match the pin is skipped with a warning. When neither holds one, stage
// removes IMEX and, while IMEXDownload is on, starts a download into the node
// cache. A pinned archive that cannot be staged ends the search: another copy
// of the same bytes would fail the same way. It never fails: a node without
// IMEX keeps simulating everything else, and the DRA ComputeDomain daemon
// fails on it the way it fails on a real node without the IMEX package. Its
// warnings repeat on every pass, so a node without IMEX keeps saying why.
func (n *nodeSoftware) stage(ctx context.Context) {
	artifact, err := n.lock.artifact(n.arch)
	if err != nil {
		zap.L().Warn("no pinned IMEX archive for this architecture", zap.String("simulator", name), zap.Error(err))
		n.unavailable()
		return
	}

	for _, src := range []source{
		{location: "provided", dir: n.providedDir},
		{location: "node cache", dir: cachedArchiveDir(n.root, n.lock, n.arch)},
	} {
		switch n.publishFrom(src, artifact) {
		case staged:
			return
		case unstageable:
			n.unavailable()
			return
		case skipped, absent:
		}
	}
	n.unavailable()
	n.fallback(ctx, artifact)
}

// finding is what publishFrom found in a source.
type finding int

const (
	absent      finding = iota // no archive under the pinned name
	skipped                    // an archive that is not the pinned one, or unreadable
	staged                     // the pinned archive, now staged
	unstageable                // the pinned archive, which could not be staged
)

// publishFrom publishes the archive in src when it matches the pin, logs what
// it found, and returns it.
func (n *nodeSoftware) publishFrom(src source, artifact Artifact) finding {
	candidate := filepath.Join(src.dir, path.Base(artifact.Path))
	log := zap.L().With(zap.String("simulator", name), zap.String("location", src.location), zap.String("archive", candidate))

	valid, err := fileHasDigest(candidate, artifact.SHA256)
	switch {
	case err != nil:
		log.Warn("cannot read IMEX archive; ignoring it", zap.Error(err))
		return skipped
	case valid:
		if err := n.publish(candidate, artifact.SHA256, log); err != nil {
			log.Warn("cannot stage IMEX archive", zap.Error(err))
			return unstageable
		}
		return staged
	case exists(candidate):
		log.Warn("IMEX archive does not match the pinned SHA-256; ignoring it",
			zap.String("version", n.lock.Version), zap.String("sha256", artifact.SHA256))
		return skipped
	default:
		return absent
	}
}

// fallback downloads the archive when IMEXDownload allows it, and says what is
// missing otherwise.
func (n *nodeSoftware) fallback(ctx context.Context, artifact Artifact) {
	log := zap.L().With(append([]zap.Field{zap.String("simulator", name)}, n.provideFields(artifact)...)...)
	if n.download == nil {
		log.Warn("IMEX node software not provided; ComputeDomain daemons on this node fail until it is")
		return
	}
	started, err := n.download.start(ctx, n.root, n.lock, n.arch)
	switch {
	case err != nil:
		log.Warn("cannot download IMEX node software; ComputeDomain daemons on this node fail until it is provided or the node agent restarts",
			zap.Error(err))
	case started:
		log.Info("downloading IMEX node software into the node cache",
			zap.String("cache", cachedArchiveDir(n.root, n.lock, n.arch)))
	}
}

// publish stages archive unless this process already published an archive
// with that digest and its files are all still there, and logs a publication.
func (n *nodeSoftware) publish(archive, digest string, log *zap.Logger) error {
	if n.publishedDigest == digest && fullyPublished(n.root) {
		return nil
	}
	n.publishedDigest = ""
	if err := stageFromArchive(n.root, n.shimPath, archive, digest); err != nil {
		return err
	}
	n.publishedDigest = digest
	log.Info("staged IMEX node software", zap.String("version", n.lock.Version))
	return nil
}

// unavailable withdraws any staged IMEX.
func (n *nodeSoftware) unavailable() {
	n.publishedDigest = ""
	if err := withdrawNodeSoftware(n.root); err != nil {
		zap.L().Error("cannot withdraw IMEX node software", zap.String("simulator", name), zap.Error(err))
	}
}

// remove withdraws staged IMEX when the node stops simulating it.
func (n *nodeSoftware) remove() error {
	n.publishedDigest = ""
	return withdrawNodeSoftware(n.root)
}

// provideFields tells an operator exactly what to provide and where.
func (n *nodeSoftware) provideFields(artifact Artifact) []zap.Field {
	source, err := url.JoinPath(n.lock.BaseURL, artifact.Path)
	if err != nil {
		source = artifact.Path
	}
	return []zap.Field{
		zap.String("version", n.lock.Version),
		zap.String("arch", n.arch),
		zap.String("archive", filepath.Join(n.providedDir, path.Base(artifact.Path))),
		zap.String("sha256", artifact.SHA256),
		zap.String("source", source),
	}
}

func exists(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil
}
