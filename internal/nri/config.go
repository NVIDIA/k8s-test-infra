// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package nri

import "github.com/NVIDIA/k8s-test-infra/internal/nri/inject"

const (
	defaultSocketPath = "/var/run/nri/nri.sock"
	// defaultPluginName is the identity containerd registers this plugin under;
	// it names the socket the runtime creates and is not the binary's name.
	defaultPluginName = "mokka-nri-plugin"
	// defaultPluginIndex orders this plugin against the others the runtime has
	// registered. Later indices adjust a container after earlier ones.
	defaultPluginIndex = "10"
)

// Config is what the plugin needs to register with the runtime, plus the
// injection decision it applies to every container it is asked about.
type Config struct {
	SocketPath  string
	PluginName  string
	PluginIndex string
	Inject      inject.Config
}

// DefaultConfig returns the registration identity used when no flag overrides
// it. The chart sets --plugin-name from nri.pluginName, so only a standalone
// run registers under defaultPluginName.
func DefaultConfig() Config {
	return Config{
		SocketPath:  defaultSocketPath,
		PluginName:  defaultPluginName,
		PluginIndex: defaultPluginIndex,
		Inject:      inject.DefaultConfig(),
	}
}
