import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
import { gunzipSync } from "node:zlib";

const VERSION_RE = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/;
const SHA_RE = /^[0-9a-f]{40}$/;
const DIGEST_RE = /^sha256:[0-9a-f]{64}$/;
const HEX_SHA256_RE = /^[0-9a-f]{64}$/;
const MAX_ARCHIVE_BYTES = 64 * 1024 * 1024;

function fail(message) {
  throw new TypeError(message);
}

function plainObject(value, name) {
  if (value === null || typeof value !== "object" || Array.isArray(value) || Object.getPrototypeOf(value) !== Object.prototype) {
    fail(`${name} must be a plain object`);
  }
  return value;
}

function exactKeys(value, allowed, name) {
  for (const key of Object.keys(value)) {
    if (!allowed.includes(key)) fail(`${name} contains an unknown field`);
  }
}

function canonicalVersion(value, name = "version") {
  if (typeof value !== "string") fail(`${name} must be canonical SemVer`);
  const match = VERSION_RE.exec(value);
  if (!match) fail(`${name} must be canonical SemVer`);
  return { version: value, major: match[1], minor: match[2], patch: match[3] };
}

function fullSha(value, name) {
  if (typeof value !== "string" || !SHA_RE.test(value)) fail(`${name} must be a full lowercase commit SHA`);
  return value;
}

export function validateDefaultBranch(value) {
  if (typeof value !== "string" || value.length === 0 || value.length > 255 ||
      !/^[A-Za-z0-9][A-Za-z0-9._/-]*$/.test(value) || value.includes("..") || value.includes("//") ||
      value.includes("@{") || value.endsWith(".") || value.endsWith(".lock") ||
      value.split("/").some((part) => part.length === 0 || part.startsWith(".") || part.endsWith("."))) {
    fail("default branch is invalid");
  }
  return value;
}

function digest(value, name) {
  if (typeof value !== "string" || !DIGEST_RE.test(value)) fail(`${name} must be a sha256 digest`);
  return value;
}

function compareVersions(left, right) {
  const a = canonicalVersion(left);
  const b = canonicalVersion(right);
  for (const key of ["major", "minor", "patch"]) {
    const av = BigInt(a[key]);
    const bv = BigInt(b[key]);
    if (av < bv) return -1;
    if (av > bv) return 1;
  }
  return 0;
}

export function validateReleaseBinding(options) {
  const value = plainObject(options, "release binding");
  const fields = [
    "tagName", "major", "minor", "patch", "releaseSha", "peeledTagSha", "checkoutSha",
    "chartVersion", "chartAppVersion", "imageSourceRevision", "githubReleaseTag", "githubReleaseTarget",
  ];
  exactKeys(value, fields, "release binding");
  for (const field of fields) {
    if (!Object.hasOwn(value, field)) fail(`release binding is missing ${field}`);
  }
  if (typeof value.tagName !== "string" || !value.tagName.startsWith("v")) fail("tagName must be canonical");
  const parsed = canonicalVersion(value.tagName.slice(1), "tagName");
  if (value.major !== parsed.major || value.minor !== parsed.minor || value.patch !== parsed.patch) fail("release components do not match tagName");
  if (value.chartVersion !== parsed.version || value.chartAppVersion !== parsed.version) fail("chart version does not match tagName");
  if (value.githubReleaseTag !== value.tagName) fail("GitHub release tag does not match tagName");
  const releaseSha = fullSha(value.releaseSha, "releaseSha");
  for (const [name, candidate] of [
    ["peeledTagSha", value.peeledTagSha],
    ["checkoutSha", value.checkoutSha],
    ["imageSourceRevision", value.imageSourceRevision],
    ["githubReleaseTarget", value.githubReleaseTarget],
  ]) {
    if (fullSha(candidate, name) !== releaseSha) fail(`${name} does not match releaseSha`);
  }
  return { version: parsed.version, tagName: value.tagName, sha: releaseSha };
}

function stableRecord(value, releaseSha) {
  const record = plainObject(value, "stable image state");
  exactKeys(record, ["digest", "sourceRevision", "signature", "sbom", "provenance"], "stable image state");
  const stableDigest = digest(record.digest, "stable digest");
  if (fullSha(record.sourceRevision, "stable source revision") !== releaseSha) fail("stable source revision mismatch");
  for (const field of ["signature", "sbom", "provenance"]) {
    if (Object.hasOwn(record, field) && typeof record[field] !== "boolean") fail(`stable ${field} must be boolean`);
  }
  return { ...record, digest: stableDigest };
}

