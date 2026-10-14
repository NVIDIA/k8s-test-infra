// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package imex

import (
	"context"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
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
	// lastOutcome is the outcome last logged, so a resync that changes
	// nothing does not log it again.
	lastOutcome string
}

// source is a directory the archive may be found in, by its NVIDIA file name.
type source struct {
	location string
	dir      string
}

// event is one thing stage found. stage logs its events together, once per
// distinct set.
type event struct {
	key    string
	level  zapcore.Level
	msg    string
	fields []zap.Field
}

func warn(key, msg string, fields ...zap.Field) event {
	return event{key: key, level: zapcore.WarnLevel, msg: msg, fields: fields}
}

// stage publishes IMEX from the first source holding the archive the lock
// pins: the provided directory, then the node cache. An archive that does not
// match the pin is skipped with a warning. When neither holds one, stage
// removes IMEX and, while IMEXDownload is on, starts a download into the node
// cache. A pinned archive that cannot be staged ends the search: another copy
// of the same bytes would fail the same way. It never fails: a node without
// IMEX keeps simulating everything else, and the DRA ComputeDomain daemon
// fails on it the way it fails on a real node without the IMEX package.
func (n *nodeSoftware) stage(ctx context.Context) {
	artifact, err := n.lock.artifact(n.arch)
	if err != nil {
		n.unavailable(warn("unsupported-arch", "no pinned IMEX archive for this architecture", zap.Error(err)))
		return
	}

	var events []event
	for _, src := range []source{
		{location: "provided", dir: n.providedDir},
		{location: "node cache", dir: cachedArchiveDir(n.root, n.lock, n.arch)},
	} {
		e, found := n.publishFrom(src, artifact)
		switch found {
		case staged:
			n.report(append(events, e)...)
			return
		case unstageable:
			n.unavailable(append(events, e)...)
			return
		case skipped:
			events = append(events, e)
		case absent:
		}
	}
	n.unavailable(append(events, n.fallback(ctx, artifact))...)
}

// finding is what publishFrom found in a source.
type finding int

const (
	absent      finding = iota // no archive under the pinned name
	skipped                    // an archive that is not the pinned one, or unreadable
	staged                     // the pinned archive, now staged
	unstageable                // the pinned archive, which could not be staged
)

// publishFrom publishes the archive in src when it matches the pin, reporting
// what it found and what to log about it.
func (n *nodeSoftware) publishFrom(src source, artifact Artifact) (event, finding) {
	candidate := filepath.Join(src.dir, path.Base(artifact.Path))
	fields := []zap.Field{zap.String("location", src.location), zap.String("archive", candidate)}

	valid, err := fileHasDigest(candidate, artifact.SHA256)
	switch {
	case err != nil:
		return warn("unreadable:"+candidate, "cannot read IMEX archive; ignoring it", append(fields, zap.Error(err))...), skipped
	case valid:
		if err := n.publish(candidate, artifact.SHA256); err != nil {
			return warn("publish-failed:"+candidate, "cannot stage IMEX archive", append(fields, zap.Error(err))...), unstageable
		}
		return event{
			key: "published:" + candidate, level: zapcore.InfoLevel, msg: "staged IMEX node software",
			fields: append(fields, zap.String("version", n.lock.Version)),
		}, staged
	case exists(candidate):
		return warn("mismatch:"+candidate, "IMEX archive does not match the pinned SHA-256; ignoring it",
			append(fields, zap.String("version", n.lock.Version), zap.String("sha256", artifact.SHA256))...), skipped
	default:
		return event{}, absent
	}
}

// fallback downloads the archive when IMEXDownload allows it, and says what is
// missing otherwise.
func (n *nodeSoftware) fallback(ctx context.Context, artifact Artifact) event {
	fields := n.provideFields(artifact)
	if n.download == nil {
		return warn("missing", "IMEX node software not provided; ComputeDomain daemons on this node fail until it is", fields...)
	}
	if err := n.download.start(ctx, n.root, n.lock, n.arch); err != nil {
		return warn("download-failed",
			"cannot download IMEX node software; ComputeDomain daemons on this node fail until it is provided or the node agent restarts",
			append(fields, zap.Error(err))...)
	}
	return event{
		key: "downloading", level: zapcore.InfoLevel, msg: "downloading IMEX node software into the node cache",
		fields: append(fields, zap.String("cache", cachedArchiveDir(n.root, n.lock, n.arch))),
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
func (n *nodeSoftware) unavailable(events ...event) {
	n.publishedDigest = ""
	if err := withdrawNodeSoftware(n.root); err != nil {
		zap.L().Error("cannot withdraw IMEX node software", zap.String("simulator", name), zap.Error(err))
	}
	n.report(events...)
}

// remove withdraws staged IMEX when the node stops simulating it.
func (n *nodeSoftware) remove() error {
	n.lastOutcome = ""
	n.publishedDigest = ""
	return withdrawNodeSoftware(n.root)
}

func (n *nodeSoftware) report(events ...event) {
	keys := make([]string, 0, len(events))
	for _, e := range events {
		keys = append(keys, e.key)
	}
	outcome := strings.Join(keys, ";")
	if outcome == n.lastOutcome {
		return
	}
	n.lastOutcome = outcome
	for _, e := range events {
		if ce := zap.L().Check(e.level, e.msg); ce != nil {
			ce.Write(append([]zap.Field{zap.String("simulator", name)}, e.fields...)...)
		}
	}
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
