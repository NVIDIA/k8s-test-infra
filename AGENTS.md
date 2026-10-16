# Mokka

Mokka helps simulate expensive GPU infrastructure on CPU nodes by
simulating the presence of GPU and network devices at the driver level.

The idea is to simulate the driver footprint at a low level,
so all consumers and applications up the stack work without modifications.

## Principles

- Simple and minimal code
- Strive for high cohesion and low coupling
- Write idiomatic Golang code and project structure
- Red-green test-driven development
- Code should communicate your intent
- Code should be composable and testable
- Errors should not be ignored. Bubble them up with more context, log or handle. If it doesn't make sense to handle an error, you must leave an concise explanation why.

## Use cases

- The system should work well for clusters up to 100k nodes.
- Also, the system should be convenient to use in local, low-scale clusters

## Tech Stack

- Modern Golang 1.27
- Kubernetes as a main deployment target
- Tilt for local development and CI E2E test environment setup

## Testing

- Use testify/require for test assertions
- When changing Golang codebase, make sure `make lint-fix` works without violations. Run `make test` to run changes against existing test suite.
- When modify helm chart, make sure to run `make helm-tests` in order to ensure that the Helm chart is not broken.
- Add only meaningful tests
- Use t.Parallel() where possible to speed up test execution

## Changelog

