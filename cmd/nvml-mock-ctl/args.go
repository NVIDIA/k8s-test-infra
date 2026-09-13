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
	"fmt"
	"math"
	"strings"

	"github.com/urfave/cli/v3"
)

// The positional values of the convenience commands. urfave/cli parses and
// types them; the arity and range checks that keep a typo from being published
// as a plausible reading live here.
//
// A required single value is declared as a one-value slice rather than the
// singular Arg types, which accept an absent value and leave the zero value
// behind. With Min 1 the action only runs once the value has parsed, so the
// readers below can index the result.

func requiredIntArg(name string) cli.Argument {
	return &cli.IntArgs{Name: name, UsageText: name, Min: 1, Max: 1}
}

func requiredFloatArg(name string) cli.Argument {
	return &cli.FloatArgs{Name: name, UsageText: name, Min: 1, Max: 1}
}

// requiredStringArgs declares one or more mandatory values, as the commands
// taking a list of throttle reasons, fabric conditions or assignments need.
func requiredStringArgs(name string) cli.Argument {
	return &cli.StringArgs{Name: name, UsageText: fmt.Sprintf("%s [%s ...]", name, name), Min: 1, Max: -1}
}

// intArg reads a single integer positional, bounded to [lo, hi].
func intArg(cmd *cli.Command, name string, lo, hi int) (int, error) {
	if err := rejectExtraArgs(cmd, name); err != nil {
		return 0, err
	}
	v := cmd.IntArgs(name)[0]
	if v < lo || v > hi {
		return 0, fmt.Errorf("%s value %d out of range [%d,%d]", name, v, lo, hi)
	}
	return v, nil
}

// floatArg reads a single floating-point positional, bounded to [lo, hi]. NaN
// and +/-Inf are rejected: they parse without error and would otherwise slip
// past a bare comparison and overflow a downstream integer conversion.
func floatArg(cmd *cli.Command, name string, lo, hi float64) (float64, error) {
	if err := rejectExtraArgs(cmd, name); err != nil {
		return 0, err
	}
	v := cmd.FloatArgs(name)[0]
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%s value %v must be finite", name, v)
	}
	if v < lo || v > hi {
		return 0, fmt.Errorf("%s value %v out of range [%v,%v]", name, v, lo, hi)
	}
	return v, nil
}

// rejectExtraArgs fails a single-value command that was given more than one.
// Values a command's arguments did not consume are left in Args() rather than
// being reported, so `temp --gpu 0 85 90` would otherwise silently apply 85.
func rejectExtraArgs(cmd *cli.Command, name string) error {
	if extra := cmd.Args().Slice(); len(extra) > 0 {
		return fmt.Errorf("%s takes exactly one value; unexpected %q", name, strings.Join(extra, " "))
	}
	return nil
}
