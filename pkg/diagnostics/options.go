/*
 * SPDX-FileCopyrightText: Copyright (c) 2025 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
 * SPDX-License-Identifier: Apache-2.0
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package diagnostics

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	nfdclient "sigs.k8s.io/node-feature-discovery/api/generated/clientset/versioned"
)

// Resource-kind names the diagnostic collectors accept via WithObjects.
const (
	// Pods is a core-group resource kind the diagnostic collectors handle.
	Pods       = "pods"
	Nodes      = "nodes"
	Namespaces = "namespaces"

	// Deployments is an apps-group resource kind the diagnostic collectors handle.
	Deployments = "deployments"
	DaemonSets  = "daemonsets"

	// Jobs is a batch-group resource kind the diagnostic collectors handle.
	Jobs = "jobs"

	// NodeFeature is an NFD extension kind the diagnostic collectors handle.
	NodeFeature     = "nodeFeature"
	NodeFeatureRule = "nodeFeatureRule"
)

// Diagnostic is a configured collector set that dumps failure artifacts.
type Diagnostic struct {
	*Config
	collectors []Collector
}

// Option mutates a Diagnostic during New.
type Option func(*Diagnostic)

// WithNamespace scopes collection to the given namespace.
func WithNamespace(namespace string) func(*Diagnostic) {
	return func(d *Diagnostic) {
		d.namespace = namespace
	}
}

// WithArtifactDir sets the on-disk directory dumps are written to.
func WithArtifactDir(artifactDir string) func(*Diagnostic) {
	return func(d *Diagnostic) {
		d.artifactDir = artifactDir
	}
}

// WithKubernetesClient wires in the core Kubernetes clientset the collectors use.
func WithKubernetesClient(clientset kubernetes.Interface) func(*Diagnostic) {
	return func(d *Diagnostic) {
		d.Clientset = clientset
	}
}

// WithNFDClient wires in the NFD clientset for NodeFeature / NodeFeatureRule dumps.
func WithNFDClient(nfdClient *nfdclient.Clientset) func(*Diagnostic) {
	return func(d *Diagnostic) {
		d.NfdClient = nfdClient
	}
}

// WithObjects selects which resource kinds (see the Pods/Nodes/... consts) to dump.
func WithObjects(objects ...string) func(*Diagnostic) {
	return func(d *Diagnostic) {
		seen := make(map[string]bool)
		for _, obj := range objects {
			if seen[obj] {
				continue
			}
			seen[obj] = true
			switch obj {
			case Nodes:
				d.collectors = append(d.collectors, nodes{Config: d.Config})
			case Namespaces:
				d.collectors = append(d.collectors, namespaces{Config: d.Config})
			case Pods:
				d.collectors = append(d.collectors, pods{Config: d.Config})
			case Deployments:
				d.collectors = append(d.collectors, deployments{Config: d.Config})
			case DaemonSets:
				d.collectors = append(d.collectors, daemonsets{Config: d.Config})
			case Jobs:
				d.collectors = append(d.collectors, jobs{Config: d.Config})
			case NodeFeature:
				d.collectors = append(d.collectors, nodeFeatures{Config: d.Config})
			case NodeFeatureRule:
				d.collectors = append(d.collectors, nodeFeatureRules{Config: d.Config})
			default:
				klog.Warningf("Unsupported object %s", obj)
				continue
			}
		}
	}
}

// New builds a Diagnostic from the given options.
func New(opts ...Option) (*Diagnostic, error) {
	c := &Config{}
	dc := &Diagnostic{
		Config: c,
	}

	// use the variadic function to set the options
	for _, opt := range opts {
		opt(dc)
	}

	return dc, nil
}
