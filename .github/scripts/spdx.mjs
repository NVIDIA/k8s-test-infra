import { createHash } from "node:crypto";
import { readFileSync, renameSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

import validateSpdx from "./spdx-schema-validator.mjs";

const UNORDERED_ARRAY_FIELDS = new Set([
  "annotations", "attributionTexts", "checksums", "creators", "documentDescribes",
  "externalDocumentRefs", "externalRefs", "fileContributors", "fileDependencies", "files", "fileTypes",
  "hasExtractedLicensingInfos", "hasFiles", "licenseInfoFromFiles", "licenseInfoInFiles", "packages",
  "packageVerificationCodeExcludedFiles", "relationships", "revieweds", "seeAlsos", "snippets",
]);

function fail(message) {
  throw new TypeError(message);
}

function normalize(value, path, fieldName = "") {
  if (value === null || typeof value === "string" || typeof value === "boolean") return value;
  if (typeof value === "number") {
    if (!Number.isFinite(value)) fail(`${path} contains a non-finite number`);
    return value;
  }
  if (Array.isArray(value)) {
    const items = value.map((item, index) => normalize(item, `${path}[${index}]`));
    if (UNORDERED_ARRAY_FIELDS.has(fieldName)) {
      items.sort((left, right) => Buffer.compare(Buffer.from(JSON.stringify(left)), Buffer.from(JSON.stringify(right))));
    }
    return items;
  }
  if (typeof value !== "object" || Object.getPrototypeOf(value) !== Object.prototype) fail(`${path} must contain only plain JSON values`);
  const result = {};
  for (const key of Object.keys(value).sort()) {
    if (key === "__proto__" || key === "constructor" || key === "prototype") fail(`${path} contains an unsafe key`);
    result[key] = normalize(value[key], `${path}.${key}`, key);
  }
  return result;
}

function document(value) {
  if (value === null || typeof value !== "object" || Array.isArray(value) || Object.getPrototypeOf(value) !== Object.prototype) {
    fail("SPDX document must be a plain object");
  }
  normalize(value, "SPDX document");
  if (!validateSpdx(value)) fail("SPDX document does not match the pinned official SPDX 2.3 schema");
  if (value.spdxVersion !== "SPDX-2.3" || value.dataLicense !== "CC0-1.0" || value.SPDXID !== "SPDXRef-DOCUMENT") {
    fail("SPDX document identity is invalid");
  }
  if (typeof value.name !== "string" || value.name.length === 0 || value.name.length > 1024) fail("SPDX document name is invalid");
  const verifyChecksums = (candidate) => {
    if (Array.isArray(candidate)) {
      for (const item of candidate) verifyChecksums(item);
      return;
    }
    if (candidate === null || typeof candidate !== "object") return;
    if (Object.hasOwn(candidate, "algorithm") && Object.hasOwn(candidate, "checksumValue") && !/^[0-9a-f]+$/.test(candidate.checksumValue)) {
      fail("SPDX checksumValue must be lowercase hexadecimal");
    }
    for (const nested of Object.values(candidate)) verifyChecksums(nested);
  };
  verifyChecksums(value);
  const copy = { ...value, creationInfo: { ...value.creationInfo } };
  delete copy.documentNamespace;
  delete copy.creationInfo.created;
  return normalize(copy, "SPDX document");
}

export function canonicalSpdx(value) {
  const semantic = document(value);
  const identity = createHash("sha256").update(JSON.stringify(semantic)).digest("hex");
  const canonical = normalize({
    ...semantic,
    documentNamespace: `https://github.com/NVIDIA/k8s-test-infra/spdx/${identity}`,
    creationInfo: { ...semantic.creationInfo, created: "1970-01-01T00:00:00Z" },
  }, "canonical SPDX document");
  return `${JSON.stringify(canonical)}\n`;
}

export function canonicalSpdxDigest(value) {
  return createHash("sha256").update(canonicalSpdx(value)).digest("hex");
}

function parseFile(path) {
  if (typeof path !== "string" || path.length === 0) fail("SPDX path is required");
  let value;
  try {
    value = JSON.parse(readFileSync(path, "utf8"));
  } catch {
    fail("SPDX file is not valid JSON");
  }
  return value;
}

function main(argv) {
  const [command, path] = argv;
  const value = parseFile(path);
  if (command === "digest") return canonicalSpdxDigest(value);
  if (command === "normalize") {
    const temporary = `${path}.canonical.tmp`;
    writeFileSync(temporary, canonicalSpdx(value), { flag: "wx", mode: 0o600 });
    renameSync(temporary, path);
    return "normalized";
  }
  fail("unsupported SPDX command");
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    process.stdout.write(`${main(process.argv.slice(2))}\n`);
  } catch (error) {
    process.stderr.write(`spdx: ${error instanceof Error ? error.message : "failed"}\n`);
    process.exitCode = 1;
  }
}
