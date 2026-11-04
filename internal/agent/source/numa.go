// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package source

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/NVIDIA/k8s-test-infra/internal/agent"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/host"
	"github.com/NVIDIA/k8s-test-infra/internal/agent/numatopology"
)

// WithNUMA enables host-resolved topology. Configure it before calling Watch;
// disabled simulation leaves the source independent of host topology.
func (f *FileSource) WithNUMA(h *host.Host, enabled bool) *FileSource {
	f.numaHost = nil
	if enabled {
		f.numaHost = h
	}
	return f
}

func (f *FileSource) buildState(ctx context.Context, data []byte) (*agent.State, error) {
	state, err := compileState(data, f.configPath)
	if err != nil {
		return nil, err
	}
	state.ConfigRaw = data
	if f.numaHost == nil {
		return state, nil
	}
	return numatopology.ResolveState(ctx, f.numaHost, state)
}

func numaStateHash(inputs [32]byte, topology *agent.NUMATopology) ([32]byte, error) {
	data, err := json.Marshal(topology)
	if err != nil {
		return [32]byte{}, fmt.Errorf("hash resolved NUMA topology: %w", err)
	}
	return sha256.Sum256(append(inputs[:], data...)), nil
}
