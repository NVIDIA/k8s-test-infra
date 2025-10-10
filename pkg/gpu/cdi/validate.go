// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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

package cdi

import (
	"fmt"

	specgo "tags.cncf.io/container-device-interface/specs-go"

	"sigs.k8s.io/yaml"
)

// Validate performs basic validation on a CDI specification YAML.
// It checks that the YAML can be unmarshaled and contains required fields.
func Validate(b []byte) error {
	var spec specgo.Spec
	if err := yaml.Unmarshal(b, &spec); err != nil {
		return fmt.Errorf("invalid YAML: %w", err)
	}

	// Basic validation
	if spec.Version == "" {
		return fmt.Errorf("missing CDI version")
	}
	if spec.Kind == "" {
		return fmt.Errorf("missing CDI kind")
	}
	if len(spec.Devices) == 0 {
		return fmt.Errorf("no devices defined")
	}

	return nil
}
