# Mokka agent skill

An [Agent Skill](https://agentskills.io/) that helps coding agents, such as
Claude Code, Codex and Cursor, use Mokka to test GPU-dependent Kubernetes
software without GPUs.

- `SKILL.md`: the skill. It tells the agent whether Mokka fits the task, how to
  install and verify Mokka, which settings each consumer needs, how to change
  GPU state at runtime, and how to troubleshoot. For everything else it points
  into the Mokka documentation.
- `references/downstream-patterns.md`: how projects that already test against
  Mokka set up their lanes, and the lessons that carry over. The agent loads it
  when it needs a worked example.

## Install

Copy or symlink this directory into your agent's skills directory. Clone the
release tag that matches the chart you install, so the skill describes that
release rather than unreleased work on `main`. Releases up to 0.7.0 do not
include the skill; with one of those charts, copy it from `main` and have the
agent read the docs for your installed version, as the skill's rules say:

```bash
git clone --depth 1 --branch v<version> https://github.com/NVIDIA/k8s-test-infra.git
cp -r k8s-test-infra/skills/mokka ~/.claude/skills/mokka      # Claude Code, every project
cp -r k8s-test-infra/skills/mokka <your-repo>/.agents/skills/  # one repository
```

## Maintaining it

The skill routes to `docs/` rather than repeating it, so most doc changes need
no skill change. Update the skill when a change alters a fact it states
directly:

- the chart location, release and namespace conventions, or the profile list;
- a consumer setting in the "Put the consumer on top" table;
- an `nvml-mock-ctl` command;
- a troubleshooting symptom;
- anything under "Ground facts" or "Rules", such as the driver root, the
  hostPaths, the containerd config file, GPU counts, the MIG-capable profiles
  or the support policy.

`make lint` checks every `github.com/NVIDIA/k8s-test-infra` link in these files
against the working tree, so a moved or deleted doc page fails CI.
