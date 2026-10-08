# nvidia-imex-shim

`execve` wrapper staged by the node agent beside the real IMEX daemon in
Mokka's node-local driver tree.

The upstream daemon hard-codes `nvidia-imex -c /imexd/imexd.cfg` with no flag passthrough,
so `--nogpu` cannot be injected from outside. This shim resolves
`nvidia-imex.real` beside its own executable and appends `--nogpu`, preserving
all caller arguments, environment, and stdio. The `exec` replaces the shim
process — no wrapper lingers, signals reach the daemon directly.

Remove once upstream supports flag passthrough. See NVIDIA/k8s-test-infra#304.
