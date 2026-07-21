"use strict";

const assert = require("node:assert/strict");
const childProcess = require("node:child_process");
const fs = require("node:fs");
const http = require("node:http");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");

const YAML = require("yaml");

const { createFakeGitHub } = require("./helpers/fake-github.js");

const actionRoot = path.resolve(__dirname, "..");
const repositoryRoot = path.resolve(actionRoot, "../../..");

function label(name, color = "123abc", description = `${name} description`) {
  return { name, color, description };
}

function expectedPlan() {
  return {
    creates: [label("kind/feature")],
    updates: [label("kind/bug", "abcdef", "Declared bug")],
    unchanged: [label("kind/test")],
    unmanaged: [label("legacy/keep")],
  };
}

function declaredLabels() {
  const plan = expectedPlan();
  return [...plan.creates, ...plan.updates, ...plan.unchanged];
}

function existingLabels() {
  const plan = expectedPlan();
  return [
    label("kind/bug", "123456", "Old bug"),
    ...plan.unchanged,
    ...plan.unmanaged,
  ];
}

function spawnNode(args, options) {
  return new Promise((resolve, reject) => {
    const child = childProcess.spawn(process.execPath, args, options);
    let stdout = "";
    let stderr = "";
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
    });
    child.stderr.on("data", (chunk) => {
      stderr += chunk;
    });
    child.on("error", reject);
    child.on("close", (code, signal) => {
      resolve({ code, signal, stdout, stderr });
    });
  });
}

function listen(server) {
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve(server.address()));
  });
}

function close(server) {
  return new Promise((resolve, reject) => {
    server.close((error) => {
      if (error) {
        reject(error);
      } else {
        resolve();
      }
    });
  });
}

test("dry-run returns the complete label plan without mutation", async () => {
  const { syncLabels } = require("../src/modes/label-sync.js");
  const github = createFakeGitHub(existingLabels());

  const result = await syncLabels({
    github,
    declaredLabels: declaredLabels(),
    dryRun: true,
  });

  assert.deepEqual(result, expectedPlan());
  assert.deepEqual(github.calls.listLabels, [{}]);
  assert.deepEqual(github.calls.createLabel, []);
  assert.deepEqual(github.calls.updateLabel, []);
  assert.deepEqual(github.snapshot(), existingLabels());
});

test("apply creates and updates exactly the labels in the plan", async () => {
  const { syncLabels } = require("../src/modes/label-sync.js");
  const github = createFakeGitHub(existingLabels());

  const result = await syncLabels({
    github,
    declaredLabels: declaredLabels(),
    dryRun: false,
  });

  assert.deepEqual(result, expectedPlan());
  assert.deepEqual(github.calls.createLabel, expectedPlan().creates);
  assert.deepEqual(github.calls.updateLabel, expectedPlan().updates);
});

test("rerunning apply after success is retry-safe and performs no more mutations", async () => {
  const { syncLabels } = require("../src/modes/label-sync.js");
  const github = createFakeGitHub(existingLabels());

  await syncLabels({ github, declaredLabels: declaredLabels(), dryRun: false });
  const mutationCount = github.calls.createLabel.length + github.calls.updateLabel.length;
  const second = await syncLabels({
    github,
    declaredLabels: declaredLabels(),
    dryRun: false,
  });

  assert.deepEqual(second, {
    creates: [],
    updates: [],
    unchanged: declaredLabels().sort((left, right) => (
      left.name.toLowerCase() < right.name.toLowerCase() ? -1 : 1
    )),
    unmanaged: expectedPlan().unmanaged,
  });
  assert.equal(github.calls.createLabel.length + github.calls.updateLabel.length, mutationCount);
  assert.equal(github.calls.listLabels.length, 2);
});

test("GitHub client returns every paginated label as a plain policy object", async () => {
  const { createGitHubClient } = require("../src/github-client.js");
  const pages = [
    [{ ...label("alpha"), id: 1, node_id: "node-1" }],
    [{ ...label("beta"), id: 2, url: "https://api.example/labels/beta" }],
  ];
  const endpointCalls = [];
  const listLabelsForRepo = async (parameters) => {
    const page = parameters.page ?? 1;
    endpointCalls.push(parameters);
    return {
      data: pages[page - 1],
      headers: page < pages.length
        ? { link: `<https://api.example/labels?page=${page + 1}>; rel="next"` }
        : {},
    };
  };
  const octokit = {
    rest: { issues: { listLabelsForRepo } },
    async paginate(endpoint, parameters) {
      const labels = [];
      for (let page = 1; page <= pages.length; page += 1) {
        labels.push(...(await endpoint({ ...parameters, page })).data);
      }
      return labels;
    },
  };

  const github = createGitHubClient(octokit, "nvidia", "k8s-test-infra");

  assert.deepEqual(await github.listLabels(), [label("alpha"), label("beta")]);
  assert.deepEqual(endpointCalls, [
    { owner: "nvidia", repo: "k8s-test-infra", per_page: 100, page: 1 },
    { owner: "nvidia", repo: "k8s-test-infra", per_page: 100, page: 2 },
  ]);
});

test("GitHub client normalizes API failures at its boundary", async () => {
  const { createGitHubClient } = require("../src/github-client.js");
  const apiError = Object.assign(new Error("service unavailable"), {
    status: 503,
    request: { request: { headers: { authorization: "token secret" } } },
  });
  const listLabelsForRepo = async () => {
    throw apiError;
  };
  const octokit = {
    rest: { issues: { listLabelsForRepo } },
    paginate: (endpoint, parameters) => endpoint(parameters),
  };
  const github = createGitHubClient(octokit, "nvidia", "k8s-test-infra");

  await assert.rejects(github.listLabels(), (error) => {
    assert.equal(error.name, "GitHubClientError");
    assert.equal(error.operation, "listLabels");
    assert.equal(error.status, 503);
    assert.match(error.message, /listLabels.*service unavailable/i);
    assert.equal(error.message.includes("secret"), false);
    return true;
  });
});

