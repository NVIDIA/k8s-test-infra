import assert from "node:assert/strict";
import test from "node:test";

import {
  bindFinalDigest,
  classifyEdgeAncestry,
  normalizedChartTreeDigest,
  planAssetPublication,
  planChartPublication,
  planDevelopmentPublication,
  planEvidencePublication,
  planImagePublication,
  planImmutableImagePromotion,
  resolveReleaseIntent,
  validateReleaseBinding,
  validateDefaultBranch,
} from "./release-state.mjs";
import { chartArchive } from "./release-state-test-helpers.mjs";

const SHA_A = "a".repeat(40);
const SHA_B = "b".repeat(40);
const DIGEST_A = `sha256:${"a".repeat(64)}`;
const DIGEST_B = `sha256:${"b".repeat(64)}`;

test("release binding requires one canonical tag, commit, chart, image, and GitHub release identity", () => {
  const valid = {
    tagName: "v1.2.3",
    major: "1",
    minor: "2",
    patch: "3",
    releaseSha: SHA_A,
    peeledTagSha: SHA_A,
    checkoutSha: SHA_A,
    chartVersion: "1.2.3",
    chartAppVersion: "1.2.3",
    imageSourceRevision: SHA_A,
    githubReleaseTag: "v1.2.3",
    githubReleaseTarget: SHA_A,
  };
  assert.deepEqual(validateReleaseBinding(valid), { version: "1.2.3", tagName: "v1.2.3", sha: SHA_A });
  for (const [field, value] of [
    ["tagName", "1.2.3"],
    ["major", "01"],
    ["releaseSha", SHA_B],
    ["peeledTagSha", SHA_B],
    ["checkoutSha", SHA_B],
    ["chartVersion", "1.2.4"],
    ["chartAppVersion", "1.2.4"],
    ["imageSourceRevision", SHA_B],
    ["githubReleaseTag", "v1.2.4"],
    ["githubReleaseTarget", SHA_B],
  ]) {
    assert.throws(() => validateReleaseBinding({ ...valid, [field]: value }), { name: "TypeError" });
  }
  for (const version of ["01.2.3", "1.02.3", "1.2.03", "v1.2.3", "1.2.3-rc.1", "1.2.3+meta", "1.2", "1.2.3\n"] ) {
    assert.throws(() => validateReleaseBinding({ ...valid, tagName: `v${version}`, major: version.split(".")[0] }), { name: "TypeError" });
  }
});

test("image publication decisions are immutable, resumable, collision-safe, and monotonic", () => {
  const base = { version: "1.2.3", releaseSha: SHA_A, stable: null, short: null, minor: null, major: null, latest: null };
  assert.deepEqual(planImagePublication(base), {
    immutable: { action: "publish", digest: null },
    development: { short: "defer" },
    aliases: { minor: "defer", major: "defer", latest: "defer" },
    resume: { signature: true, sbom: true, provenance: true },
  });

  const stable = { digest: DIGEST_A, sourceRevision: SHA_A, signature: true, sbom: false, provenance: false };
  assert.deepEqual(planImagePublication({ ...base, stable }), {
    immutable: { action: "reuse", digest: DIGEST_A },
    development: { short: "update" },
    aliases: { minor: "update", major: "update", latest: "update" },
    resume: { signature: false, sbom: true, provenance: true },
  });
  assert.equal(planImagePublication({ ...base, short: { digest: DIGEST_B, sourceRevision: SHA_A } }).development.short, "preserve");
  assert.equal(planImagePublication({ ...base, stable, short: { digest: DIGEST_A, sourceRevision: SHA_A } }).development.short, "skip");
  assert.equal(planImagePublication({ ...base, stable, short: { digest: DIGEST_B, sourceRevision: SHA_A } }).development.short, "update");
  assert.throws(() => planImagePublication({ ...base, short: { digest: DIGEST_B, sourceRevision: SHA_B } }), { name: "TypeError" });
  assert.throws(() => planImagePublication({ ...base, short: { digest: DIGEST_B } }), { name: "TypeError" });
  assert.equal(planImagePublication({ ...base, stable, minor: { digest: DIGEST_A, version: "1.2.3" } }).aliases.minor, "skip");
  assert.equal(planImagePublication({ ...base, stable, minor: { digest: DIGEST_B, version: "1.2.2" } }).aliases.minor, "update");
  for (const remote of [
    { digest: DIGEST_B, version: "1.2.3" },
    { digest: DIGEST_B, version: "1.2.4" },
    { digest: DIGEST_B, version: "broken" },
  ]) {
    assert.throws(() => planImagePublication({ ...base, stable, latest: remote }), { name: "TypeError" });
  }
  for (const badStable of [
    { digest: DIGEST_A },
    { digest: DIGEST_A, sourceRevision: SHA_B },
    { digest: "sha256:short", sourceRevision: SHA_A },
  ]) {
    assert.throws(() => planImagePublication({ ...base, stable: badStable }), { name: "TypeError" });
  }
});

