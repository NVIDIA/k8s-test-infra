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

package collect

import (
	"errors"
	"strings"
)

// podTarget is a pod the cluster already runs on each node: the first pod in
// namespace whose name contains part, and its container (the default if "").
type podTarget struct{ namespace, part, container string }

// parseExecIn reads -exec-in, namespace/name-part[/container]. An empty value
// means no target: the host captures start a pod of their own.
func parseExecIn(v string) (*podTarget, error) {
	if v == "" {
		return nil, nil
	}
	f := strings.Split(v, "/")
	if len(f) < 2 || len(f) > 3 || f[0] == "" || f[1] == "" {
		return nil, errors.New("want namespace/name-part[/container], got " + v)
	}
	t := &podTarget{namespace: f[0], part: f[1]}
	if len(f) == 3 {
		t.container = f[2]
	}
	return t, nil
}

// borrowPod points the host commands at the target pod on node instead of a
// pod of the run's own. It creates, installs and deletes nothing, so it suits
// a cluster where a new privileged pod is not welcome; the pod should be
// privileged and on the host network to see the node's /sys and interfaces.
// Commands whose tools the pod lacks fail into errors.log.
func (c *collector) borrowPod(node string) bool {
	pod := c.podIn(c.execIn.namespace, node, c.execIn.part)
	if pod == "" {
		c.logf("no pod matching %q in namespace %s on %s; skipping", c.execIn.part, c.execIn.namespace, node)
		return false
	}
	c.logf("== %s: running the host commands in %s/%s", node, c.execIn.namespace, pod)
	c.hostExec, c.hostNS, c.hostCtr = pod, c.execIn.namespace, c.execIn.container
	return true
}
