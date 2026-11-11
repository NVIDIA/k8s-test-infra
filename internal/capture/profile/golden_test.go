// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package profile

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	ibconfig "github.com/NVIDIA/k8s-test-infra/internal/ib/config"
	"github.com/NVIDIA/k8s-test-infra/pkg/gpu/mocknvml/engine"
)

var update = flag.Bool("update", false, "rewrite the golden profiles from the committed captures")

// goldens are committed captures and the profile each generates. A change to
// a rule shows up as a diff of the golden file, which is the review of it.
var goldens = []struct {
	capture string
	profile string
}{
	{"../../../tests/hardware-captures/vr-nvl72/node-a", "testdata/mock-nvml-config-vrnvl72.yaml"},
}

func generateGolden(t *testing.T, capturePath, profilePath string) (string, string) {
	t.Helper()
	c, err := loadCapture(capturePath)
	require.NoError(t, err)
	g, err := generate(c, nil)
	require.NoError(t, err)
	return g.documents(filepath.Base(profilePath))
}

func TestGoldenProfiles(t *testing.T) {
	t.Parallel()
	for _, gc := range goldens {
		profile, mig := generateGolden(t, gc.capture, gc.profile)
		if *update {
			require.NoError(t, os.WriteFile(gc.profile, []byte(profile), 0o644))
			require.NoError(t, os.WriteFile(migSibling(gc.profile), []byte(mig), 0o644))
		}
		want, err := os.ReadFile(gc.profile)
		require.NoError(t, err)
		require.Equal(t, string(want), profile, "regenerate with: go test ./internal/capture/profile -update")
		wantMIG, err := os.ReadFile(migSibling(gc.profile))
		require.NoError(t, err)
		require.Equal(t, string(wantMIG), mig)
	}
}

func TestGenerationIsDeterministic(t *testing.T) {
	t.Parallel()
	gc := goldens[0]
	first, firstMIG := generateGolden(t, gc.capture, gc.profile)
	for range 3 {
		again, againMIG := generateGolden(t, gc.capture, gc.profile)
		require.Equal(t, first, again)
		require.Equal(t, firstMIG, againMIG)
	}
}

// newerSchemaKeys are keys the generator writes from the capture that this
// engine does not read yet. An older engine ignores them; each comes out of
// the list once the engine reads it.
var newerSchemaKeys = [][]string{
	{"system", "product_name"},
	{"system", "absent_features"},
	{"device_defaults", "egm"},
	{"device_defaults", "thermal", "tlimit"},
	{"device_defaults", "power", "supported_power_mizer_modes"},
}

func TestGoldenProfilesLoadInTheEngine(t *testing.T) {
	// LoadYAMLConfig resolves the MIG table beside the profile unless this
	// variable names another, so it must be empty here.
	t.Setenv(engine.EnvMIGProfilesConfig, "")
	for _, gc := range goldens {
		cfg, err := engine.LoadYAMLConfig(gc.profile)
		require.NoError(t, err)
		require.Len(t, cfg.Devices, 4)
		require.NotNil(t, cfg.DeviceDefaults.MIG)
		require.NotEmpty(t, cfg.DeviceDefaults.MIG.SupportedProfiles, "the MIG table beside the profile is attached")

		// Every key must be one the engine knows: a misspelt key would be
		// ignored silently by the lenient loader.
		raw, err := os.ReadFile(gc.profile)
		require.NoError(t, err)
		var doc map[string]any
		require.NoError(t, yaml.Unmarshal(raw, &doc))
		for _, path := range newerSchemaKeys {
			drop(doc, path)
		}
		// The NIC block belongs to the InfiniBand simulation, which reads
		// the same file for that key alone.
		nics, err := yaml.Marshal(map[string]any{"infiniband": doc["infiniband"]})
		require.NoError(t, err)
		var ib ibconfig.Profile
		require.NoError(t, yaml.UnmarshalStrict(nics, &ib))
		require.True(t, ib.Infiniband.Enabled)
		delete(doc, "infiniband")

		stripped, err := yaml.Marshal(doc)
		require.NoError(t, err)
		var strict engine.YAMLConfig
		require.NoError(t, yaml.UnmarshalStrict(stripped, &strict))
	}
}

func drop(m map[string]any, path []string) {
	for _, k := range path[:len(path)-1] {
		next, ok := m[k].(map[string]any)
		if !ok {
			return
		}
		m = next
	}
	delete(m, path[len(path)-1])
}