function aliasAction(value, targetVersion, targetDigest) {
  if (value === null) return "update";
  const record = plainObject(value, "alias state");
  exactKeys(record, ["digest", "version"], "alias state");
  const remoteDigest = digest(record.digest, "alias digest");
  canonicalVersion(record.version, "alias version");
  const order = compareVersions(record.version, targetVersion);
  if (order > 0) fail("alias points to a newer version");
  if (order === 0 && remoteDigest !== targetDigest) fail("equal alias version has a different digest");
  if (remoteDigest === targetDigest) return "skip";
  return "update";
}

function shortTagAction(value, releaseSha, stableDigest) {
  if (value === null) return stableDigest === null ? "defer" : "update";
  const record = plainObject(value, "short-SHA tag state");
  exactKeys(record, ["digest", "sourceRevision"], "short-SHA tag state");
  const remoteDigest = digest(record.digest, "short-SHA tag digest");
  if (fullSha(record.sourceRevision, "short-SHA source revision") !== releaseSha) {
    fail("short-SHA tag source revision collision");
  }
  if (stableDigest === null) return "preserve";
  return remoteDigest === stableDigest ? "skip" : "update";
}

export function planImagePublication(options) {
  const value = plainObject(options, "image publication state");
  exactKeys(value, ["version", "releaseSha", "stable", "short", "minor", "major", "latest"], "image publication state");
  const parsed = canonicalVersion(value.version);
  const releaseSha = fullSha(value.releaseSha, "releaseSha");
  for (const field of ["stable", "short", "minor", "major", "latest"]) {
    if (!Object.hasOwn(value, field)) fail(`image publication state is missing ${field}`);
  }
  if (value.stable === null) {
    for (const alias of [value.minor, value.major, value.latest]) {
      if (alias !== null) {
        const record = plainObject(alias, "alias state");
        exactKeys(record, ["digest", "version"], "alias state");
        digest(record.digest, "alias digest");
        canonicalVersion(record.version, "alias version");
      }
    }
    return {
      immutable: { action: "publish", digest: null },
      development: { short: shortTagAction(value.short, releaseSha, null) },
      aliases: { minor: "defer", major: "defer", latest: "defer" },
      resume: { signature: true, sbom: true, provenance: true },
    };
  }
  const stable = stableRecord(value.stable, releaseSha);
  return {
    immutable: { action: "reuse", digest: stable.digest },
    development: { short: shortTagAction(value.short, releaseSha, stable.digest) },
    aliases: {
      minor: aliasAction(value.minor, parsed.version, stable.digest),
      major: aliasAction(value.major, parsed.version, stable.digest),
      latest: aliasAction(value.latest, parsed.version, stable.digest),
    },
    resume: {
      signature: stable.signature !== true,
      sbom: stable.sbom !== true,
      provenance: stable.provenance !== true,
    },
  };
}

function developmentRecord(value, name) {
  const record = plainObject(value, name);
  exactKeys(record, ["digest", "sourceRevision"], name);
  return {
    digest: digest(record.digest, `${name} digest`),
    sourceRevision: fullSha(record.sourceRevision, `${name} source revision`),
  };
}

export function classifyEdgeAncestry(options, isAncestor) {
  const value = plainObject(options, "edge ancestry input");
  exactKeys(value, ["edgeSha", "releaseSha"], "edge ancestry input");
  const releaseSha = fullSha(value.releaseSha, "edge target SHA");
  if (value.edgeSha === null) return "absent";
  const edgeSha = fullSha(value.edgeSha, "edge source SHA");
  if (typeof isAncestor !== "function") fail("edge ancestry resolver must be a function");
  if (edgeSha === releaseSha) return "equal";
  if (isAncestor(edgeSha, releaseSha) === true) return "ancestor";
  if (isAncestor(releaseSha, edgeSha) === true) return "descendant";
  return "unrelated";
}

