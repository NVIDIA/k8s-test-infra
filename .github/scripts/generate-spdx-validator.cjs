"use strict";

const { createHash } = require("node:crypto");
const { createRequire } = require("node:module");
const { readFileSync, writeFileSync } = require("node:fs");
const path = require("node:path");

const root = path.resolve(__dirname, "../..");
const schemaPath = path.join(root, ".github/schemas/spdx-2.3.schema.json");
const outputPath = path.join(root, ".github/scripts/spdx-schema-validator.mjs");
const upstreamSha256 = "239208b7ac287b3cf5d9a9af23f9d69863971102a5e1587a27a398b43490b89b";
const expectedSha256 = "1e7a377f428c24d4b13dd786afe219d1517df18de76114b67040a0ce6ca18afa";
const source = readFileSync(schemaPath);
if (createHash("sha256").update(source).digest("hex") !== expectedSha256) throw new Error("official SPDX schema checksum changed");

const requireFromAction = createRequire(path.join(root, ".github/actions/repo-automation/package.json"));
const Ajv = requireFromAction("ajv");
const standaloneCode = requireFromAction("ajv/dist/standalone").default;
const schema = JSON.parse(source);
const ajv = new Ajv({ allErrors: true, strict: false, code: { source: true, esm: true, lines: true } });
const validate = ajv.compile(schema);
const generated = standaloneCode(ajv, validate);
writeFileSync(outputPath, `// Generated from SPDX v2.3 official schema (tag v2.3), upstream sha256:${upstreamSha256}.\n${generated}`);