- Entries are [changie](https://changie.dev) fragments. Never edit `CHANGELOG.md` — it holds released versions only and is written at release time.
- Add one with `make changelog KIND=<kind> BODY="..." [ISSUE=<number>]`. Valid kinds: `Added`, `Changed`, `Deprecated`, `Removed`, `Fixed`, `Security`.
- Add a fragment on every PR that changes behavior (feature, bug fix, API change, security bump). Skip refactors, docs, tests and CI, and apply the `skip-changelog` label instead — CI fails a PR with neither.
- Commit the fragment with the code. Fragments never conflict between PRs, so no coordination is needed.

### Writing the entry

An entry tells a user what changed for them, not how or why it was built. The PR and the docs hold the detail; the entry points to it.

- One or two sentences, at most ~300 characters. If it needs more, the extra belongs in the docs — link to the page instead.
- Prefix the component the user touches: `nvml-mock:`, `node-agent:`, `helm:`, `profiles:`.
- Lead with the observable behavior: what a user can now do, or what was broken and now works. For a fix, name the symptom a user would have hit.
- Leave out the root cause, implementation, alternatives considered, and test coverage.
- Put the issue or PR number in `ISSUE`, not in the body.
- Mark an incompatible change with a leading `**Breaking:**` and state the action a user must take.
- One entry per user-visible change. A PR with several unrelated changes gets several fragments.

```yaml
# Good: what changed for the user, then at most one clause of context
body: |-
  <component>: <what a user can now do, or the symptom that no longer occurs>;
    <the one detail a user needs to act on it, or a link to the docs>.

# Too long: reads like the PR description
body: |-
  <component>: <behavior>. <why it was broken>. <how the fix works>.
    <alternatives considered>. <what the tests cover>.
```

## CI/CD

- Github Actions must be used for CI/CD
- Use Makefile commands in Github Action pipelines to keep pipeline logic lean and be able to reproduce the same commands locally

## Logging

- use a globally registered instance of zap.L() logger
- Ensure sufficient and meaningful logging in the new or modified codebase.
- Use log level appropriately. Debug level is or diagnosing or understanding internal execution; normally disabled in production and enabled during development. Info level is for meaningful, expected lifecycle or business events. Warning is for something unexpected, but the operation or service can continue. Error level is for The current operation failed and could not recover at this layer.

## Comments

- When leaving a comment in code, it should explain intent where it's not obvious — why we are doing something, not what we are doing, in most cases. Don't write comments based on the current conversation context; they should generally be valuable long-term for readers.

## Docs

- Write high-quality technical documentation for this project as **Fern** pages. Fern is NVIDIA's documentation standard; the site is built from `docs/` with configuration in `fern/`.
- First, inspect the project structure, source code, configuration, existing documentation, examples, and tests. Build an accurate mental model before writing.
- Do not invent behavior or capabilities that are not supported by the repository.

### What is published

The published site is for people **running** Mokka. `docs/index.yml` is the single list of what is in and what is out: a page is published only if it appears there, and its order and grouping are the site's navigation.

* A page belongs on the site if a user or operator can follow it without a repository clone and without reading source. A page that leans on code artifacts — scripts, Dockerfiles, Terraform, values files that live beside it — is a test script, not a guide. Keep it out of `docs/index.yml`.
* Content for people changing Mokka — contributing, maintainers, build and test tooling, demos that need a checkout — stays in the repository as GitHub-rendered markdown under `docs/contributing/` and `docs/maintainers/`. Do not list it in `docs/index.yml`, and use GitHub alerts (`> [!NOTE]`) there, not Fern components.
* When a page you add or move is not published, add it to `DOCS_UNPUBLISHED` in the `Makefile`, which keeps Fern's MDX checks away from syntax that only GitHub renders.
* Many links from a published page to files in the repository is a smell. Inline small configs as code blocks with a `title`; if a page needs more than that, it probably should not be published.

### Writing style

* Be concise, direct, and technically precise.
* Explain concepts in plain language before introducing specialized terminology.
* Focus on what the system does, why it exists, how its parts fit together, and how users interact with it.
* Describe implementation at the architectural level: summarize the approach, important components, data flow, lifecycle, and design decisions.
* Do not walk through source code line by line or document trivial implementation details.
* Use concrete examples where they improve understanding.
* Avoid marketing language, filler, repetition, and generic introductions.
* Define acronyms and project-specific terms on first use.

### Structure

The published site has two top-level spaces. Place a page by who is reading it, not by what it is about.

* **General** — someone learning or operating Mokka. Overview, Getting Started, Architecture, Reference, Troubleshooting, FAQ.
* **Guides** — someone with a specific goal. How-to scenarios and Tasks.

Within General, order pages so each builds progressively on the last: Overview → Getting started → Concepts → Architecture → Reference → Troubleshooting.

Placement rules:

* Assume the reader is running Mokka in Kubernetes. The local `LD_LIBRARY_PATH` build is the contributor workflow and belongs under `docs/contributing/`.
* Every fact has exactly one home. Link to it; never restate it. A fact written twice is a fact that will disagree with itself.
* The landing page says what Mokka is, shows one install, and routes. No reference tables — it is the most-read and least-maintained page.
* Architecture pages describe the moving parts, how they connect, and how the system behaves. Each component gets its own page under Architecture, covering what it does and how it behaves.
* A guide is customer-facing and self-contained: it installs published artifacts and inlines the small files it needs. It must run without a repository clone.
* Create only pages the project justifies. Do not add a section to complete an outline.

### Fern features

Pages are Markdown parsed as MDX, so some characters are syntax. Run `make docs-check` before sending a change; it runs the MDX safety check, which catches the common failures:

* Self-close void elements (`<img ... />`), and write a bare URL as an ordinary Markdown link, not as an `<https://...>` autolink.
* Escape `{` as `\{` and a bare `<` before a digit as `&lt;`, or put them in backticks. Wrap placeholders such as `<GPU_UUID>` in backticks.
* Do not use HTML comments. License headers on markdown are covered by `REUSE.toml`.

Use Fern components when they materially improve comprehension:

* callouts: `<Note>`, `<Warning>`, `<Tip>`;
* `<Tabs>` and `<Tab title="...">` for meaningful alternatives;
* `<Accordion>` for optional or advanced details;
* Mermaid diagrams in a `mermaid` code block for architecture, workflows, and state transitions;
* a `title="..."` on a code block that is a file the reader should save;
* tables for exact comparisons, field definitions, states, and mappings.

Do not overuse these. Prefer ordinary prose when it communicates the idea more clearly. Do not use MkDocs Material syntax (`!!! note`, `=== "Tab"`).

### Cross-references

Treat the documentation as a connected system rather than isolated pages.

* Cross-reference related concepts, guides, and reference pages.
* Link a term to its canonical explanation instead of redefining it everywhere.
* Use relative links to `.md` files that are listed in `docs/index.yml`. A link to a page that is not published will not resolve on the site; link to it on GitHub instead, or drop it.
* Use descriptive link text—not "here" or raw filenames.
* Avoid duplicate explanations; keep one authoritative explanation and link to it.
* Verify that every internal link and heading anchor resolves correctly. `make docs-check` checks published pages, and `make lint` checks every in-repo markdown link.

### Level of detail

Choose detail according to the reader's needs:

* Explain public behavior, user-visible resources, configuration, workflows, invariants, and operational consequences.
* Explain internal components only when they help readers understand behavior, operate the system, troubleshoot it, or contribute effectively.
* Summarize algorithms and controller logic as inputs → decisions → outputs.
* Mention important edge cases and failure modes.
* Omit private helper functions, routine plumbing, and line-by-line implementation commentary.
* Link to relevant source files only when a contributor genuinely benefits from seeing the implementation.

### Accuracy requirements

* Derive all technical claims from the repository.
* Keep terminology consistent with the code and APIs.
* Clearly distinguish current behavior from planned or proposed behavior.
* Do not assume defaults—verify them.
* Ensure commands and examples are runnable and internally consistent.
* Identify uncertainty or missing information instead of guessing.
* Preserve useful existing documentation, improving or reorganizing it where necessary.

Produce or update the Markdown pages and the `docs/index.yml` navigation. Finish by checking:

* navigation and page hierarchy;
* internal links and anchors;
* terminology consistency;
* code examples and commands;
* duplicated or unnecessarily detailed content;
* that `make docs-check` passes.

The final result should let a technically competent newcomer understand the project, get it running,
form the correct mental model, and find deeper reference information without being overwhelmed by implementation details.