export function planDevelopmentPublication(options) {
  const value = plainObject(options, "development publication state");
  exactKeys(value, ["releaseSha", "immutable", "short", "edge", "edgeRelation"], "development publication state");
  for (const field of ["releaseSha", "immutable", "short", "edge", "edgeRelation"]) {
    if (!Object.hasOwn(value, field)) fail(`development publication state is missing ${field}`);
  }
  const releaseSha = fullSha(value.releaseSha, "releaseSha");
  let immutable;
  if (value.immutable === null) {
    immutable = { action: "publish", digest: null };
  } else {
    const record = developmentRecord(value.immutable, "immutable SHA state");
    if (record.sourceRevision !== releaseSha) fail("immutable SHA source revision mismatch");
    immutable = { action: "reuse", digest: record.digest };
  }
  const targetDigest = immutable.digest;
  let short;
  if (value.short === null) {
    short = targetDigest === null ? "defer" : "update";
  } else {
    const record = developmentRecord(value.short, "short-SHA tag state");
    if (record.sourceRevision !== releaseSha) fail("short-SHA tag source revision collision");
    short = targetDigest === null ? "preserve" : record.digest === targetDigest ? "skip" : "update";
  }
  let edge;
  if (value.edge === null) {
    if (value.edgeRelation !== "absent") fail("absent edge tag has an invalid ancestry relation");
    edge = targetDigest === null ? "defer" : "update";
  } else {
    const record = developmentRecord(value.edge, "edge tag state");
    if (!["equal", "ancestor", "descendant", "unrelated"].includes(value.edgeRelation)) fail("edge ancestry relation is invalid");
    if (value.edgeRelation === "equal" && record.sourceRevision !== releaseSha) fail("equal edge ancestry has a different source revision");
    if (value.edgeRelation !== "equal" && record.sourceRevision === releaseSha) fail("edge ancestry does not match its source revision");
    if (record.digest === targetDigest && value.edgeRelation !== "equal") fail("edge digest identity conflicts with its source ancestry");
    if (value.edgeRelation === "descendant") fail("edge points to a newer default-branch commit");
    if (value.edgeRelation === "unrelated") fail("edge points outside the target default-branch ancestry");
    edge = targetDigest === null ? "defer" : record.digest === targetDigest ? "skip" : "update";
  }
  return { immutable, short, edge };
}

export function bindFinalDigest(options) {
  const value = plainObject(options, "final digest binding");
  exactKeys(value, ["selectedDigest", "finalDigest"], "final digest binding");
  if (!Object.hasOwn(value, "selectedDigest") || !Object.hasOwn(value, "finalDigest")) fail("final digest binding is incomplete");
  const selected = digest(value.selectedDigest, "selected digest");
  const final = digest(value.finalDigest, "final digest");
  if (selected !== final) fail("final immutable digest changed after selection");
  return final;
}

export function planImmutableImagePromotion(options) {
  const value = plainObject(options, "immutable image promotion state");
  exactKeys(value, ["initialDigest", "stagedDigest", "finalDigest"], "immutable image promotion state");
  for (const field of ["initialDigest", "stagedDigest", "finalDigest"]) {
    if (!Object.hasOwn(value, field)) fail(`immutable image promotion state is missing ${field}`);
    if (value[field] !== null) digest(value[field], field);
  }
  if (value.initialDigest === null) {
    if (value.stagedDigest === null) fail("absent immutable image requires a staged digest");
    if (value.finalDigest === null) return { action: "update", digest: value.stagedDigest };
    if (value.finalDigest !== value.stagedDigest) fail("immutable image tag appeared with a different digest");
    return { action: "skip", digest: value.stagedDigest };
  }
  if (value.stagedDigest !== null) fail("reused immutable image cannot contain a staged digest");
  if (value.finalDigest !== value.initialDigest) fail("reused immutable image digest changed before publication");
  return { action: "skip", digest: value.initialDigest };
}

export function resolveReleaseIntent(options) {
  const value = plainObject(options, "release intent");
  exactKeys(value, ["eventName", "pushSha", "manualVersion", "manualSha", "releasePlease"], "release intent");
  for (const field of ["eventName", "pushSha", "manualVersion", "manualSha", "releasePlease"]) {
    if (!Object.hasOwn(value, field)) fail(`release intent is missing ${field}`);
  }
  if (value.eventName === "workflow_dispatch") {
    const parsed = canonicalVersion(value.manualVersion, "manual version");
    const sha = fullSha(value.manualSha, "manual SHA");
    if (value.releasePlease !== null) fail("manual release intent cannot contain Release Please state");
    return {
      mode: "release", version: parsed.version, tagName: `v${parsed.version}`,
      major: parsed.major, minor: parsed.minor, patch: parsed.patch, sha,
    };
  }
  if (value.eventName !== "push") fail("unsupported release event");
  const pushSha = fullSha(value.pushSha, "push SHA");
  if (value.manualVersion !== "" || value.manualSha !== "") fail("push intent cannot contain manual identity");
  const releasePlease = plainObject(value.releasePlease, "Release Please outputs");
  if (releasePlease.releaseCreated === "false") {
    exactKeys(releasePlease, ["releaseCreated"], "Release Please outputs");
    return { mode: "development", version: "", tagName: "", major: "", minor: "", patch: "", sha: pushSha };
  }
  exactKeys(releasePlease, ["releaseCreated", "tagName", "major", "minor", "patch", "sha"], "Release Please outputs");
  if (releasePlease.releaseCreated !== "true") fail("releaseCreated must be true or false");
  if (typeof releasePlease.tagName !== "string" || !releasePlease.tagName.startsWith("v")) fail("Release Please tag is invalid");
  const parsed = canonicalVersion(releasePlease.tagName.slice(1), "Release Please tag");
  if (releasePlease.major !== parsed.major || releasePlease.minor !== parsed.minor || releasePlease.patch !== parsed.patch) {
    fail("Release Please version components mismatch");
  }
  const sha = fullSha(releasePlease.sha, "Release Please SHA");
  if (sha !== pushSha) fail("Release Please SHA does not match push SHA");
  return {
    mode: "release", version: parsed.version, tagName: releasePlease.tagName,
    major: parsed.major, minor: parsed.minor, patch: parsed.patch, sha,
  };
}