test("GitHub client redacts credentials from the actual Octokit error shape", async () => {
  const { createGitHubClient } = require("../src/github-client.js");
  const authorization = "token octokit-sensitive-sentinel-9347";
  const alternateToken = "alternate-sensitive-sentinel-6218";
  const apiError = Object.assign(
    new Error(`service unavailable; echoed ${authorization} and ${alternateToken}`),
    {
      status: 503,
      request: {
        headers: {
          Authorization: authorization,
          "X-Auth-Token": alternateToken,
        },
      },
      config: { headers: { authorization } },
      response: { headers: { "x-auth-token": alternateToken } },
    },
  );
  const listLabelsForRepo = async () => {
    throw apiError;
  };
  const octokit = {
    rest: { issues: { listLabelsForRepo } },
    paginate: (endpoint, parameters) => endpoint(parameters),
  };
  const github = createGitHubClient(octokit, "nvidia", "k8s-test-infra");

  await assert.rejects(github.listLabels(), (error) => {
    assert.equal(error.name, "GitHubClientError");
    assert.equal(error.operation, "listLabels");
    assert.equal(error.status, 503);
    assert.match(error.message, /listLabels.*service unavailable/i);
    assert.equal(error.message.includes(authorization), false);
    assert.equal(error.message.includes(alternateToken), false);
    assert.equal(Object.hasOwn(error, "request"), false);
    assert.equal(Object.hasOwn(error, "config"), false);
    assert.equal(Object.hasOwn(error, "response"), false);
    return true;
  });
});

test("dispatcher rejects an unknown mode as a hard error", async () => {
  const { run } = require("../src/index.js");
  const core = {
    getInput(name) {
      return name === "mode" ? "not-a-mode" : "";
    },
    getBooleanInput() {
      return true;
    },
    setOutput() {
      assert.fail("an unknown mode must not produce an output");
    },
  };

  await assert.rejects(
    () => run({ core }),
    /unknown|unsupported.*mode|mode.*not-a-mode/i,
  );
});

test("importing the application entry exports run without executing the action", () => {
  const indexPath = require.resolve("../src/index.js");
  const corePath = path.join(actionRoot, "node_modules", "@actions", "core", "lib", "core.js");
  delete require.cache[indexPath];
  delete require.cache[corePath];

  const application = require(indexPath);

  assert.equal(typeof application.run, "function");
  assert.equal(require.cache[corePath], undefined);
});

test("action metadata pins the Node 24 entry point and stable input/output contract", () => {
  const action = YAML.parse(fs.readFileSync(path.join(actionRoot, "action.yml"), "utf8"));

  assert.deepEqual(action.runs, { using: "node24", main: "dist/index.js" });
  assert.deepEqual(Object.keys(action.inputs).sort(), ["dry-run", "mode", "pr-number"]);
  for (const input of ["mode", "pr-number", "dry-run"]) {
    assert.equal(typeof action.inputs[input].description, "string");
    assert.notEqual(action.inputs[input].description.trim(), "");
  }
  assert.equal(action.inputs.mode.required, true);
  assert.equal(action.inputs["pr-number"].required, false);
  assert.equal(action.inputs["dry-run"].required, false);
  assert.equal(action.inputs["dry-run"].default, "true");
  assert.deepEqual(action.outputs, {
    summary: { description: "JSON summary of the idempotent operation" },
  });
});

test("packaged dist is a standalone runnable dry-run action", async (t) => {
  const distRoot = path.join(actionRoot, "dist");
  const distEntry = path.join(distRoot, "index.js");
  assert.equal(fs.statSync(distEntry).isFile(), true);
  assert.deepEqual(
    fs.readdirSync(distRoot).filter((name) => name.endsWith(".js")),
    ["index.js"],
  );

  const server = http.createServer((request, response) => {
    if (request.method === "GET" && request.url.startsWith("/repos/nvidia/k8s-test-infra/labels")) {
      response.writeHead(200, { "content-type": "application/json" });
      response.end("[]");
      return;
    }
    response.writeHead(404, { "content-type": "application/json" });
    response.end(JSON.stringify({ message: "not found" }));
  });
  t.after(() => close(server));
  const address = await listen(server);
  const outputRoot = fs.mkdtempSync(path.join(os.tmpdir(), "repo-automation-dist-"));
  const outputPath = path.join(outputRoot, "github-output");
  fs.writeFileSync(outputPath, "");

  const result = await spawnNode([distEntry], {
    cwd: repositoryRoot,
    env: {
      ...process.env,
      GITHUB_API_URL: `http://127.0.0.1:${address.port}`,
      GITHUB_OUTPUT: outputPath,
      GITHUB_REPOSITORY: "nvidia/k8s-test-infra",
      GITHUB_TOKEN: "package-smoke-token",
      GITHUB_WORKSPACE: repositoryRoot,
      "INPUT_DRY-RUN": "true",
      INPUT_MODE: "label-sync",
    },
    stdio: ["ignore", "pipe", "pipe"],
  });

  assert.equal(result.code, 0, `${result.stderr}\n${result.stdout}`);
  assert.equal(result.signal, null);
  const actionOutput = fs.readFileSync(outputPath, "utf8");
  assert.match(actionOutput, /summary<</);
  assert.match(actionOutput, /kind\/feature/);
});
