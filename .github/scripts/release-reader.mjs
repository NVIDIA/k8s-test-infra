import { readFileSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

import { normalizedChartTreeDigest } from "./release-state.mjs";

const DIGEST_RE = /^sha256:[0-9a-f]{64}$/;
const SHA_RE = /^[0-9a-f]{40}$/;
const VERSION_RE = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/;
const REPOSITORY_RE = /^[a-z0-9](?:[a-z0-9._-]*)(?:\/[a-z0-9](?:[a-z0-9._-]*))*$/;
const MAX_JSON_BYTES = 4 * 1024 * 1024;
const MAX_RESPONSE_BYTES = 64 * 1024 * 1024;
const MANIFEST_ACCEPT = [
  "application/vnd.oci.image.index.v1+json",
  "application/vnd.oci.image.manifest.v1+json",
  "application/vnd.docker.distribution.manifest.list.v2+json",
  "application/vnd.docker.distribution.manifest.v2+json",
].join(", ");

function fail(message) {
  throw new TypeError(message);
}

function object(value, name) {
  if (value === null || typeof value !== "object" || Array.isArray(value) || Object.getPrototypeOf(value) !== Object.prototype) {
    fail(`${name} must be a plain object`);
  }
  return value;
}

function exactKeys(value, allowed, name) {
  for (const key of Object.keys(value)) if (!allowed.includes(key)) fail(`${name} contains an unknown field`);
}

function digest(value, name) {
  if (typeof value !== "string" || !DIGEST_RE.test(value)) fail(`${name} must be a sha256 digest`);
  return value;
}

function sha(value, name) {
  if (typeof value !== "string" || !SHA_RE.test(value)) fail(`${name} must be a full lowercase commit SHA`);
  return value;
}

function version(value, name = "version") {
  if (typeof value !== "string" || !VERSION_RE.test(value)) fail(`${name} must be canonical SemVer`);
  return value;
}

function repository(value) {
  if (typeof value !== "string" || !REPOSITORY_RE.test(value)) fail("OCI repository is invalid");
  return value;
}

function bodyBuffer(response, name, maximum = MAX_JSON_BYTES) {
  if (!response || !Number.isSafeInteger(response.status) || !(response.body instanceof Uint8Array)) fail(`${name} response is invalid`);
  const body = Buffer.from(response.body);
  if (body.length > maximum) fail(`${name} response is too large`);
  return body;
}

function jsonResponse(response, name) {
  const body = bodyBuffer(response, name);
  try {
    return object(JSON.parse(body.toString("utf8")), name);
  } catch (error) {
    if (error instanceof TypeError) throw error;
    fail(`${name} is not valid JSON`);
  }
}

function header(response, name) {
  const value = response.headers?.get?.(name) ?? response.headers?.get?.(name.toLowerCase());
  return value ?? null;
}

function descriptor(value, name) {
  const result = object(value, name);
  exactKeys(result, ["mediaType", "digest", "size", "platform", "annotations", "artifactType"], name);
  digest(result.digest, `${name} digest`);
  if (typeof result.mediaType !== "string" || result.mediaType.length > 200) fail(`${name} media type is invalid`);
  if (!Number.isSafeInteger(result.size) || result.size < 0) fail(`${name} size is invalid`);
  return result;
}

async function readConfig(request, repo, configDescriptor, expectedPlatform) {
  descriptor(configDescriptor, "OCI config descriptor");
  if (configDescriptor.mediaType !== "application/vnd.oci.image.config.v1+json" &&
      configDescriptor.mediaType !== "application/vnd.docker.container.image.v1+json") {
    fail("OCI image config media type is invalid");
  }
  const response = await request({
    url: `https://ghcr.io/v2/${repo}/blobs/${configDescriptor.digest}`,
    headers: { accept: configDescriptor.mediaType },
  });
  if (response.status !== 200) fail("OCI config blob read failed");
  const config = jsonResponse(response, "OCI image config");
  exactKeys(config, ["architecture", "author", "config", "container", "container_config", "created", "docker_version", "history", "os", "rootfs", "variant"], "OCI image config");
  if (typeof config.os !== "string" || typeof config.architecture !== "string" || `${config.os}/${config.architecture}` !== expectedPlatform) {
    fail("OCI image config platform does not match its index descriptor");
  }
  const runtime = object(config.config, "OCI runtime config");
  const labels = object(runtime.Labels, "OCI image labels");
  const sourceRevision = sha(labels["org.opencontainers.image.revision"], "OCI source revision label");
  const imageVersion = version(labels["org.opencontainers.image.version"], "OCI version label");
  return { sourceRevision, version: imageVersion };
}

async function readImageManifest(request, repo, reference, expectedDigest = null, expectedPlatform = null) {
  const response = await request({
    url: `https://ghcr.io/v2/${repo}/manifests/${encodeURIComponent(reference)}`,
    headers: { accept: MANIFEST_ACCEPT },
  });
  if (response.status === 404 && expectedDigest === null) return null;
  if (response.status !== 200) fail("OCI manifest read failed");
  const manifestDigest = digest(header(response, "docker-content-digest"), "OCI manifest response digest");
  if (expectedDigest !== null && manifestDigest !== expectedDigest) fail("OCI descriptor digest mismatch");
  const manifest = jsonResponse(response, "OCI manifest");
  if (manifest.schemaVersion !== 2 || typeof manifest.mediaType !== "string") fail("OCI manifest schema is invalid");
  if (manifest.mediaType === "application/vnd.oci.image.index.v1+json" ||
      manifest.mediaType === "application/vnd.docker.distribution.manifest.list.v2+json") {
    if (expectedDigest !== null) fail("nested OCI image indexes are not allowed");
    exactKeys(manifest, ["schemaVersion", "mediaType", "manifests", "annotations"], "OCI image index");
    if (!Array.isArray(manifest.manifests) || manifest.manifests.length === 0 || manifest.manifests.length > 16) fail("OCI image index is invalid");
    const runnable = new Map();
    const attestations = new Set();
    const descriptorDigests = new Set();
    for (const item of manifest.manifests) {
      const child = descriptor(item, "OCI platform descriptor");
      if (descriptorDigests.has(child.digest)) fail("OCI image index contains a duplicate descriptor digest");
      descriptorDigests.add(child.digest);
      const platform = object(child.platform, "OCI descriptor platform");
      exactKeys(platform, ["os", "architecture"], "OCI descriptor platform");
      const platformName = `${platform.os}/${platform.architecture}`;
      if (platformName === "linux/amd64" || platformName === "linux/arm64") {
        if (child.annotations !== undefined || child.artifactType !== undefined) fail("runnable OCI descriptor contains evidence metadata");
        if (!["application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json"].includes(child.mediaType)) {
          fail("runnable OCI descriptor media type is invalid");
        }
        if (runnable.has(platformName)) fail("OCI image index contains a duplicate runnable platform");
        runnable.set(platformName, child);
        continue;
      }
      if (platformName !== "unknown/unknown") fail("OCI image index contains an unsupported runnable platform");
      if (child.mediaType !== "application/vnd.oci.image.manifest.v1+json" || child.artifactType !== undefined) {
        fail("Buildx attestation descriptor shape is invalid");
      }
      const annotations = object(child.annotations, "Buildx attestation annotations");
      exactKeys(annotations, ["vnd.docker.reference.digest", "vnd.docker.reference.type"], "Buildx attestation annotations");
      if (annotations["vnd.docker.reference.type"] !== "attestation-manifest") fail("OCI unknown platform descriptor is not a Buildx attestation");
      const subject = digest(annotations["vnd.docker.reference.digest"], "Buildx attestation subject");
      if (attestations.has(subject)) fail("OCI image index contains ambiguous Buildx attestations");
      attestations.add(subject);
    }
    if (runnable.size !== 2 || !runnable.has("linux/amd64") || !runnable.has("linux/arm64")) {
      fail("OCI image index must contain exactly linux/amd64 and linux/arm64");
    }
    for (const subject of attestations) {
      if (![...runnable.values()].some((item) => item.digest === subject)) fail("Buildx attestation subject is not a runnable platform");
    }
    let identity = null;
    for (const [platformName, child] of runnable.entries()) {
      const current = await readImageManifest(request, repo, child.digest, child.digest, platformName);
      if (current === null) fail("OCI platform manifest is absent");
      const candidate = { sourceRevision: current.sourceRevision, version: current.version };
      if (identity !== null && (identity.sourceRevision !== candidate.sourceRevision || identity.version !== candidate.version)) {
        fail("OCI platform image identities differ");
      }
      identity = candidate;
    }
    return { digest: manifestDigest, ...identity };
  }
  if (expectedDigest === null) fail("OCI image tag must resolve to a multi-platform index");
  if (manifest.mediaType !== "application/vnd.oci.image.manifest.v1+json" &&
      manifest.mediaType !== "application/vnd.docker.distribution.manifest.v2+json") {
    fail("OCI image manifest media type is invalid");
  }
  exactKeys(manifest, ["schemaVersion", "mediaType", "artifactType", "config", "layers", "subject", "annotations"], "OCI manifest");
  if (!Array.isArray(manifest.layers) || manifest.layers.length > 256) fail("OCI image layers are invalid");
  if (expectedPlatform === null) fail("OCI platform expectation is missing");
  const identity = await readConfig(request, repo, manifest.config, expectedPlatform);
  return { digest: manifestDigest, ...identity };
}

function evidence(value) {
  const result = object(value, "image evidence");
  exactKeys(result, ["subjectDigest", "signature", "sbom", "provenance"], "image evidence");
  if (!Object.hasOwn(result, "subjectDigest")) fail("image evidence is missing subjectDigest");
  if (result.subjectDigest !== null) digest(result.subjectDigest, "image evidence subject digest");
  for (const key of ["signature", "sbom", "provenance"]) if (typeof result[key] !== "boolean") fail(`image evidence ${key} must be boolean`);
  return result;
}

export async function gatherImageIdentityState(options) {
  const value = object(options, "image reader options");
  exactKeys(value, ["request", "repository", "version", "releaseSha"], "image reader options");
  if (typeof value.request !== "function") fail("image reader request must be a function");
  const repo = repository(value.repository);
  const targetVersion = version(value.version);
  const releaseSha = sha(value.releaseSha, "release SHA");
  const [stable, short, minor, major, latest] = await Promise.all([
    readImageManifest(value.request, repo, targetVersion),
    readImageManifest(value.request, repo, `sha-${releaseSha.slice(0, 12)}`),
    readImageManifest(value.request, repo, targetVersion.split(".").slice(0, 2).join(".")),
    readImageManifest(value.request, repo, targetVersion.split(".")[0]),
    readImageManifest(value.request, repo, "latest"),
  ]);
  if (short !== null && short.sourceRevision !== releaseSha) fail("short-SHA tag source revision collision");
  return {
    version: targetVersion,
    releaseSha,
    stable: stable === null ? null : { digest: stable.digest, sourceRevision: stable.sourceRevision },
    short: short === null ? null : { digest: short.digest, sourceRevision: short.sourceRevision },
    minor: minor === null ? null : { digest: minor.digest, version: minor.version },
    major: major === null ? null : { digest: major.digest, version: major.version },
    latest: latest === null ? null : { digest: latest.digest, version: latest.version },
  };
}

export async function gatherImageState(options) {
  const value = object(options, "image reader options");
  exactKeys(value, ["request", "repository", "version", "releaseSha", "evidence"], "image reader options");
  const proof = evidence(value.evidence);
  const identity = await gatherImageIdentityState({
    request: value.request, repository: value.repository, version: value.version, releaseSha: value.releaseSha,
  });
  if (identity.stable === null) {
    if (proof.subjectDigest !== null || proof.signature || proof.sbom || proof.provenance) fail("evidence exists without an immutable image subject");
    return identity;
  }
  if (proof.subjectDigest !== identity.stable.digest) fail("image evidence subject does not match the immutable image digest");
  const booleans = { signature: proof.signature, sbom: proof.sbom, provenance: proof.provenance };
  return { ...identity, stable: { ...identity.stable, ...booleans } };
}

export async function gatherDevelopmentState(options) {
  const value = object(options, "development reader options");
  exactKeys(value, ["request", "repository", "releaseSha"], "development reader options");
  if (typeof value.request !== "function") fail("development reader request must be a function");
  const repo = repository(value.repository);
  const releaseSha = sha(value.releaseSha, "release SHA");
  const fullTag = `sha-${releaseSha}`;
  const [immutable, short, edge] = await Promise.all([
    readImageManifest(value.request, repo, fullTag),
    readImageManifest(value.request, repo, `sha-${releaseSha.slice(0, 12)}`),
    readImageManifest(value.request, repo, "edge"),
  ]);
  const project = (item) => item === null ? null : { digest: item.digest, sourceRevision: item.sourceRevision };
  return { releaseSha, immutable: project(immutable), short: project(short), edge: project(edge) };
}

export async function gatherChartState(options) {
  const value = object(options, "chart reader options");
  exactKeys(value, ["request", "repository", "version", "chartName"], "chart reader options");
  if (typeof value.request !== "function") fail("chart reader request must be a function");
  const repo = repository(value.repository);
  const targetVersion = version(value.version);
  if (typeof value.chartName !== "string" || !/^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$/.test(value.chartName)) fail("chart name is invalid");
  const response = await value.request({ url: `https://ghcr.io/v2/${repo}/manifests/${targetVersion}`, headers: { accept: MANIFEST_ACCEPT } });
  if (response.status === 404) return { remoteManifestDigest: null, remoteTreeDigest: null, archive: null };
  if (response.status !== 200) fail("OCI chart manifest read failed");
  const manifestDigest = digest(header(response, "docker-content-digest"), "OCI chart manifest digest");
  const manifest = jsonResponse(response, "OCI chart manifest");
  exactKeys(manifest, ["schemaVersion", "mediaType", "config", "layers", "annotations"], "OCI chart manifest");
  if (manifest.schemaVersion !== 2 || manifest.mediaType !== "application/vnd.oci.image.manifest.v1+json") fail("OCI chart manifest is invalid");
  descriptor(manifest.config, "OCI chart config");
  if (manifest.config.mediaType !== "application/vnd.cncf.helm.config.v1+json") fail("OCI chart config media type is invalid");
  if (!Array.isArray(manifest.layers) || manifest.layers.length !== 1) fail("OCI chart must contain one content layer");
  const layer = descriptor(manifest.layers[0], "OCI chart layer");
  if (layer.mediaType !== "application/vnd.cncf.helm.chart.content.v1.tar+gzip") fail("OCI chart layer media type is invalid");
  const blob = await value.request({ url: `https://ghcr.io/v2/${repo}/blobs/${layer.digest}`, headers: { accept: layer.mediaType } });
  if (blob.status !== 200) fail("OCI chart content read failed");
  const archive = bodyBuffer(blob, "OCI chart content", MAX_RESPONSE_BYTES);
  if (archive.length !== layer.size) fail("OCI chart content length mismatch");
  return { remoteManifestDigest: manifestDigest, remoteTreeDigest: normalizedChartTreeDigest(archive, value.chartName), archive };
}

export async function gatherGitHubReleaseState(options) {
  const value = object(options, "GitHub release reader options");
  exactKeys(value, ["request", "owner", "repository", "tagName", "releaseSha"], "GitHub release reader options");
  if (typeof value.request !== "function") fail("GitHub release request must be a function");
  if (value.owner !== "NVIDIA" || value.repository !== "k8s-test-infra") fail("GitHub repository identity is invalid");
  if (typeof value.tagName !== "string" || !value.tagName.startsWith("v")) fail("GitHub release tag is invalid");
  version(value.tagName.slice(1), "GitHub release tag");
  const releaseSha = sha(value.releaseSha, "release SHA");
  const response = await value.request({
    url: `https://api.github.com/repos/NVIDIA/k8s-test-infra/releases/tags/${encodeURIComponent(value.tagName)}`,
    headers: { accept: "application/vnd.github+json", "x-github-api-version": "2022-11-28" },
  });
  if (response.status !== 200) fail("GitHub release read failed");
  const release = jsonResponse(response, "GitHub release");
  if (release.tag_name !== value.tagName || release.target_commitish !== releaseSha) fail("GitHub release identity mismatch");
  if (!Array.isArray(release.assets) || release.assets.length > 1000) fail("GitHub release assets are invalid");
  const fixed = new Map([["image-sbom.spdx.json", "image"], ["chart-sbom.spdx.json", "chart"]]);
  const assets = { image: null, chart: null };
  for (const raw of release.assets) {
    const asset = object(raw, "GitHub release asset");
    if (!fixed.has(asset.name)) continue;
    const key = fixed.get(asset.name);
    if (assets[key] !== null) fail("GitHub release contains a duplicate fixed asset");
    if (!Number.isSafeInteger(asset.id) || asset.id <= 0) fail("GitHub release asset id is invalid");
    const expected = `https://api.github.com/repos/NVIDIA/k8s-test-infra/releases/assets/${asset.id}`;
    if (asset.url !== expected) fail("GitHub release asset URL is invalid");
    assets[key] = { id: asset.id, name: asset.name };
  }
  return { tagName: value.tagName, target: releaseSha, assets };
}

function parseChallenge(value, repo) {
  if (typeof value !== "string") fail("GHCR authentication challenge is missing");
  const match = /^Bearer realm="(https:\/\/ghcr\.io\/token)",service="ghcr\.io",scope="(repository:[a-z0-9._/-]+:pull)"$/.exec(value);
  if (!match || match[2] !== `repository:${repo}:pull`) fail("GHCR authentication challenge is invalid");
  return { realm: match[1], service: "ghcr.io", scope: match[2] };
}

function fetchResponse(response, body) {
  const declared = response.headers.get("content-length");
  if (declared !== null && (!/^[0-9]+$/.test(declared) || Number(declared) > MAX_RESPONSE_BYTES)) fail("remote response length is invalid");
  if (body.length > MAX_RESPONSE_BYTES) fail("remote response is too large");
  return { status: response.status, headers: response.headers, body };
}

function createGhcrRequest(repo, username, token) {
  repository(repo);
  if (typeof username !== "string" || !/^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$/.test(username)) fail("GHCR username is invalid");
  if (typeof token !== "string" || token.length < 20 || token.length > 512 || /[\r\n]/.test(token)) fail("GHCR token is invalid");
  let bearer = null;
  return async ({ url, headers }) => {
    const parsed = new URL(url);
    if (parsed.origin !== "https://ghcr.io" || !parsed.pathname.startsWith(`/v2/${repo}/`)) fail("GHCR request endpoint is invalid");
    const execute = async (authorization) => {
      const response = await fetch(url, { headers: { ...headers, authorization } });
      return fetchResponse(response, Buffer.from(await response.arrayBuffer()));
    };
    let response = await execute(bearer === null ? `Basic ${Buffer.from(`${username}:${token}`).toString("base64")}` : `Bearer ${bearer}`);
    if (response.status !== 401) return response;
    const challenge = parseChallenge(header(response, "www-authenticate"), repo);
    const tokenUrl = new URL(challenge.realm);
    tokenUrl.searchParams.set("service", challenge.service);
    tokenUrl.searchParams.set("scope", challenge.scope);
    const tokenResponse = await fetch(tokenUrl, { headers: { authorization: `Basic ${Buffer.from(`${username}:${token}`).toString("base64")}` } });
    if (tokenResponse.status !== 200) fail("GHCR token request failed");
    const tokenBody = object(await tokenResponse.json(), "GHCR token response");
    bearer = typeof tokenBody.token === "string" ? tokenBody.token : tokenBody.access_token;
    if (typeof bearer !== "string" || bearer.length < 20 || bearer.length > 4096 || /[\r\n]/.test(bearer)) fail("GHCR bearer token is invalid");
    response = await execute(`Bearer ${bearer}`);
    return response;
  };
}

function createGitHubRequest(token) {
  if (typeof token !== "string" || token.length < 20 || token.length > 512 || /[\r\n]/.test(token)) fail("GitHub token is invalid");
  return async ({ url, headers }) => {
    const parsed = new URL(url);
    if (parsed.origin !== "https://api.github.com" || !parsed.pathname.startsWith("/repos/NVIDIA/k8s-test-infra/")) fail("GitHub request endpoint is invalid");
    const response = await fetch(url, { headers: { ...headers, authorization: `Bearer ${token}` } });
    return fetchResponse(response, Buffer.from(await response.arrayBuffer()));
  };
}

async function main(argv) {
  const [command] = argv;
  const releaseSha = process.env.RELEASE_SHA;
  const imageRepository = "nvidia/nvml-mock";
  const chartRepository = "nvidia/charts/nvml-mock";
  if (command === "github-release") {
    return gatherGitHubReleaseState({
      request: createGitHubRequest(process.env.GH_TOKEN), owner: "NVIDIA", repository: "k8s-test-infra",
      tagName: process.env.RELEASE_TAG, releaseSha,
    });
  }
  const username = process.env.GHCR_USER;
  const token = process.env.GHCR_TOKEN;
  if (command === "image") {
    const proof = JSON.parse(readFileSync(process.env.EVIDENCE_FILE, "utf8"));
    return gatherImageState({ request: createGhcrRequest(imageRepository, username, token), repository: imageRepository, version: process.env.RELEASE_VERSION, releaseSha, evidence: proof });
  }
  if (command === "image-identity") {
    return gatherImageIdentityState({ request: createGhcrRequest(imageRepository, username, token), repository: imageRepository, version: process.env.RELEASE_VERSION, releaseSha });
  }
  if (command === "development") {
    return gatherDevelopmentState({ request: createGhcrRequest(imageRepository, username, token), repository: imageRepository, releaseSha });
  }
  if (command === "chart") {
    const result = await gatherChartState({ request: createGhcrRequest(chartRepository, username, token), repository: chartRepository, version: process.env.RELEASE_VERSION, chartName: "nvml-mock" });
    if (result.archive !== null) writeFileSync(process.env.REMOTE_CHART_ARCHIVE, result.archive, { flag: "wx" });
    return { remoteManifestDigest: result.remoteManifestDigest, remoteTreeDigest: result.remoteTreeDigest };
  }
  fail("unsupported release reader command");
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    process.stdout.write(`${JSON.stringify(await main(process.argv.slice(2)))}\n`);
  } catch (error) {
    process.stderr.write(`release-reader: ${error instanceof Error ? error.message : "failed"}\n`);
    process.exitCode = 1;
  }
}