export function planChartPublication(options) {
  const value = plainObject(options, "chart publication state");
  exactKeys(value, ["version", "localTreeDigest", "remoteTreeDigest", "remoteManifestDigest"], "chart publication state");
  canonicalVersion(value.version);
  const local = digest(value.localTreeDigest, "local tree digest");
  const remoteTree = value.remoteTreeDigest;
  const remoteManifest = value.remoteManifestDigest;
  if (remoteTree === null && remoteManifest === null) return { action: "publish", digest: null };
  if (remoteTree === null || remoteManifest === null) fail("remote chart state is partial");
  if (digest(remoteTree, "remote tree digest") !== local) fail("immutable chart content mismatch");
  return { action: "reuse", digest: digest(remoteManifest, "remote manifest digest") };
}

export function planAssetPublication(options) {
  const value = plainObject(options, "release asset state");
  exactKeys(value, ["localSha256", "remoteSha256"], "release asset state");
  if (typeof value.localSha256 !== "string" || !HEX_SHA256_RE.test(value.localSha256)) fail("local asset checksum is invalid");
  if (value.remoteSha256 === null) return "upload";
  if (typeof value.remoteSha256 !== "string" || !HEX_SHA256_RE.test(value.remoteSha256)) fail("remote asset checksum is invalid");
  if (value.localSha256 !== value.remoteSha256) fail("release asset content mismatch");
  return "skip";
}

export function planEvidencePublication(options) {
  const value = plainObject(options, "evidence state");
  exactKeys(value, ["subjectDigest", "signature", "sbom", "provenance"], "evidence state");
  if (!Object.hasOwn(value, "subjectDigest")) fail("evidence state is missing subjectDigest");
  const result = { subjectDigest: digest(value.subjectDigest, "evidence subject digest") };
  for (const field of ["signature", "sbom", "provenance"]) {
    if (typeof value[field] !== "boolean") fail(`evidence ${field} must be boolean`);
    result[field] = !value[field];
  }
  return result;
}

function tarOctal(buffer, offset, length, name) {
  const raw = buffer.subarray(offset, offset + length).toString("ascii").replace(/\0.*$/, "").trim();
  if (!/^[0-7]+$/.test(raw)) fail(`invalid tar ${name}`);
  const value = Number.parseInt(raw, 8);
  if (!Number.isSafeInteger(value) || value < 0) fail(`invalid tar ${name}`);
  return value;
}

function tarString(buffer, offset, length) {
  return buffer.subarray(offset, offset + length).toString("utf8").replace(/\0.*$/, "");
}

function frame(hash, value) {
  const bytes = Buffer.isBuffer(value) ? value : Buffer.from(String(value));
  const length = Buffer.alloc(8);
  length.writeBigUInt64BE(BigInt(bytes.length));
  hash.update(length);
  hash.update(bytes);
}

