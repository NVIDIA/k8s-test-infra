// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package featuregate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"
)

const referenceURL = "https://github.com/NVIDIA/k8s-test-infra/issues/859"

func definition(id string, stage Stage) Definition {
	def := Definition{
		ID:           id,
		Description:  "test gate",
		Stage:        stage,
		FromVersion:  "v0.1.0",
		ReferenceURL: referenceURL,
	}
	if stage == StageStable || stage == StageDeprecated {
		def.ToVersion = "v0.3.0"
	}
	return def
}

func TestStageDefaults(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	alpha := registry.MustRegister(definition("mokka.alpha", StageAlpha))
	beta := registry.MustRegister(definition("mokka.beta", StageBeta))
	stable := registry.MustRegister(definition("mokka.stable", StageStable))
	deprecated := registry.MustRegister(definition("mokka.deprecated", StageDeprecated))

	require.False(t, alpha.IsEnabled())
	require.True(t, beta.IsEnabled())
	require.True(t, stable.IsEnabled())
	require.False(t, deprecated.IsEnabled())
}

func TestConfigure(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	alpha := registry.MustRegister(definition("mokka.alpha", StageAlpha))
	beta := registry.MustRegister(definition("mokka.beta", StageBeta))

	require.NoError(t, registry.Configure("+mokka.alpha, -mokka.beta"))
	require.True(t, alpha.IsEnabled())
	require.False(t, beta.IsEnabled())

	require.NoError(t, registry.Configure(""))
	require.False(t, alpha.IsEnabled())
	require.True(t, beta.IsEnabled())
}

func TestConfigureIsAtomic(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	alpha := registry.MustRegister(definition("mokka.alpha", StageAlpha))

	err := registry.Configure("mokka.alpha,mokka.unknown")
	require.ErrorContains(t, err, `unknown feature gate "mokka.unknown"`)
	require.False(t, alpha.IsEnabled())
}

func TestConfigureRejectsInvalidLifecycleOverrides(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registry.MustRegister(definition("mokka.stable", StageStable))
	registry.MustRegister(definition("mokka.deprecated", StageDeprecated))

	require.ErrorContains(t, registry.Configure("-mokka.stable"), "cannot be disabled")
	require.ErrorContains(t, registry.Configure("mokka.deprecated"), "cannot be enabled")
}

func TestRegisterRejectsInvalidDefinitions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		def  Definition
	}{
		{name: "invalid ID", def: definition("mokka/bad", StageAlpha)},
		{name: "missing description", def: func() Definition { d := definition("mokka.gate", StageAlpha); d.Description = ""; return d }()},
		{name: "missing version", def: func() Definition { d := definition("mokka.gate", StageAlpha); d.FromVersion = ""; return d }()},
		{name: "invalid URL", def: func() Definition { d := definition("mokka.gate", StageAlpha); d.ReferenceURL = "://"; return d }()},
		{name: "stable without removal", def: func() Definition { d := definition("mokka.gate", StageStable); d.ToVersion = ""; return d }()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewRegistry().Register(tc.def)
			require.Error(t, err)
		})
	}
}

func TestDefinitionsAreSorted(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registry.MustRegister(definition("mokka.zeta", StageAlpha))
	registry.MustRegister(definition("mokka.alpha", StageAlpha))

	definitions := registry.Definitions()
	require.Equal(t, []string{"mokka.alpha", "mokka.zeta"}, []string{definitions[0].ID, definitions[1].ID})
}

func TestCLIFlagReadsEnvironmentAndCLIWins(t *testing.T) {
	t.Setenv(EnvName, "mokka.fromEnvironment")

	var got string
	command := &cli.Command{
		Flags: []cli.Flag{CLIFlag()},
		Action: func(_ context.Context, cmd *cli.Command) error {
			got = cmd.String(FlagName)
			return nil
		},
	}

	require.NoError(t, command.Run(context.Background(), []string{"test", "--feature-gates=mokka.fromCLI"}))
	require.Equal(t, "mokka.fromCLI", got)
}
