// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

// A Workload is a specialized workload: the containers that one part of the
// stack authors, such as an operator's operand or a DRA driver's daemon, which
// get their adjustment from the workload in place of the generic overlay. They
// carry none of Mokka's annotations, so each workload picks its containers by
// identity, and holds the configuration it adjusts them with. Like every
// container the plugin adjusts, they wait for the node agent's staging gate.
type Workload interface {
	// Matches reports whether container is one of the workload's, from what
	// NRI presents at CreateContainer.
	Matches(container Container) bool
	// Adjust returns what to add to container, or ok=false to leave it as
	// authored. It never fails: like the generic overlay, a workload fails
	// open.
	Adjust(container Container) (Adjustment, bool)
}

// workload returns the workload that claims container, or nil. The built-in
// specialized workloads, such as the DRA ComputeDomain daemon, come first.
func (c Config) workload(container Container) Workload {
	for i := range specializedWorkloads {
		if specializedWorkloads[i].matches(container) {
			return builtinWorkload{spec: &specializedWorkloads[i], cfg: c}
		}
	}
	for _, workload := range c.Workloads {
		if workload.Matches(container) {
			return workload
		}
	}
	return nil
}

// builtinWorkload binds a built-in specialized workload to the configuration
// it adjusts with. Built-ins are always on, and Gated tells which of them skip
// the staging gate.
type builtinWorkload struct {
	spec *specializedWorkload
	cfg  Config
}

func (w builtinWorkload) Matches(container Container) bool { return w.spec.matches(container) }

func (w builtinWorkload) Adjust(container Container) (Adjustment, bool) {
	return w.spec.adjust(w.cfg, container)
}
