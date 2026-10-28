// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package inject

import "go.uber.org/zap"

// attachIMEXChannels delivers the mock IMEX channel nodes to a container that
// opted in.
//
// This step is deliberately outside attachGPUs' MEP-0002 suppression. That rule
// exists because the device plugin already delivered the GPUs the scheduler
// allocated, so re-injecting the GPU tree would widen the container past its
// allocation. The device plugin has no concept of an IMEX channel and never
// delivers one, so there is no allocation to widen — suppressing channels here
// would instead deny a ComputeDomain workload the fabric it explicitly asked for.
func attachIMEXChannels(cfg Config, adjustment *Adjustment) {
	// Fail open like the GPU path: the node agent stages channels only on
	// multi-node NVLink profiles (nvlink.multi_node), so an annotation on a
	// node without them must not block the pod.
	// Delivers every channel the node agent staged, which is channel0 only.
	channels, err := discoverImexChannels(cfg.ImexChannelHostPath)
	if err != nil {
		zap.L().Warn("imex channel injection requested but the channel tree is unavailable; "+
			"injecting without channels (does the node's GPU profile set nvlink.multi_node?)",
			zap.String("path", cfg.ImexChannelHostPath), zap.Error(err))
		return
	}
	adjustment.Devices = append(adjustment.Devices, channels...)
}
