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
	"os"
	"path/filepath"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
)

func TestInit_HoldsTheControlDeviceUntilTheLastShutdown(t *testing.T) {
	t.Parallel()
	e := NewEngine(&Config{NumDevices: 1, DriverVersion: "550.163.01"})
	e.controlDevicePath = filepath.Join(t.TempDir(), "nvidiactl")
	require.NoError(t, os.WriteFile(e.controlDevicePath, nil, 0o600))

	require.Equal(t, nvml.SUCCESS, e.Init())
	held := e.controlDevice
	require.NotNil(t, held, "an initialized client holds the driver, as real NVML does")

	require.Equal(t, nvml.SUCCESS, e.Init())
	require.Same(t, held, e.controlDevice, "a nested init opens nothing more")

	require.Equal(t, nvml.SUCCESS, e.Shutdown())
	require.Same(t, held, e.controlDevice, "the outer init still holds it")

	require.Equal(t, nvml.SUCCESS, e.Shutdown())
	require.Nil(t, e.controlDevice)
	_, err := held.Stat()
	require.ErrorIs(t, err, os.ErrClosed)
}

// On a node without a driver serving the device, as anywhere the simulated
// module is not loaded, NVML initializes all the same.
func TestInit_SucceedsWithoutAControlDevice(t *testing.T) {
	t.Parallel()
	e := NewEngine(&Config{NumDevices: 1, DriverVersion: "550.163.01"})
	e.controlDevicePath = filepath.Join(t.TempDir(), "absent")

	require.Equal(t, nvml.SUCCESS, e.Init())
	require.Nil(t, e.controlDevice)
	require.Equal(t, nvml.SUCCESS, e.Shutdown())
}
