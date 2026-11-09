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

package cmdline

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

func TestRunReturnsTheActionsExitCode(t *testing.T) {
	t.Parallel()
	for _, code := range []int{0, 1, 130} {
		root := &cli.Command{Name: "tool", Action: func(context.Context, *cli.Command) error { return Exit(code) }}
		var stdout, stderr bytes.Buffer
		require.Equal(t, code, Run(context.Background(), root, nil, &stdout, &stderr))
		require.Empty(t, stderr.String(), "an action that exits through Exit has reported its failure itself")
	}
}

// A usage error goes to standard error alone: the reason, then the help of the
// command that was invoked wrongly.
func TestRunReportsUsageErrorsOnStandardError(t *testing.T) {
	t.Parallel()
	for args, reason := range map[string]string{
		"nope":        `unknown command "nope"`,
		"leaf -n x f": `invalid value "x" for flag -n`,
		"leaf":        `Required argument "file" not set`,
		"leaf f g":    `unexpected argument "g"`,
	} {
		root := &cli.Command{
			Name:   "tool",
			Action: UnknownCommand,
			Commands: []*cli.Command{{
				Name:      "leaf",
				Flags:     []cli.Flag{&cli.IntFlag{Name: "n"}},
				Arguments: []cli.Argument{&cli.StringArg{Name: "file", Required: true}},
				Action:    func(_ context.Context, cmd *cli.Command) error { return RejectExtraArgs(cmd) },
			}},
		}
		var stdout, stderr bytes.Buffer
		require.Equal(t, 2, Run(context.Background(), root, strings.Fields(args), &stdout, &stderr), args)
		require.Empty(t, stdout.String(), args)
		require.True(t, strings.HasPrefix(stderr.String(), reason), "%s: %s", args, stderr.String())
		require.Contains(t, stderr.String(), "USAGE:", args)
	}
}
