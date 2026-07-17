import assert from "node:assert/strict";
import test from "node:test";

import { chartArchive } from "./release-state-test-helpers.mjs";
import { planChartPublication, planImagePublication } from "./release-state.mjs";
import {
  gatherChartState,
  gatherGitHubReleaseState,
  gatherImageState,
} from "./release-reader.mjs";

const SHA_A = "a".repeat(40);
const SHA_B = "b".repeat(40);
const DIGEST_A = `sha256:${"a".repeat(64)}`;
const DIGEST_B = `sha256:${"b".repeat(64)}`;
const CONFIG_A = `sha256:${"c".repeat(64)}`;

function response(status, body, digest = null) {
  return {
    status,
    headers: new Map(digest === null ? [] : [["docker-content-digest", digest]]),
    body: Buffer.isBuffer(body) ? body : Buffer.from(typeof body === "string" ? body : JSON.stringify(body)),
  };
}

function imageFixture({ collision = false, absent = false, aliasVersion = "1.2.3", aliasDigest = DIGEST_A } = {}) {
  const tags = new Map([
    ["1.2.3", { digest: DIGEST_A, revision: SHA_A, version: "1.2.3" }],
    [`sha-${SHA_A.slice(0, 12)}`, { digest: DIGEST_A, revision: collision ? SHA_B : SHA_A, version: "1.2.3" }],
    ["1.2", { digest: aliasDigest, revision: SHA_A, version: aliasVersion }],
    ["1", { digest: aliasDigest, revision: SHA_A, version: aliasVersion }],
    ["latest", { digest: aliasDigest, revision: SHA_A, version: aliasVersion }],
    ["edge", { digest: DIGEST_B, revision: SHA_B, version: "1.2.2" }],
  ]);
  if (absent) tags.clear();
  let index = 1;
  const configs = new Map();
  for (const item of tags.values()) {
    item.config = `sha256:${String(index).repeat(64)}`;
    configs.set(item.config, item);
    index += 1;
  }
  return async ({ url }) => {
    const manifest = /\/manifests\/([^/]+)$/.exec(url);
    if (manifest) {
      const item = tags.get(decodeURIComponent(manifest[1]));
      if (!item) return response(404, "");
      return response(200, {
        schemaVersion: 2,
        mediaType: "application/vnd.oci.image.manifest.v1+json",
        config: { mediaType: "application/vnd.oci.image.config.v1+json", digest: item.config, size: 100 },
        layers: [],
      }, item.digest);
    }
    const blob = /\/blobs\/(sha256:[0-9a-f]{64})$/.exec(url);
    if (blob && configs.has(blob[1])) {
      const item = configs.get(blob[1]);
      return response(200, { config: { Labels: {
        "org.opencontainers.image.revision": item.revision,
        "org.opencontainers.image.version": item.version,
      } } });
    }
    throw new Error(`unexpected fixture request: ${url}`);
  };
}

test("OCI image reader creates the exact planner state and rejects a short-SHA collision", async () => {
  const state = await gatherImageState({
    request: imageFixture(), repository: "nvidia/nvml-mock", version: "1.2.3", releaseSha: SHA_A,
    evidence: { signature: true, sbom: false, provenance: true },
  });
  assert.deepEqual(state.stable, { digest: DIGEST_A, sourceRevision: SHA_A, signature: true, sbom: false, provenance: true });
  assert.deepEqual(state.short, { digest: DIGEST_A, sourceRevision: SHA_A });
  assert.deepEqual(state.minor, { digest: DIGEST_A, version: "1.2.3" });
  assert.deepEqual(state.major, { digest: DIGEST_A, version: "1.2.3" });
  assert.deepEqual(state.latest, { digest: DIGEST_A, version: "1.2.3" });
  await assert.rejects(() => gatherImageState({
    request: imageFixture({ collision: true }), repository: "nvidia/nvml-mock", version: "1.2.3", releaseSha: SHA_A,
    evidence: { signature: false, sbom: false, provenance: false },
  }), { name: "TypeError" });
});