export function normalizedChartTreeDigest(archive, expectedRoot) {
  if (!Buffer.isBuffer(archive) || archive.length === 0 || archive.length > MAX_ARCHIVE_BYTES) fail("chart archive is invalid");
  if (typeof expectedRoot !== "string" || !/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/.test(expectedRoot)) fail("expected chart root is invalid");
  let tar;
  try {
    tar = gunzipSync(archive, { maxOutputLength: MAX_ARCHIVE_BYTES });
  } catch {
    fail("chart archive is not a bounded gzip stream");
  }
  const records = [];
  const paths = new Set();
  let offset = 0;
  while (offset + 512 <= tar.length) {
    const header = tar.subarray(offset, offset + 512);
    if (header.every((byte) => byte === 0)) break;
    const storedChecksum = tarOctal(header, 148, 8, "checksum");
    const checksumHeader = Buffer.from(header);
    checksumHeader.fill(0x20, 148, 156);
    const computedChecksum = [...checksumHeader].reduce((sum, byte) => sum + byte, 0);
    if (storedChecksum !== computedChecksum) fail("tar checksum mismatch");
    const name = tarString(header, 0, 100);
    const prefix = tarString(header, 345, 155);
    const fullName = prefix ? `${prefix}/${name}` : name;
    if (!fullName || fullName.includes("\\") || fullName.startsWith("/")) fail("unsafe chart archive path");
    const segments = fullName.replace(/\/$/, "").split("/");
    if (segments.some((segment) => segment === "" || segment === "." || segment === "..")) fail("unsafe chart archive path");
    if (segments[0] !== expectedRoot) fail("chart archive has an unexpected root");
    const relativePath = segments.slice(1).join("/");
    if (paths.has(relativePath)) fail("chart archive contains a duplicate path");
    paths.add(relativePath);
    const typeFlag = String.fromCharCode(header[156] || 0x30);
    if (typeFlag !== "0" && typeFlag !== "5") fail("chart archive contains a non-regular entry");
    const type = typeFlag === "5" ? "directory" : "file";
    const size = tarOctal(header, 124, 12, "size");
    const mode = tarOctal(header, 100, 8, "mode") & 0o7777;
    if (type === "directory" && size !== 0) fail("chart directory has content");
    const dataStart = offset + 512;
    const dataEnd = dataStart + size;
    if (dataEnd > tar.length) fail("truncated chart archive entry");
    records.push({ path: relativePath, type, mode, content: Buffer.from(tar.subarray(dataStart, dataEnd)) });
    offset = dataStart + Math.ceil(size / 512) * 512;
  }
  if (records.length === 0) fail("chart archive is empty");
  records.sort((a, b) => Buffer.compare(Buffer.from(a.path), Buffer.from(b.path)));
  const hash = createHash("sha256");
  for (const record of records) {
    frame(hash, record.path);
    frame(hash, record.type);
    frame(hash, record.mode.toString(8));
    frame(hash, record.content.length);
    frame(hash, record.content);
  }
  return `sha256:${hash.digest("hex")}`;
}

function planFromChart(chartPath, sourceSha) {
  const source = readFileSync(chartPath, "utf8");
  const version = /^version:\s*["']?([^\s"']+)["']?\s*$/m.exec(source)?.[1];
  const appVersion = /^appVersion:\s*["']?([^\s"']+)["']?\s*$/m.exec(source)?.[1];
  const parsed = canonicalVersion(version, "chart version");
  if (appVersion !== parsed.version) fail("chart appVersion does not match version");
  const sha = fullSha(sourceSha, "source SHA");
  return {
    publish: false,
    image: "ghcr.io/nvidia/nvml-mock",
    chart: "oci://ghcr.io/nvidia/charts/nvml-mock",
    stableTags: [parsed.version, `${parsed.major}.${parsed.minor}`, parsed.major, "latest"],
    developmentTags: ["edge", `sha-${sha.slice(0, 12)}`],
    digestTargets: ["ghcr.io/nvidia/nvml-mock@sha256:<digest>", "ghcr.io/nvidia/charts/nvml-mock@sha256:<digest>"],
  };
}

async function main(argv) {
  const [command, inputPath, extra] = argv;
  if (command === "plan") return planFromChart(inputPath, process.env.SOURCE_SHA);
  if (command === "tree") return { digest: normalizedChartTreeDigest(readFileSync(inputPath), extra) };
  if (!["binding", "image", "development", "intent", "chart", "asset", "evidence", "final-digest", "immutable-promotion"].includes(command)) fail("unsupported release-state command");
  const input = JSON.parse(readFileSync(inputPath, "utf8"));
  return {
    binding: validateReleaseBinding,
    image: planImagePublication,
    development: planDevelopmentPublication,
    intent: resolveReleaseIntent,
    chart: planChartPublication,
    asset: planAssetPublication,
    evidence: planEvidencePublication,
    "final-digest": bindFinalDigest,
    "immutable-promotion": planImmutableImagePromotion,
  }[command](input);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const result = await main(process.argv.slice(2));
    process.stdout.write(`${JSON.stringify(result)}\n`);
  } catch (error) {
    process.stderr.write(`release-state: ${error instanceof Error ? error.message : "failed"}\n`);
    process.exitCode = 1;
  }
}
