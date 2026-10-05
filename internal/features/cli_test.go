// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package features

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"k8s.io/apimachinery/pkg/util/version"
	"k8s.io/component-base/featuregate"
)

// testSpecs holds gates at each stage, each declared the way the contributing
// guide describes, with earlier specs kept as history.
var testSpecs = map[featuregate.Feature]featuregate.VersionedSpecs{
	"AlphaGate": {
		{Version: version.MajorMinor(0, 5), Default: false, PreRelease: featuregate.Alpha},
	},

	"OtherAlphaGate": {
		{Version: version.MajorMinor(0, 5), Default: false, PreRelease: featuregate.Alpha},
	},

	"BetaGate": {
		{Version: version.MajorMinor(0, 5), Default: false, PreRelease: featuregate.Alpha},
		{Version: version.MajorMinor(0, 6), Default: true, PreRelease: featuregate.Beta},
	},

	"GAGate": {
		{Version: version.MajorMinor(0, 5), Default: true, PreRelease: featuregate.Beta},
		{Version: version.MajorMinor(0, 6), Default: true, PreRelease: featuregate.GA, LockToDefault: true},
	},

	"DeprecatedGate": {
		{Version: version.MajorMinor(0, 5), Default: true, PreRelease: featuregate.Beta},
		{Version: version.MajorMinor(0, 6), Default: false, PreRelease: featuregate.Deprecated},
	},
}

func TestConfigure(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		value     string
		wantErr   string
		wantGates []any
		wantWarn  []string
	}{
		{
			// Mokka's spec versions must stay older than the Kubernetes
			// version the gate emulates, or the newer specs do not apply.
			name:      "newest spec of every gate applies",
			wantGates: []any{"AlphaGate=false", "BetaGate=true", "DeprecatedGate=false", "GAGate=true", "OtherAlphaGate=false"},
		},
		{
			name:      "explicit entry overrides AllAlpha",
			value:     "AllAlpha=true,AlphaGate=false",
			wantGates: []any{"AlphaGate=false", "BetaGate=true", "DeprecatedGate=false", "GAGate=true", "OtherAlphaGate=true"},
		},
		{
			name:      "setting a GA or deprecated gate warns",
			value:     "GAGate=true,DeprecatedGate=true",
			wantGates: []any{"AlphaGate=false", "BetaGate=true", "DeprecatedGate=true", "GAGate=true", "OtherAlphaGate=false"},
			wantWarn:  []string{"DeprecatedGate stage=deprecated", "GAGate stage=GA"},
		},
		{
			name:    "unknown gate",
			value:   "Nope=true",
			wantErr: `invalid --feature-gates "Nope=true": unrecognized feature gate: Nope`,
		},
		{
			name:    "locked gate",
			value:   "GAGate=false",
			wantErr: "cannot set feature gate GAGate to false, feature is locked to true",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// A fresh gate per case, because a gate keeps a rejected value. It
			// emulates the same versions as Mokka's gate, so the specs resolve
			// as they do in the services.
			g := featuregate.NewVersionedFeatureGateWithMinCompatibility(gate.EmulationVersion(), gate.MinCompatibilityVersion())
			require.NoError(t, g.AddVersioned(testSpecs))
			core, logs := observer.New(zapcore.InfoLevel)

			err := configure(g, tc.value, zap.New(core))
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)

			var warned []string
			for _, entry := range logs.FilterLevelExact(zapcore.WarnLevel).All() {
				fields := entry.ContextMap()
				warned = append(warned, fmt.Sprintf("%s stage=%s", fields["gate"], fields["stage"]))
			}
			require.Equal(t, tc.wantWarn, warned)

			states := logs.FilterMessage("feature gates").All()
			require.Len(t, states, 1)
			require.Equal(t, tc.wantGates, states[0].ContextMap()["gates"])
		})
	}
}

// Until a gate is registered, the services must start without logging gates,
// even when the built-in toggles are set.
func TestConfigureWithoutGates(t *testing.T) {
	t.Parallel()

	g, ok := gate.DeepCopy().(mutableGate)
	require.True(t, ok)
	core, logs := observer.New(zapcore.InfoLevel)

	require.NoError(t, configure(g, "AllAlpha=true,AllBeta=false", zap.New(core)))
	require.Zero(t, logs.Len())
}

// The documented environment variable feeds the flag, and the flag replaces it
// rather than merging with it.
func TestCLIFlagEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "environment only", want: "AllAlpha=true"},
		{name: "flag replaces environment", args: []string{"--feature-gates=AllBeta=false"}, want: "AllBeta=false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MOKKA_FEATURE_GATES", "AllAlpha=true")

			var got string
			command := &cli.Command{
				Flags: []cli.Flag{CLIFlag()},
				Action: func(_ context.Context, cmd *cli.Command) error {
					got = cmd.String(FlagName)
					return nil
				},
			}
			require.NoError(t, command.Run(context.Background(), append([]string{"test"}, tc.args...)))
			require.Equal(t, tc.want, got)
		})
	}
}
