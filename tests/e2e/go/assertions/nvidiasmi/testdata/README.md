# nvidia-smi -q -x fixtures

Real `nvidia-smi -q -x` documents captured from the e2e kind cluster
(nvidia-smi 580.65.06, NVML 580.65, `nvsmi_device_v13.dtd`). Element names and
bodies are verbatim; the only edits are keeping the first two `<gpu>` blocks and
setting `attached_gpus` to 2, so the files stay readable while still exercising
per-GPU indexing and override scoping.

| File | Content |
| --- | --- |
| `qx-a100-healthy.xml` | Ampere. Absolute temperature thresholds, `gpu_temp_tlimit` = `N/A`. |
| `qx-gb200-healthy.xml` | Blackwell, captured before #679. `*_tlimit_threshold` elements; the absolute ones are absent. Still carries the fields Blackwell removed (`current_gom`, `retired_count`, `sparse_operation_mode`, ...), so `BlackwellRemovedFieldProblems` rejects it — that is deliberate: it is the pre-fix document the check has to fail. Use `qx-gb300-healthy.xml` as the current Blackwell reference. |
| `qx-gb300-healthy.xml` | Blackwell, captured after #679. Same threshold shape as `qx-gb200-healthy.xml`, with the removed fields reporting N/A. |
| `qx-gb200-lost.xml` | GPU 0 healthy, GPU 1 lost (`GPU is lost` bodies). |
| `qx-gb200-ecc-injected.xml` | GPU 0 has a non-zero `ecc_errors/aggregate/dram_uncorrectable`. |
| `qx-gb200-fabric-degraded.xml` | GPU 0 fabric healthy, GPU 1 `route_unhealthy` (#677). Taken against the fixed library, so it is also the healthy-fabric reference. |

Watch for two element names that repeat under different parents: `sm_clock`
appears under both `clocks` (current) and `max_clocks`, and `average_power_draw`
under `gpu_power_readings`, `gpu_memory_power_readings` and
`module_power_readings`. `--query-gpu` reads the first of each.

To recapture, deploy the chart with the wanted profile and run:

    kubectl exec -n mokka <nvml-mock-pod> -- nvidia-smi -q -x > qx-<profile>.xml

For the lost and ECC variants, inject first and wait out the 30 s override TTL:

    kubectl exec -n mokka <pod> -- nvml-mock-ctl fail --gpu 1 --mode lost
    kubectl exec -n mokka <pod> -- nvml-mock-ctl fail --gpu 0 --mode ecc_uncorrectable --after-calls 1
    kubectl exec -n mokka <pod> -- nvml-mock-ctl fabric-health --gpu 1 route_unhealthy

Then trim to two GPUs, keeping the header and the first two `<gpu>` blocks and
rewriting `attached_gpus` to 2.

A driver bump can rename elements. `schema.go` is the only place that names them.
