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
	t.Parallel()
	var out bytes.Buffer
	require.Equal(t, 0, run([]string{"help"}, &out, io.Discard))
	require.Contains(t, out.String(), "collect")
	require.Equal(t, 0, run([]string{"collect", "-h"}, io.Discard, io.Discard))

	var stderr bytes.Buffer
	require.Equal(t, 2, run([]string{"nope"}, io.Discard, &stderr))
	require.Contains(t, stderr.String(), `unknown command "nope"`)

	for _, args := range [][]string{
		nil,
		{"diff"},
		{"diff", "nope"},
		{"diff", "smi", "only-one"},
		{"diff", "nvml", "only-one"},
		{"diff", "nvml", "a.json", "b.json", "c.json"},
		{"scrub"},
		{"profile"},
		{"dump", "extra"},
		{"collect"},
		{"collect", "nope"},
		{"collect", "-nope", "cluster"},
	} {
		var stdout, stderr bytes.Buffer
		require.Equal(t, 2, run(args, &stdout, &stderr), args)
		require.Empty(t, stdout.String(), "%v: a usage error goes to standard error", args)
		require.Contains(t, stderr.String(), "USAGE:", "%v: a usage error shows the command's help", args)
	}
}

// Each command's help documents its own flags and arguments, and no other
// command's.
func TestEachCommandHelpListsItsOwnFlags(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args        []string
		want, other string
	}{
		{[]string{"collect", "--help"}, "--dump-binary", "--replace"},
		{[]string{"collect", "driver", "--help"}, "driver [options] node [node ...]", "--replace"},
		{[]string{"scrub", "--help"}, "--replace", "--dump-binary"},
		{[]string{"profile", "-h"}, "profile [options] <capture node directory>", "--replace"},
		{[]string{"diff", "smi", "--help"}, "smi [options] real.xml mock.xml", "--explain"},
		{[]string{"diff", "nvml", "--help"}, "nvml [options] real.json mock.json", "--explain"},
		{[]string{"help", "dump"}, "mokka-capture dump", "--max"},
	} {
		var stdout bytes.Buffer
		require.Equal(t, 0, run(tc.args, &stdout, io.Discard), tc.args)
		require.Contains(t, stdout.String(), tc.want, tc.args)
		require.NotContains(t, stdout.String(), tc.other, tc.args)
	}
}
