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

// nvml-mock-ctl mutates the nvml-mock runtime config override so a running node's
// simulated GPU state can be changed without a Helm upgrade or pod restart.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/urfave/cli/v3"
)

const (
	defaultConfigOverride = "/var/lib/nvml-mock/driver/config/overrides.yaml"
	defaultConfig         = "/var/lib/nvml-mock/driver/config/config.yaml"
)

// Exit codes the CLI's callers branch on: a bad invocation or an invalid value
// is reported separately from a node whose override file could not be written.
const (
	exitFailure = 1
	exitUsage   = 2
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run executes one invocation and returns its exit code. Keeping the process
// exit in main() alone lets a test drive the CLI end to end.
func run(args []string, stdout, stderr io.Writer) int {
	root := newCLI(stdout, stderr)

	err := root.Run(context.Background(), append([]string{root.Name}, args...))
	if err == nil {
		return 0
	}

	// A usage error has already written the offending command's help, and
	// carries no message of its own so nothing is reported twice.
	if err.Error() != "" {
		fprintf(stderr, "%v\n", err)
	}

	var coder cli.ExitCoder
	if errors.As(err, &coder) {
		return coder.ExitCode()
	}
	return exitUsage
}

func newCLI(stdout, stderr io.Writer) *cli.Command {
	root := &cli.Command{
		Name:  "nvml-mock-ctl",
		Usage: "mutate the simulated GPU state of a running nvml-mock node",
		// Root flags are persistent, so --file and --config keep working on
		// either side of the subcommand as they did under the hand-rolled
		// parser this replaces.
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "file",
				Usage:   "config override path",
				Value:   defaultConfigOverride,
				Sources: cli.EnvVars("MOCK_NVML_OVERRIDES"),
			},
			&cli.StringFlag{
				Name:    "config",
				Usage:   "config path for UUID resolution and validation",
				Value:   defaultConfig,
				Sources: cli.EnvVars("MOCK_NVML_CONFIG"),
			},
		},
		Commands: []*cli.Command{
			failCommand(),
			temperatureCommand(),
			powerCommand(),
			fanCommand(),
			utilizationCommand(),
			clocksCommand(),
			throttleCommand(),
			pstateCommand(),
			nvlinkErrorCommand(),
			sramECCCommand(),
			fabricHealthCommand(),
			migCommand(),
			setCommand(),
			statusCommand(),
			resetCommand(),
			watchAllocationsCommand(),
		},
		Action:    usageAction,
		Writer:    stdout,
		ErrWriter: stderr,
		// The default handler prints the error and exits the process itself;
		// run() owns both so the CLI stays an ordinary function.
		ExitErrHandler: func(context.Context, *cli.Command, error) {},
	}
	reportUsageErrors(root)
	return root
}

// usageAction runs when no subcommand matched, on the root or on a command
// that only groups verbs. Neither does anything on its own, so a bare
// invocation and an unknown subcommand are both usage errors. reportUsage
// picks the help template from the command it is given, so the same action
// serves both.
func usageAction(_ context.Context, cmd *cli.Command) error {
	if name := cmd.Args().First(); name != "" {
		fprintf(cmd.Root().ErrWriter, "unknown command %q\n\n", name)
	}
	return reportUsage(cmd)
}

// reportUsageErrors routes every command's flag and positional-argument errors
// through reportUsage. The library's own reporting splits a failure across both
// streams — message on stderr, help on stdout — and leaves the error for run()
// to print a second time.
func reportUsageErrors(cmd *cli.Command) {
	cmd.OnUsageError = func(_ context.Context, errCmd *cli.Command, err error, _ bool) error {
		fprintf(errCmd.Root().ErrWriter, "%v\n\n", err)
		return reportUsage(errCmd)
	}
	for _, sub := range cmd.Commands {
		reportUsageErrors(sub)
	}
}

// reportUsage writes cmd's help to stderr and returns the usage exit code
// carrying an empty message, marking the failure as already reported.
func reportUsage(cmd *cli.Command) error {
	template := cli.CommandHelpTemplate
	switch {
	case cmd == cmd.Root():
		template = cli.RootCommandHelpTemplate
	case len(cmd.VisibleCommands()) > 0:
		// CommandHelpTemplate renders no COMMANDS section, so a command that
		// groups verbs would report the verbs nowhere — on the very path an
		// operator reaches by forgetting one. VisibleCommands is the same test
		// the library applies when it picks a template for `--help`.
		template = cli.SubcommandHelpTemplate
	}
	// The library's ShowHelp helpers always write to the root's stdout writer;
	// help printed because an invocation was wrong belongs on stderr.
	cli.HelpPrinter(cmd.Root().ErrWriter, template, cmd)
	return cli.Exit("", exitUsage)
}

// failf reports an infrastructure failure — the override file could not be
// locked, read or written — which exits 1 rather than the usage code.
func failf(format string, a ...any) error {
	return cli.Exit(fmt.Sprintf(format, a...), exitFailure)
}

// Writes to the CLI's stdout/stderr can't meaningfully fail-recover, so these
// helpers swallow the error (and satisfy errcheck, which only whitelists the
// literal os.Stdout/os.Stderr destinations, not io.Writer parameters).
func fprint(w io.Writer, a ...any)                 { _, _ = fmt.Fprint(w, a...) }
func fprintln(w io.Writer, a ...any)               { _, _ = fmt.Fprintln(w, a...) }
func fprintf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
