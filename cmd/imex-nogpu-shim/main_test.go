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

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildArgv(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "no args appends nogpu",
			args: nil,
			want: []string{"/usr/bin/nvidia-imex.real", "--nogpu"},
		},
		{
			name: "upstream invocation preserved and nogpu appended",
			args: []string{"-c", "/etc/nvidia-imex/imexd.cfg"},
			want: []string{"/usr/bin/nvidia-imex.real", "-c", "/etc/nvidia-imex/imexd.cfg", "--nogpu"},
		},
		{
			name: "existing --nogpu not duplicated",
			args: []string{"-c", "/cfg", "--nogpu"},
			want: []string{"/usr/bin/nvidia-imex.real", "-c", "/cfg", "--nogpu"},
		},
		{
			name: "existing single-dash -nogpu not duplicated",
			args: []string{"-nogpu", "-c", "/cfg"},
			want: []string{"/usr/bin/nvidia-imex.real", "-nogpu", "-c", "/cfg"},
		},
		{
			name: "nogpu-prefixed value is not the flag",
			args: []string{"-c", "--nogpufoo"},
			want: []string{"/usr/bin/nvidia-imex.real", "-c", "--nogpufoo", "--nogpu"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, buildArgv("/usr/bin/nvidia-imex.real", tt.args))
		})
	}
}

func TestRealBin(t *testing.T) {
	t.Run("default when env unset", func(t *testing.T) {
		t.Setenv(envRealBin, "")
		assert.Equal(t, "/usr/bin/nvidia-imex.real", realBin())
	})
	t.Run("env override wins", func(t *testing.T) {
		t.Setenv(envRealBin, "/opt/imex/nvidia-imex")
		assert.Equal(t, "/opt/imex/nvidia-imex", realBin())
	})
	t.Run("constant pins the documented literal", func(t *testing.T) {
		assert.Equal(t, "IMEX_SHIM_REAL_BIN", envRealBin,
			"documented in Dockerfile.compute-domain-daemon's shim stage; the constant must match")
	})
}