test("gathered image fixtures drive absent, identical, older, equal-mismatch, and newer planner decisions", async () => {
  const options = (request) => ({
    request, repository: "nvidia/nvml-mock", version: "1.2.3", releaseSha: SHA_A,
    evidence: { signature: true, sbom: false, provenance: true },
  });
  assert.deepEqual(planImagePublication(await gatherImageState(options(imageFixture({ absent: true })))), {
    immutable: { action: "publish", digest: null }, development: { short: "defer" },
    aliases: { minor: "defer", major: "defer", latest: "defer" },
    resume: { signature: true, sbom: true, provenance: true },
  });
  assert.deepEqual(planImagePublication(await gatherImageState(options(imageFixture()))), {
    immutable: { action: "reuse", digest: DIGEST_A }, development: { short: "skip" },
    aliases: { minor: "skip", major: "skip", latest: "skip" },
    resume: { signature: false, sbom: true, provenance: false },
  });
  assert.equal(planImagePublication(await gatherImageState(options(imageFixture({ aliasVersion: "1.2.2", aliasDigest: DIGEST_B })))).aliases.latest, "update");
  const equalMismatch = await gatherImageState(options(imageFixture({ aliasDigest: DIGEST_B })));
  assert.throws(() => planImagePublication(equalMismatch), { name: "TypeError" });
  const newer = await gatherImageState(options(imageFixture({ aliasVersion: "1.2.4", aliasDigest: DIGEST_B })));
  assert.throws(() => planImagePublication(newer), { name: "TypeError" });
});

test("OCI chart reader pulls the explicit archive layer and returns normalized tree and manifest digests", async () => {
  const archive = chartArchive([{ name: "nvml-mock/", type: "directory" }, { name: "nvml-mock/Chart.yaml", content: "version: 1.2.3\n" }]);
  const request = async ({ url }) => {
    if (url.endsWith("/manifests/1.2.3")) return response(200, {
      schemaVersion: 2,
      mediaType: "application/vnd.oci.image.manifest.v1+json",
      config: { mediaType: "application/vnd.cncf.helm.config.v1+json", digest: CONFIG_A, size: 2 },
      layers: [{ mediaType: "application/vnd.cncf.helm.chart.content.v1.tar+gzip", digest: DIGEST_B, size: archive.length }],
    }, DIGEST_A);
    if (url.endsWith(`/blobs/${DIGEST_B}`)) return response(200, archive);
    throw new Error(`unexpected fixture request: ${url}`);
  };
  const state = await gatherChartState({ request, repository: "nvidia/charts/nvml-mock", version: "1.2.3", chartName: "nvml-mock" });
  assert.equal(state.remoteManifestDigest, DIGEST_A);
  assert.match(state.remoteTreeDigest, /^sha256:[0-9a-f]{64}$/);
  assert.deepEqual(state.archive, archive);
});

test("GitHub reader validates the exact release target and returns only fixed asset identities", async () => {
  const request = async ({ url }) => {
    assert.equal(url, "https://api.github.com/repos/NVIDIA/k8s-test-infra/releases/tags/v1.2.3");
    return response(200, {
      tag_name: "v1.2.3", target_commitish: SHA_A,
      assets: [
        { id: 11, name: "image-sbom.spdx.json", url: "https://api.github.com/repos/NVIDIA/k8s-test-infra/releases/assets/11" },
        { id: 12, name: "chart-sbom.spdx.json", url: "https://api.github.com/repos/NVIDIA/k8s-test-infra/releases/assets/12" },
      ],
    });
  };
  assert.deepEqual(await gatherGitHubReleaseState({
    request, owner: "NVIDIA", repository: "k8s-test-infra", tagName: "v1.2.3", releaseSha: SHA_A,
  }), {
    tagName: "v1.2.3", target: SHA_A,
    assets: { image: { id: 11, name: "image-sbom.spdx.json" }, chart: { id: 12, name: "chart-sbom.spdx.json" } },
  });
});
