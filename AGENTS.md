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

- Modern Golang 1.26
- Kubernetes as a main deployment target
- Tilt for local development and CI E2E test environment setup

## Testing

- Use testify/require for test assertions
- When changing Golang codebase, make sure `make lint-fix` works without violations. Run `make test` to run changes against existing test suite.
- When modify helm chart, make sure to run `make helm-tests` in order to ensure that the Helm chart is not broken.
- Add only meaningful tests
- Use t.Parallel() where possible to speed up test execution

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

- Write high-quality technical documentation for this project using **MkDocs with the Material theme**.
- First, inspect the project structure, source code, configuration, existing documentation, examples, and tests. Build an accurate mental model before writing. 
- Do not invent behavior or capabilities that are not supported by the repository.

### Writing style

* Be concise, direct, and technically precise.
* Explain concepts in plain language before introducing specialized terminology.
* Focus on what the system does, why it exists, how its parts fit together, and how users interact with it.
* Describe implementation at the architectural level: summarize the approach, important components, data flow, lifecycle, and design decisions.
* Do not walk through source code line by line or document trivial implementation details.
* Use concrete examples where they improve understanding.
* Avoid marketing language, filler, repetition, and generic introductions.
* Define acronyms and project-specific terms on first use.

### MkDocs Material features

Use Material features when they materially improve comprehension:

* admonitions such as `note`, `tip`, `warning`, and `example`;
* content tabs for meaningful alternatives;
* Mermaid diagrams for architecture, workflows, state transitions, or resource relationships;
* code annotations for non-obvious parts of short examples;
* tables for exact comparisons, field definitions, states, and mappings;
* collapsible sections for optional or advanced details;
* footnotes, abbreviations, tooltips, and glossary entries where useful;
* clearly labeled code blocks with appropriate language identifiers;
* page metadata and navigation where supported by the existing configuration.

Do not overuse these features. Prefer ordinary prose when it communicates the idea more clearly.

### Cross-references

Treat the documentation as a connected system rather than isolated pages.

* Cross-reference related concepts, guides, and reference pages.
* Link a term to its canonical explanation instead of redefining it everywhere.
* Use stable relative links compatible with MkDocs.
* Use descriptive link text—not “here” or raw filenames.
* Avoid duplicate explanations; keep one authoritative explanation and link to it.
* Verify that every internal link and heading anchor resolves correctly.

### Level of detail

Choose detail according to the reader’s needs:

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

Produce or update the Markdown pages, `mkdocs.yml` navigation, and required Material extensions. Finish by checking:

* navigation and page hierarchy;
* internal links and anchors;
* terminology consistency;
* code examples and commands;
* duplicated or unnecessarily detailed content;
* compatibility with the project’s MkDocs configuration.

The final result should let a technically competent newcomer understand the project, get it running, 
form the correct mental model, and find deeper reference information without being overwhelmed by implementation details.
