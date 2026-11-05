// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"fmt"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
	"go.uber.org/zap"
	"k8s.io/component-base/featuregate"
	"k8s.io/klog/v2"
)

const (
	// FlagName is the command-line flag of the services that apply gates:
	// node-agent, nri-plugin and control-plane.
	FlagName = "feature-gates"
	// EnvName is the environment variable those services read when the flag
	// is not set.
	EnvName = "MOKKA_FEATURE_GATES"
)

// CLIFlag returns the --feature-gates flag of the services that apply gates.
// Like the Kubernetes components' flag, its help lists the alpha and beta
// gates with their defaults.
func CLIFlag() cli.Flag {
	// urfave/cli aligns only the first line of a usage; the tabs indent the
	// rest under it.
	return &cli.StringFlag{
		Name:    FlagName,
		Sources: cli.EnvVars(EnvName),
		Usage: "comma-separated Name=true|false pairs that enable or disable feature gates. Options are:\n\t" +
			strings.Join(gate.KnownFeatures(), "\n\t"),
	}
}

// ConfigureFromCLI applies the --feature-gates value and logs the resulting
// state. Call it once, before starting any workers.
func ConfigureFromCLI(cmd *cli.Command, logger *zap.Logger) error {
	return configure(gate, cmd.String(FlagName), logger)
}

// mutableGate is the part of component-base's feature gate that configure
// uses.
type mutableGate interface {
	SetWithLogger(logger klog.Logger, value string) error
	GetAll() map[featuregate.Feature]featuregate.FeatureSpec
	Enabled(key featuregate.Feature) bool
	ExplicitlySet(name featuregate.Feature) bool
}

// configure takes the gate as an argument so tests can give each case a fresh
// one: a gate keeps a rejected value, so every later Set on it fails too.
func configure(g mutableGate, value string, logger *zap.Logger) error {
	// component-base logs GA and deprecated overrides through klog, which
	// ignores Mokka's log format and level. Discard that and warn through zap.
	if err := g.SetWithLogger(klog.Logger{}, value); err != nil {
		return fmt.Errorf("invalid --%s %q: %w", FlagName, value, err)
	}

	specs := g.GetAll()
	// AllAlpha and AllBeta are component-base's built-in toggles, not Mokka
	// gates; their effect shows in the gates they change.
	delete(specs, "AllAlpha")
	delete(specs, "AllBeta")
	if len(specs) == 0 {
		return nil
	}

	names := make([]featuregate.Feature, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	slices.Sort(names)

	states := make([]string, 0, len(names))
	for _, name := range names {
		states = append(states, fmt.Sprintf("%s=%t", name, g.Enabled(name)))
		if !g.ExplicitlySet(name) {
			continue
		}
		// Setting a gate that is due for removal fails the upgrade that
		// removes it, so make the operator see it now.
		var stage string
		switch specs[name].PreRelease {
		case featuregate.GA:
			stage = "GA"
		case featuregate.Deprecated:
			stage = "deprecated"
		default:
			continue
		}
		logger.Warn("feature gate will be removed in a future release; stop setting it",
			zap.String("gate", string(name)), zap.String("stage", stage))
	}
	logger.Info("feature gates", zap.Strings("gates", states))
	return nil
}
