// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package assertions

import (
	"encoding/json"
	"errors"
	"fmt"
)

// This file carries no build tag on purpose, like gfd_labels.go: parsing
// `crictl info` needs nothing but the standard library, so its tests run in
// the regular `go test ./...` job rather than only under -tags e2e.

const (
	// NvidiaHandler is the runtime handler the node daemon registers: the name
	// of the GPU Operator's RuntimeClass.
	NvidiaHandler = "nvidia"
	// NvidiaRuntimeBinary is the runtime the handler runs.
	NvidiaRuntimeBinary = "/usr/bin/nvidia-container-runtime"
)

// ContainerRuntime is what a node's running containerd reports about itself
// over CRI: the configuration it loaded, not what its files say now.
type ContainerRuntime struct {
	EnableCDI      bool
	CDISpecDirs    []string
	DefaultRuntime string
	// Handlers maps each runtime handler to the binary it runs; empty for
	// runc's built-in default.
	Handlers map[string]string
}

// ParseCRIInfo parses the output of `crictl info`.
func ParseCRIInfo(out []byte) (ContainerRuntime, error) {
	var info struct {
		Config *struct {
			EnableCDI   bool     `json:"enableCDI"`
			CDISpecDirs []string `json:"cdiSpecDirs"`
			Containerd  struct {
				DefaultRuntimeName string `json:"defaultRuntimeName"`
				Runtimes           map[string]struct {
					Options map[string]any `json:"options"`
				} `json:"runtimes"`
			} `json:"containerd"`
		} `json:"config"`
	}

	if err := json.Unmarshal(out, &info); err != nil {
		return ContainerRuntime{}, fmt.Errorf("parse crictl info: %w", err)
	}

	if info.Config == nil {
		return ContainerRuntime{}, errors.New("crictl info carries no CRI configuration")
	}

	rt := ContainerRuntime{
		EnableCDI:      info.Config.EnableCDI,
		CDISpecDirs:    info.Config.CDISpecDirs,
		DefaultRuntime: info.Config.Containerd.DefaultRuntimeName,
		Handlers:       make(map[string]string, len(info.Config.Containerd.Runtimes)),
	}

	for name, runtime := range info.Config.Containerd.Runtimes {
		binary, _ := runtime.Options["BinaryName"].(string)
		rt.Handlers[name] = binary
	}

	return rt, nil
}

// CheckNvidiaHandler reports what keeps rt from serving the node daemon's
// handler: it must run the NVIDIA runtime, with CDI on, as the default
// handler.
func CheckNvidiaHandler(rt ContainerRuntime) error {
	binary, ok := rt.Handlers[NvidiaHandler]
	if !ok {
		return fmt.Errorf("no %s handler; containerd has %v", NvidiaHandler, rt.Handlers)
	}

	if binary != NvidiaRuntimeBinary {
		return fmt.Errorf("the %s handler runs %q, not %q", NvidiaHandler, binary, NvidiaRuntimeBinary)
	}

	if rt.DefaultRuntime != NvidiaHandler {
		return fmt.Errorf("the default handler is %q, not %s", rt.DefaultRuntime, NvidiaHandler)
	}

	if !rt.EnableCDI {
		return errors.New("containerd has CDI off")
	}

	return nil
}
