// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Package featuregate controls the rollout of incomplete or transitional
// behavior without coupling that behavior to its configuration source.
package featuregate

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Stage describes the lifecycle and default state of a feature gate.
type Stage string

const (
	// StageAlpha gates behavior that is disabled by default.
	StageAlpha Stage = "alpha"
	// StageBeta gates behavior that is enabled by default but may be disabled.
	StageBeta Stage = "beta"
	// StageStable marks enabled behavior whose gate is awaiting removal.
	StageStable Stage = "stable"
	// StageDeprecated marks disabled behavior whose gate and implementation are awaiting removal.
	StageDeprecated Stage = "deprecated"
)

// Definition records the information needed to operate and eventually remove
// a feature gate.
type Definition struct {
	ID           string
	Description  string
	Stage        Stage
	FromVersion  string
	ToVersion    string
	ReferenceURL string
}

// Gate is a registered feature gate. Call IsEnabled at the boundary where the
// gated behavior is selected, rather than spreading configuration checks
// throughout the implementation.
type Gate struct {
	definition Definition
	enabled    atomic.Bool
}

// Definition returns the immutable metadata supplied at registration.
func (g *Gate) Definition() Definition { return g.definition }

// IsEnabled reports the process-wide state of the gate.
func (g *Gate) IsEnabled() bool { return g.enabled.Load() }

// Registry owns a set of feature gates. A Registry is safe for concurrent
// reads and configuration, although applications should configure it once at
// startup before launching workers.
type Registry struct {
	mu    sync.RWMutex
	gates map[string]*Gate
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9]+(?:\.[A-Za-z0-9]+)*$`)

// NewRegistry returns an empty feature gate registry.
func NewRegistry() *Registry {
	return &Registry{gates: make(map[string]*Gate)}
}

// Register adds a feature gate and returns the handle used by implementation
// code to query it.
func (r *Registry) Register(def Definition) (*Gate, error) {
	if err := validateDefinition(def); err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.gates[def.ID]; exists {
		return nil, fmt.Errorf("feature gate %q is already registered", def.ID)
	}

	gate := &Gate{definition: def}
	gate.enabled.Store(def.Stage == StageBeta || def.Stage == StageStable)
	r.gates[def.ID] = gate
	return gate, nil
}

// MustRegister is Register with panic-on-error semantics for package-level gate
// declarations, where invalid metadata is a programmer error.
func (r *Registry) MustRegister(def Definition) *Gate {
	gate, err := r.Register(def)
	if err != nil {
		panic(err)
	}
	return gate
}

// Configure applies a comma-separated list of gate identifiers. An identifier
// enables a gate; a leading '-' disables it and an optional '+' makes enabling
// explicit. The entire value is validated before any state changes.
func (r *Registry) Configure(value string) error {
	settings, err := r.parse(value)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, gate := range r.gates {
		gate.enabled.Store(gate.definition.Stage == StageBeta || gate.definition.Stage == StageStable)
	}
	for _, setting := range settings {
		r.gates[setting.id].enabled.Store(setting.enabled)
	}
	return nil
}

type setting struct {
	id      string
	enabled bool
}

func (r *Registry) parse(value string) ([]setting, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	settings := make([]setting, 0, strings.Count(value, ",")+1)
	for _, raw := range strings.Split(value, ",") {
		parsed, err := r.parseSetting(raw)
		if err != nil {
			return nil, err
		}
		settings = append(settings, parsed)
	}
	return settings, nil
}

func (r *Registry) parseSetting(raw string) (setting, error) {
	token := strings.TrimSpace(raw)
	if token == "" {
		return setting{}, errors.New("feature gate list contains an empty entry")
	}

	enabled := true
	if token[0] == '+' || token[0] == '-' {
		enabled = token[0] != '-'
		token = token[1:]
	}
	if token == "" {
		return setting{}, fmt.Errorf("invalid feature gate %q", raw)
	}

	gate, ok := r.gates[token]
	if !ok {
		return setting{}, fmt.Errorf("unknown feature gate %q (valid gates: %s)", token, strings.Join(r.idsLocked(), ", "))
	}
	if err := validateOverride(gate, enabled); err != nil {
		return setting{}, err
	}
	return setting{id: token, enabled: enabled}, nil
}

func validateOverride(gate *Gate, enabled bool) error {
	switch gate.definition.Stage {
	case StageAlpha, StageBeta:
		return nil
	case StageStable:
		if !enabled {
			return fmt.Errorf("stable feature gate %q cannot be disabled", gate.definition.ID)
		}
	case StageDeprecated:
		if enabled {
			return fmt.Errorf("deprecated feature gate %q cannot be enabled", gate.definition.ID)
		}
	}
	return nil
}

// Definitions returns all registered gate metadata ordered by identifier.
func (r *Registry) Definitions() []Definition {
	r.mu.RLock()
	defer r.mu.RUnlock()
	definitions := make([]Definition, 0, len(r.gates))
	for _, gate := range r.gates {
		definitions = append(definitions, gate.definition)
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].ID < definitions[j].ID })
	return definitions
}

func (r *Registry) idsLocked() []string {
	ids := make([]string, 0, len(r.gates))
	for id := range r.gates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func validateDefinition(def Definition) error {
	if !idPattern.MatchString(def.ID) {
		return fmt.Errorf("invalid feature gate ID %q: use dot-separated ASCII letters and numbers", def.ID)
	}
	if strings.TrimSpace(def.Description) == "" {
		return fmt.Errorf("feature gate %q has no description", def.ID)
	}
	if strings.TrimSpace(def.FromVersion) == "" {
		return fmt.Errorf("feature gate %q has no from version", def.ID)
	}
	if def.ReferenceURL == "" {
		return fmt.Errorf("feature gate %q has no reference URL", def.ID)
	}
	if !validReferenceURL(def.ReferenceURL) {
		return fmt.Errorf("feature gate %q has invalid reference URL %q", def.ID, def.ReferenceURL)
	}
	return validateStage(def)
}

func validateStage(def Definition) error {
	switch def.Stage {
	case StageAlpha, StageBeta:
		if def.ToVersion != "" {
			return fmt.Errorf("%s feature gate %q must not have a removal version", def.Stage, def.ID)
		}
	case StageStable, StageDeprecated:
		if strings.TrimSpace(def.ToVersion) == "" {
			return fmt.Errorf("%s feature gate %q has no removal version", def.Stage, def.ID)
		}
	default:
		return fmt.Errorf("feature gate %q has invalid stage %q", def.ID, def.Stage)
	}
	return nil
}

func validReferenceURL(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

var global = NewRegistry()

// GlobalRegistry returns the registry used by Mokka binaries.
func GlobalRegistry() *Registry { return global }

// MustRegister adds a gate to the process-wide registry.
func MustRegister(def Definition) *Gate { return global.MustRegister(def) }
