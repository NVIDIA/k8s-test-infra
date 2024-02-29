/*
 * Copyright (c) 2024, NVIDIA CORPORATION.  All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"os"

	"k8s.io/klog/v2"

	"github.com/NVIDIA/k8s-ci-artifacts/cmd/nv-ci-bot/retitle"
	cli "github.com/urfave/cli/v2"
)

const (
	// ProgramName is the canonical name of this program
	ProgramName = "nv-ci-bot"
)

type config struct {
	Debug int
}

func main() {
	config := config{}

	// Create the top-level CLI
	c := cli.NewApp()
	c.Name = ProgramName
	c.Usage = "NV GitHub bot for CI/CD automation"
	c.Version = "0.1.0"

	// Setup the flags for this command
	c.Flags = []cli.Flag{
		&cli.IntFlag{
			Name:        "debug",
			Aliases:     []string{"d"},
			Usage:       "Enable debug-level logging",
			Destination: &config.Debug,
			Value:       0,
			EnvVars:     []string{"DEBUG"},
		},
	}

	log := klog.NewKlogr().V(config.Debug)

	// Define the subcommands
	c.Commands = []*cli.Command{
		retitle.NewCommand(&log),
	}

	err := c.Run(os.Args)
	if err != nil {
		log.Error(err, "Error running command")
		os.Exit(1)
	}
}