test("ordinary pushes publish one immutable SHA then promote collision-safe development tags", () => {
  const absent = { releaseSha: SHA_A, immutable: null, short: null, edge: null, edgeRelation: "absent" };
  assert.deepEqual(planDevelopmentPublication(absent), {
    immutable: { action: "publish", digest: null },
    short: "defer",
    edge: "defer",
  });
  const immutable = { digest: DIGEST_A, sourceRevision: SHA_A };
  assert.deepEqual(planDevelopmentPublication({ ...absent, immutable }), {
    immutable: { action: "reuse", digest: DIGEST_A },
    short: "update",
    edge: "update",
  });
  assert.equal(planDevelopmentPublication({ ...absent, immutable, short: immutable, edge: immutable, edgeRelation: "equal" }).short, "skip");
  assert.equal(planDevelopmentPublication({ ...absent, immutable, edge: { digest: DIGEST_B, sourceRevision: SHA_A }, edgeRelation: "equal" }).edge, "update");
  assert.throws(() => planDevelopmentPublication({ ...absent, immutable, short: { digest: DIGEST_B, sourceRevision: SHA_B } }), { name: "TypeError" });
  assert.equal(planDevelopmentPublication({ ...absent, immutable, edge: { digest: DIGEST_B, sourceRevision: SHA_B }, edgeRelation: "ancestor" }).edge, "update");
  assert.throws(() => planDevelopmentPublication({ ...absent, immutable, edge: { digest: DIGEST_A, sourceRevision: SHA_B }, edgeRelation: "ancestor" }), { name: "TypeError" });
  assert.throws(() => planDevelopmentPublication({ ...absent, immutable, edge: { digest: DIGEST_B, sourceRevision: SHA_B }, edgeRelation: "descendant" }), { name: "TypeError" });
  assert.throws(() => planDevelopmentPublication({ ...absent, immutable, edge: { digest: DIGEST_B, sourceRevision: SHA_B }, edgeRelation: "unrelated" }), { name: "TypeError" });
});

test("edge ancestry classification is derived from verified Git ancestry", () => {
  const relation = (edgeSha, answers) => classifyEdgeAncestry({ edgeSha, releaseSha: SHA_A }, (left, right) => answers.get(`${left}:${right}`) ?? false);
  assert.equal(classifyEdgeAncestry({ edgeSha: null, releaseSha: SHA_A }, () => false), "absent");
  assert.equal(relation(SHA_A, new Map()), "equal");
  assert.equal(relation(SHA_B, new Map([[`${SHA_B}:${SHA_A}`, true]])), "ancestor");
  assert.equal(relation(SHA_B, new Map([[`${SHA_A}:${SHA_B}`, true]])), "descendant");
  assert.equal(relation(SHA_B, new Map()), "unrelated");
});

