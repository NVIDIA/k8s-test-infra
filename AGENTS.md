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

## Docs

- When leaving a comment in code, it should explain intent where it's not obvious — why we are doing something, not what we are doing, in most cases. Don't write comments based on the current conversation context; they should generally be valuable long-term for readers.
- Any documentation you produce must be concise and straight to the point.
