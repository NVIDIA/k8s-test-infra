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

// Package cmdline runs the mokka-capture command line on urfave/cli with the
// tool's exit codes: 0 when a command completed, 2 for a bad invocation, and
// otherwise the code a command's action exits with. A bad invocation is
// reported on standard error, with the reason and the command's help, so a
// script that reads a command's standard output never receives help text.
package cmdline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/urfave/cli/v3"
)

// exitUsage is the exit code of a bad invocation: an unknown command or flag,
// a missing or extra argument, or a flag value that does not parse.
const exitUsage = 2

// Run runs root with args, which leave out the program name, and returns the
// exit code. root and every command under it write to stdout and stderr.
func Run(ctx context.Context, root *cli.Command, args []string, stdout, stderr io.Writer) int {
	root.Writer, root.ErrWriter = stdout, stderr
	// The default handler prints the error and exits the process itself; Run
	// returns the code instead, so the caller owns the exit.
	root.ExitErrHandler = func(context.Context, *cli.Command, error) {}
	reportUsageErrors(root)

	err := root.Run(ctx, append([]string{root.Name}, args...))
	if err == nil {
		return 0
	}
	// A usage error has been reported already, and an action that exits
	// through Exit has printed its own messages, so neither carries one.
	if msg := err.Error(); msg != "" {
		_, _ = fmt.Fprintln(stderr, msg)
	}
	var coder cli.ExitCoder
	if errors.As(err, &coder) {
		return coder.ExitCode()
	}
	return exitUsage
}

// Exit is what an action returns to exit with code once it has reported the
// failure itself; code 0 is success.
func Exit(code int) error {
	if code == 0 {
		return nil
	}
	return cli.Exit("", code)
}

// UnknownCommand is the action of a command that only groups others. It runs
// when the invocation names none of them.
func UnknownCommand(_ context.Context, cmd *cli.Command) error {
	reason := ""
	if name := cmd.Args().First(); name != "" {
		reason = fmt.Sprintf("unknown command %q", name)
	}
	return Usage(cmd, reason)
}

// RejectExtraArgs reports a usage error when cmd was given more positional
// arguments than it declares. urfave/cli leaves those in Args instead of
// failing, so a mistyped invocation would otherwise drop them silently.
func RejectExtraArgs(cmd *cli.Command) error {
	if extra := cmd.Args().Slice(); len(extra) > 0 {
		return Usage(cmd, fmt.Sprintf("unexpected argument %q", strings.Join(extra, " ")))
	}
	return nil
}

// Usage reports a bad invocation of cmd: the reason, when there is one, then
// cmd's help, all on standard error. Run exits 2 on the error it returns.
func Usage(cmd *cli.Command, reason string) error {
	w := cmd.Root().ErrWriter
	if reason != "" {
		_, _ = fmt.Fprintf(w, "%s\n\n", reason)
	}
	template := cli.CommandHelpTemplate
	switch {
	case cmd == cmd.Root():
		template = cli.RootCommandHelpTemplate
	case len(cmd.VisibleCommands()) > 0:
		template = cli.SubcommandHelpTemplate
	}
	// urfave/cli's Show*Help functions write to the root's standard output;
	// help printed because an invocation was wrong belongs on standard error.
	cli.HelpPrinter(w, template, cmd)
	return cli.Exit("", exitUsage)
}

// reportUsageErrors routes the flag and argument errors of cmd and of every
// command under it through Usage. urfave/cli's own reporting splits such an
// error across both streams: the message on standard error, the help on
// standard output.
func reportUsageErrors(cmd *cli.Command) {
	cmd.OnUsageError = func(_ context.Context, errCmd *cli.Command, err error, _ bool) error {
		return Usage(errCmd, err.Error())
	}
	for _, sub := range cmd.Commands {
		reportUsageErrors(sub)
	}
}
