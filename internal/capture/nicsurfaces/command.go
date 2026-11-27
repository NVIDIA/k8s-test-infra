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

package nicsurfaces

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/cmdline"
)

// Command is `mokka-capture nic-surfaces`.
func Command() *cli.Command {
	return &cli.Command{
		Name:  "nic-surfaces",
		Usage: "print a captured node's NIC surfaces as one normalized list, object, attribute, value and source file",
		Description: "Reads the files `collect network` wrote for one node and keys each record by what the hardware fixes " +
			"(a PCI function's address, a port's number), so two nodes' lists compare record by record.",
		Arguments: []cli.Argument{&cli.StringArg{Name: "node-dir", Required: true}},
		Action: func(_ context.Context, cmd *cli.Command) error {
			if err := cmdline.RejectExtraArgs(cmd); err != nil {
				return err
			}
			n, err := Read(cmd.StringArg("node-dir"))
			if err != nil {
				_, _ = fmt.Fprintln(cmd.Root().ErrWriter, err)
				return cmdline.Exit(1)
			}
			if err := n.Write(cmd.Root().Writer); err != nil {
				_, _ = fmt.Fprintln(cmd.Root().ErrWriter, err)
				return cmdline.Exit(1)
			}
			return nil
		},
	}
}
