import assert from "node:assert/strict";
import test from "node:test";

import { canonicalSpdx, canonicalSpdxDigest } from "./spdx.mjs";

const base = {
  spdxVersion: "SPDX-2.3",
  dataLicense: "CC0-1.0",
  SPDXID: "SPDXRef-DOCUMENT",
  name: "image",
  documentNamespace: "https://example.invalid/random-a",
  creationInfo: { created: "2026-07-17T10:00:00Z", creators: ["Tool: syft-1", "Organization: NVIDIA"] },
  packages: [
    { SPDXID: "SPDXRef-Package-B", name: "b", versionInfo: "2", downloadLocation: "NOASSERTION", filesAnalyzed: false, licenseConcluded: "NOASSERTION", licenseDeclared: "NOASSERTION", copyrightText: "NOASSERTION", checksums: [{ algorithm: "SHA256", checksumValue: "b".repeat(64) }] },
    { SPDXID: "SPDXRef-Package-A", name: "a", versionInfo: "1", downloadLocation: "NOASSERTION", filesAnalyzed: false, licenseConcluded: "NOASSERTION", licenseDeclared: "NOASSERTION", copyrightText: "NOASSERTION" },
  ],
  relationships: [{ spdxElementId: "SPDXRef-DOCUMENT", relationshipType: "DESCRIBES", relatedSpdxElement: "SPDXRef-Package-A" }],
};

test("SPDX canonicalization ignores generation identity and unordered arrays", () => {
  const regenerated = {
    ...base,
    documentNamespace: "https://example.invalid/random-b",
    creationInfo: { ...base.creationInfo, created: "2026-07-17T11:00:00Z", creators: [...base.creationInfo.creators].reverse() },
    packages: [...base.packages].reverse(),
  };
  assert.equal(canonicalSpdxDigest(base), canonicalSpdxDigest(regenerated));
  assert.equal(canonicalSpdx(base), canonicalSpdx(regenerated));
  const canonical = JSON.parse(canonicalSpdx(base));
  assert.match(canonical.documentNamespace, /^https:\/\/github\.com\/NVIDIA\/k8s-test-infra\/spdx\/[0-9a-f]{64}$/);
  assert.equal(canonical.creationInfo.created, "1970-01-01T00:00:00Z");
});

test("SPDX canonicalization preserves semantic package changes and rejects hostile shapes", () => {
  const changed = structuredClone(base);
  changed.packages[0].versionInfo = "3";
  assert.notEqual(canonicalSpdxDigest(base), canonicalSpdxDigest(changed));
  assert.throws(() => canonicalSpdx({ ...base, packages: [{ __proto__: { polluted: true } }] }), { name: "TypeError" });
  assert.throws(() => canonicalSpdx({ ...base, packages: [{ SPDXID: "SPDXRef-Bad", name: "bad" }] }), { name: "TypeError" });
  assert.throws(() => canonicalSpdx({ ...base, relationships: [{ spdxElementId: "SPDXRef-DOCUMENT", relationshipType: "MADE_UP", relatedSpdxElement: "SPDXRef-Package-A" }] }), { name: "TypeError" });
  const badChecksum = structuredClone(base);
  badChecksum.packages[0].checksums[0].checksumValue = "not-hex";
  assert.throws(() => canonicalSpdx(badChecksum), { name: "TypeError" });
});

test("SPDX canonicalization preserves schema-defined ordered collections", () => {
  const ordered = {
    ...base,
    hasExtractedLicensingInfos: [{
      licenseId: "LicenseRef-Example", extractedText: "license text",
      crossRefs: [{ url: "https://example.invalid/second", order: 2 }, { url: "https://example.invalid/first", order: 1 }],
    }],
  };
  const canonical = JSON.parse(canonicalSpdx(ordered));
  assert.deepEqual(canonical.hasExtractedLicensingInfos[0].crossRefs.map((value) => value.order), [2, 1]);
});
