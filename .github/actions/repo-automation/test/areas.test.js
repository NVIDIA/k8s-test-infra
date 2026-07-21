"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { deriveAreaLabels } = require("../src/areas.js");

function areaConfig(rules) {
  return { schemaVersion: 1, areas: rules };
}

const currentAreas = areaConfig([
  {
    paths: ["deployments/nvml-mock/**", "cmd/nvml-mock/**"],
    labels: ["area/nvml-mock"],
  },
  { paths: ["pkg/gpu/mocknvml/**"], labels: ["area/nvml-mock"] },
  { paths: ["pkg/gpu/mockcuda/**"], labels: ["area/mockcuda"] },
  { paths: ["deployments/nvml-mock/helm/**"], labels: ["area/helm"] },
  { paths: ["tests/e2e/**"], labels: ["area/kubernetes"] },
  { paths: [".github/**", "hack/**", "Makefile"], labels: ["area/ci"] },
  { paths: ["docs/**", "*.md"], labels: ["area/docs"] },
]);

test("accepts the current areas config shape and returns sorted overlapping labels", () => {
  assert.deepEqual(
    deriveAreaLabels(["deployments/nvml-mock/helm/Chart.yaml"], currentAreas),
    ["area/helm", "area/nvml-mock"],
  );
});

test("matches root Markdown and dotfiles with repository-relative POSIX globs", async (t) => {
  const cases = [
    ["root Markdown", "README.md", ["area/docs"]],
    ["root dot-directory", ".github/workflows/metadata.yml", ["area/ci"]],
    ["nested dotfile", "docs/.markdownlint.json", ["area/docs"]],
  ];

  for (const [name, path, labels] of cases) {
    await t.test(name, () => {
      assert.deepEqual(deriveAreaLabels([path], currentAreas), labels);
    });
  }
});

test("is independent of changed-path, rule, glob, and label ordering", () => {
  const rules = [
    {
      paths: ["deployments/nvml-mock/**", "cmd/nvml-mock/**"],
      labels: ["area/nvml-mock", "area/ci"],
    },
    {
      paths: ["docs/**", "*.md"],
      labels: ["area/docs"],
    },
  ];
  const paths = ["cmd/nvml-mock/main.go", "README.md"];
  const expected = ["area/ci", "area/docs", "area/nvml-mock"];

  assert.deepEqual(deriveAreaLabels(paths, areaConfig(rules)), expected);
  assert.deepEqual(
    deriveAreaLabels(
      [...paths].reverse(),
      areaConfig(
        [...rules]
          .reverse()
          .map((rule) => ({
            paths: [...rule.paths].reverse(),
            labels: [...rule.labels].reverse(),
          })),
      ),
    ),
    expected,
  );
});

test("suppresses duplicate labels across paths, globs, and rules", () => {
  const areas = areaConfig([
    { paths: ["docs/**", "docs/*.md"], labels: ["area/docs", "area/docs"] },
    { paths: ["**/*.md"], labels: ["area/docs", "area/text"] },
  ]);

  assert.deepEqual(
    deriveAreaLabels(["docs/guide.md", "docs/reference.md"], areas),
    ["area/docs", "area/text"],
  );
});

test("returns no labels when paths are empty or no rule matches", async (t) => {
  await t.test("empty path collection", () => {
    assert.deepEqual(deriveAreaLabels([], currentAreas), []);
  });
  await t.test("unmatched path", () => {
    assert.deepEqual(deriveAreaLabels(["pkg/unmapped/file.go"], currentAreas), []);
  });
});

test("rejects non-POSIX and otherwise invalid changed paths", async (t) => {
  const cases = [
    ["non-array collection", "docs/guide.md"],
    ["empty path", [""]],
    ["non-string path", [42]],
    ["object path", [{ filename: "docs/guide.md" }]],
    ["backslash path", ["docs\\guide.md"]],
    ["absolute path", ["/docs/guide.md"]],
    ["dot-relative path", ["./docs/guide.md"]],
    ["parent traversal", ["docs/../README.md"]],
    ["empty segment", ["docs//guide.md"]],
  ];

  for (const [name, paths] of cases) {
    await t.test(name, () => {
      assert.throws(() => deriveAreaLabels(paths, currentAreas), { name: "TypeError" });
    });
  }
});

test("rejects invalid area config, rules, globs, and labels", async (t) => {
  const cases = [
    ["non-object config", []],
    ["wrong schema", { schemaVersion: 2, areas: [] }],
    ["unknown config key", { schemaVersion: 1, areas: [], unexpected: true }],
    ["non-array rules", { schemaVersion: 1, areas: {} }],
    ["empty rules", areaConfig([])],
    ["non-object rule", areaConfig([null])],
    ["unknown rule key", areaConfig([{ paths: ["docs/**"], labels: ["area/docs"], x: true }])],
    ["empty globs", areaConfig([{ paths: [], labels: ["area/docs"] }])],
    ["empty glob", areaConfig([{ paths: [""], labels: ["area/docs"] }])],
    ["non-string glob", areaConfig([{ paths: [42], labels: ["area/docs"] }])],
    ["backslash glob", areaConfig([{ paths: ["docs\\**"], labels: ["area/docs"] }])],
    ["absolute glob", areaConfig([{ paths: ["/docs/**"], labels: ["area/docs"] }])],
    ["parent glob", areaConfig([{ paths: ["../docs/**"], labels: ["area/docs"] }])],
    ["empty labels", areaConfig([{ paths: ["docs/**"], labels: [] }])],
    ["empty label", areaConfig([{ paths: ["docs/**"], labels: [""] }])],
    ["non-string label", areaConfig([{ paths: ["docs/**"], labels: [42] }])],
    ["non-area label", areaConfig([{ paths: ["docs/**"], labels: ["kind/documentation"] }])],
  ];

  for (const [name, areas] of cases) {
    await t.test(name, () => {
      assert.throws(() => deriveAreaLabels(["docs/guide.md"], areas), { name: "TypeError" });
    });
  }
});
