// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

// The GPU Operator runs its container toolkit installer in this container, in
// the pods of its toolkit DaemonSet.
const (
	toolkitAppLabel      = "app"
	toolkitApp           = "nvidia-container-toolkit-daemonset"
	toolkitInstallerName = "nvidia-container-toolkit-ctr"
)

// cdiDefaultKindEnv names the CDI kind the toolkit's runtime resolves
// NVIDIA_VISIBLE_DEVICES against. mokkaCDIKind is the kind of the spec the node
// agent publishes as /var/run/cdi/nvidia.yaml.
const (
	cdiDefaultKindEnv = "NVIDIA_CONTAINER_RUNTIME_MODES_CDI_DEFAULT_KIND"
	mokkaCDIKind      = "nvidia.com/gpu"
)

// GPUOperatorToolkit is the GPU Operator's container toolkit installer, whose
// runtime it points at the node agent's CDI spec.
type GPUOperatorToolkit struct{}

var _ Workload = GPUOperatorToolkit{}

// Matches reports whether container is the toolkit installer.
func (GPUOperatorToolkit) Matches(container Container) bool {
	return container.Name == toolkitInstallerName && container.PodLabels[toolkitAppLabel] == toolkitApp
}

// Adjust sets the runtime's CDI kind. The installer writes the runtime
// configuration from its environment, where the operator has the runtime
// resolve the NVIDIA_VISIBLE_DEVICES of its own operands against the management
// spec the installer generates from the driver root. That spec names the real
// NVML the driver container installed; the node agent's serves the mock, with
// the node's profile beside it.
func (GPUOperatorToolkit) Adjust(container Container) (Adjustment, bool) {
	env := newEnvSet(container.Env)
	env.set(cdiDefaultKindEnv, mokkaCDIKind)
	return Adjustment{Env: env.changed()}, true
}
