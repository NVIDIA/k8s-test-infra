// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package engine

import (
	"testing"

	"github.com/NVIDIA/k8s-test-infra/pkg/fmcoord"
	"github.com/stretchr/testify/require"
)

// TestFabricCoordContract guards the marker/env contract shared between the
// mock NVML engine (which READS the readiness marker for `fabric.state: auto`)
// and the fake fabricmanager daemon/ctl in pkg/fmcoord (which WRITE it). The
// two packages deliberately do not import each other, so they agree only by
// convention; a rename in one without the other would silently break the
// coupling with no compile error. This test fails loudly instead.
func TestFabricCoordContract(t *testing.T) {
	require.Equal(t, fmcoord.EnvStateDir, EnvFabricStateDir, "state-dir env var drift")
	require.Equal(t, fmcoord.ReadyMarker, FabricReadyMarker, "ready-marker filename drift")
}