test("default branch identity is a bounded safe Git branch", () => {
  assert.equal(validateDefaultBranch("main"), "main");
  assert.equal(validateDefaultBranch("release/stable-1.2"), "release/stable-1.2");
  for (const value of ["", "-main", "/main", "main..old", "main//old", "main@{old", "main.lock", "main\nforged"]) {
    assert.throws(() => validateDefaultBranch(value), { name: "TypeError" });
  }
});

test("final immutable digest binding rejects a changed registry identity", () => {
  assert.equal(bindFinalDigest({ selectedDigest: DIGEST_A, finalDigest: DIGEST_A }), DIGEST_A);
  assert.throws(() => bindFinalDigest({ selectedDigest: DIGEST_A, finalDigest: DIGEST_B }), { name: "TypeError" });
  assert.throws(() => bindFinalDigest({ selectedDigest: "sha256:short", finalDigest: DIGEST_A }), { name: "TypeError" });
});

test("immutable image publication stages by digest and fails closed on a concurrent final tag", () => {
  assert.deepEqual(planImmutableImagePromotion({ initialDigest: null, stagedDigest: DIGEST_A, finalDigest: null }), { action: "update", digest: DIGEST_A });
  assert.deepEqual(planImmutableImagePromotion({ initialDigest: null, stagedDigest: DIGEST_A, finalDigest: DIGEST_A }), { action: "skip", digest: DIGEST_A });
  assert.throws(() => planImmutableImagePromotion({ initialDigest: null, stagedDigest: DIGEST_A, finalDigest: DIGEST_B }), { name: "TypeError" });
  assert.deepEqual(planImmutableImagePromotion({ initialDigest: DIGEST_A, stagedDigest: null, finalDigest: DIGEST_A }), { action: "skip", digest: DIGEST_A });
  assert.throws(() => planImmutableImagePromotion({ initialDigest: DIGEST_A, stagedDigest: null, finalDigest: null }), { name: "TypeError" });
  assert.throws(() => planImmutableImagePromotion({ initialDigest: DIGEST_A, stagedDigest: DIGEST_B, finalDigest: DIGEST_A }), { name: "TypeError" });
});

test("push and manual release paths converge on one validated intent contract", () => {
  const released = {
    releaseCreated: "true", tagName: "v1.2.3", major: "1", minor: "2", patch: "3", sha: SHA_A,
  };
  assert.deepEqual(resolveReleaseIntent({ eventName: "push", pushSha: SHA_A, manualVersion: "", manualSha: "", releasePlease: released }), {
    mode: "release", version: "1.2.3", tagName: "v1.2.3", major: "1", minor: "2", patch: "3", sha: SHA_A,
  });
  assert.deepEqual(resolveReleaseIntent({ eventName: "push", pushSha: SHA_A, manualVersion: "", manualSha: "", releasePlease: { releaseCreated: "false" } }), {
    mode: "development", version: "", tagName: "", major: "", minor: "", patch: "", sha: SHA_A,
  });
  assert.deepEqual(resolveReleaseIntent({ eventName: "workflow_dispatch", pushSha: "", manualVersion: "1.2.3", manualSha: SHA_A, releasePlease: null }), {
    mode: "release", version: "1.2.3", tagName: "v1.2.3", major: "1", minor: "2", patch: "3", sha: SHA_A,
  });
  assert.throws(() => resolveReleaseIntent({ eventName: "push", pushSha: SHA_A, manualVersion: "", manualSha: "", releasePlease: { ...released, sha: SHA_B } }), { name: "TypeError" });
  assert.throws(() => resolveReleaseIntent({ eventName: "workflow_dispatch", pushSha: "", manualVersion: "01.2.3", manualSha: SHA_A, releasePlease: null }), { name: "TypeError" });
});

