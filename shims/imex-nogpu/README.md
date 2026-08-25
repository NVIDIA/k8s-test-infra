# imex-nogpu

`execve` wrapper installed at `/usr/bin/nvidia-imex` in the compute-domain-daemon image.

The upstream daemon hard-codes `nvidia-imex -c /imexd/imexd.cfg` with no flag passthrough,
so `--nogpu` cannot be injected from outside. This shim execs the real binary (at
`/usr/bin/nvidia-imex.real`) with `--nogpu` appended, preserving all caller arguments,
environment, and stdio. The `exec` replaces the shim process — no wrapper lingers, signals
reach the daemon directly.

Remove once upstream supports flag passthrough. See NVIDIA/k8s-test-infra#304.
