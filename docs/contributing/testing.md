# Testing and Linting

Two commands cover almost everything:

```bash
make lint-fix    # fix what can be fixed, report the rest
make test        # unit tests with the race detector
```

## What `make lint` actually does

More than `golangci-lint`. It is the gate that most often surprises people,
because several of its steps fail on files you did not edit:

| Step | Fails when |
|---|---|
| `gen-check` | Generated code is stale — run `make gen` |
| `go mod tidy` diff | `go.mod` or `go.sum` is out of sync |
| Vendor check | A `vendor/` directory reappeared; dependencies resolve through the Go proxy |
| Go proxy digest | The vendored `setup-dgxc-goproxy` action was edited in place |
| Documentation links | A markdown link does not resolve anywhere in the repo |
| `go vet`, `golangci-lint`, `govulncheck` | The usual |

`make lint-fix` runs the same checks and auto-fixes what it can.

## The test layers

| Layer | Command | Needs a cluster |
|---|---|---|
| Unit | `make test` | no |
| Helm chart | `make helm-tests`, `make helm-crds-tests` | no |
| Shim integration | `make test-mockpcisysfs`, `make test-mocknvml-bridge` | no |
| E2E framework unit | `make test-e2e-framework` | no |
| End-to-end | `make e2e` and friends | **yes** |

Everything except the last runs on a laptop with no cluster.

### End-to-end

The E2E suite attaches to a cluster you already created; it does not build the
image or create the cluster. Set both up first with
[Tilt](local-development.md), then:

```bash
make e2e                  # standalone scenario, default profile
make e2e-dra              # DRA driver
make e2e-gpu-operator     # GPU Operator
make e2e-multi-node       # heterogeneous a100/t4 fleet
make e2e-nri              # node-wide NRI injection
make e2e-nfd              # NFD label provenance
```

!!! warning "`make e2e` does not run everything"
    It applies a default label filter that excludes the six scenarios above, so
    a bare `make e2e` runs only the standalone scenario. Use the per-scenario
    targets for the rest.

The suite's own manual — scenarios, Ginkgo labels, environment variables and
diagnostics — is in
[`tests/e2e/README.md`](https://github.com/NVIDIA/k8s-test-infra/blob/main/tests/e2e/README.md).
Which upstream consumer versions are actually exercised is recorded in
[`tests/e2e/VERSION-MATRIX.md`](https://github.com/NVIDIA/k8s-test-infra/blob/main/tests/e2e/VERSION-MATRIX.md).

## Writing tests

Use `testify/require` for assertions, and `t.Parallel()` wherever a test allows
it. Add tests that assert behaviour worth protecting rather than tests that
restate the implementation.

When a change touches the Helm chart, run `make helm-tests` — the chart has its
own unit-test suite and snapshot tests that catch template regressions no Go
test will.

## What CI runs

These are the gates. Running them locally reproduces CI exactly:

```bash
make lint
make test
make test-mockpcisysfs
make test-mocknvml-bridge
make test-e2e-framework
make helm-tests          # chart paths only
make helm-crds-tests     # chart paths only
make verify-greptile
```

!!! note "Why E2E did not run on your pull request"
    Unit, lint and chart checks run on the pull request itself. The E2E suite
    runs from a mirrored `pull-request/N` branch that copy-pr-bot creates, so it
    appears a little later and under a separate run — not on the PR event.

## Related

| To read about | See |
|---|---|
| Bringing up a cluster to test against | [Local Development](local-development.md) |
| Submitting the change | [Pull Requests](pull-requests.md) |