test("chart publication compares normalized trees and reuses only the exact manifest", () => {
  const base = { version: "1.2.3", localTreeDigest: DIGEST_A, remoteTreeDigest: null, remoteManifestDigest: null };
  assert.deepEqual(planChartPublication(base), { action: "publish", digest: null });
  assert.deepEqual(planChartPublication({ ...base, remoteTreeDigest: DIGEST_A, remoteManifestDigest: DIGEST_B }), { action: "reuse", digest: DIGEST_B });
  for (const remote of [
    { remoteTreeDigest: DIGEST_B, remoteManifestDigest: DIGEST_A },
    { remoteTreeDigest: DIGEST_A, remoteManifestDigest: null },
    { remoteTreeDigest: null, remoteManifestDigest: DIGEST_A },
  ]) {
    assert.throws(() => planChartPublication({ ...base, ...remote }), { name: "TypeError" });
  }
});

test("evidence publication resumes only independently missing immutable evidence", () => {
  assert.deepEqual(planEvidencePublication({ subjectDigest: DIGEST_A, signature: true, sbom: false, provenance: true }), {
    subjectDigest: DIGEST_A, signature: false, sbom: true, provenance: false,
  });
  assert.deepEqual(planEvidencePublication({ subjectDigest: DIGEST_B, signature: false, sbom: false, provenance: false }), {
    subjectDigest: DIGEST_B, signature: true, sbom: true, provenance: true,
  });
  assert.throws(() => planEvidencePublication({ subjectDigest: DIGEST_A, signature: "yes", sbom: false, provenance: false }), { name: "TypeError" });
  assert.throws(() => planEvidencePublication({ subjectDigest: DIGEST_A, signature: false, sbom: false, provenance: false, extra: true }), { name: "TypeError" });
});

test("release assets upload once, skip identical content, and never clobber", () => {
  assert.equal(planAssetPublication({ localSha256: "a".repeat(64), remoteSha256: null }), "upload");
  assert.equal(planAssetPublication({ localSha256: "a".repeat(64), remoteSha256: "a".repeat(64) }), "skip");
  assert.throws(() => planAssetPublication({ localSha256: "a".repeat(64), remoteSha256: "b".repeat(64) }), { name: "TypeError" });
});

test("normalized chart trees ignore archive order and timestamps", () => {
  const first = chartArchive([
    { name: "nvml-mock/", type: "directory", mtime: 1 },
    { name: "nvml-mock/Chart.yaml", content: "version: 1.2.3\n", mtime: 1 },
    { name: "nvml-mock/templates/a.yaml", content: "kind: ConfigMap\n", mode: 0o644, mtime: 1 },
  ]);
  const reordered = chartArchive([
    { name: "nvml-mock/templates/a.yaml", content: "kind: ConfigMap\n", mode: 0o644, mtime: 999 },
    { name: "nvml-mock/Chart.yaml", content: "version: 1.2.3\n", mtime: 999 },
    { name: "nvml-mock/", type: "directory", mtime: 999 },
  ]);
  assert.equal(normalizedChartTreeDigest(first, "nvml-mock"), normalizedChartTreeDigest(reordered, "nvml-mock"));
  assert.notEqual(normalizedChartTreeDigest(first, "nvml-mock"), normalizedChartTreeDigest(chartArchive([
    { name: "nvml-mock/", type: "directory" },
    { name: "nvml-mock/Chart.yaml", content: "version: 9.9.9\n" },
  ]), "nvml-mock"));
});

test("normalized chart trees reject unsafe archive entries", () => {
  for (const entries of [
    [{ name: "nvml-mock/../escape", content: "bad" }],
    [{ name: "/nvml-mock/Chart.yaml", content: "bad" }],
    [{ name: "other/Chart.yaml", content: "bad" }],
    [{ name: "nvml-mock/link", type: "symlink", linkname: "/etc/passwd" }],
    [{ name: "nvml-mock/device", type: "character" }],
    [{ name: "nvml-mock/Chart.yaml", content: "a" }, { name: "nvml-mock/Chart.yaml", content: "b" }],
  ]) {
    assert.throws(() => normalizedChartTreeDigest(chartArchive(entries), "nvml-mock"), { name: "TypeError" });
  }
});
