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
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunDispatchesAndRejectsUnknownCommands(t *testing.T) {
	var out bytes.Buffer
	require.Equal(t, 0, run([]string{"help"}, &out, io.Discard))
	require.Contains(t, out.String(), "collect")

	require.Equal(t, 2, run(nil, io.Discard, io.Discard))
	require.Equal(t, 2, run([]string{"nope"}, io.Discard, io.Discard))
	require.Equal(t, 2, run([]string{"diff"}, io.Discard, io.Discard))
	require.Equal(t, 2, run([]string{"diff", "smi", "only-one"}, io.Discard, io.Discard))
	require.Equal(t, 2, run([]string{"diff", "nvml", "only-one"}, io.Discard, io.Discard))
	require.Equal(t, 2, run([]string{"scrub"}, io.Discard, io.Discard))
	require.Equal(t, 2, run([]string{"dump", "extra"}, io.Discard, io.Discard))
	require.Equal(t, 0, run([]string{"collect", "-h"}, io.Discard, io.Discard))
}
