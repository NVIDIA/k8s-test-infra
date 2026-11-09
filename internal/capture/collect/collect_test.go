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
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/NVIDIA/k8s-test-infra/internal/nri/inject"
)

// fakeKubectl writes a kubectl that logs each call, one per line, and answers
// as a cluster with one driver pod and one toolkit pod on every node. FAIL
// names an argument that makes a call fail.
const fakeKubectl = `#!/bin/sh
echo "$*" >> "$LOG"
case " $* " in *" $FAIL "*) echo "failed: $*" >&2; exit 1;; esac
case "$*" in
  *"--field-selector"*) printf 'pod/nvidia-driver-daemonset-x\npod/nvidia-container-toolkit-daemonset-y\n';;
  *"app=nvidia-dcgm-exporter"*) echo pod/nvidia-dcgm-exporter-z;;
  "get crd -o name") printf 'customresourcedefinitions/nicclusterpolicies.mellanox.com\ncustomresourcedefinitions/other.example.com\n';;
  *" -i "*) cat >/dev/null; echo "stdin for: $*";;
  *) echo "answer to: $*";;
esac
`

func setup(t *testing.T, fail string) (dir, log, kubectl string) {
	t.Helper()
	dir = t.TempDir()
	log = filepath.Join(dir, "calls.log")
	kubectl = filepath.Join(dir, "kubectl")
	require.NoError(t, os.WriteFile(kubectl, []byte(fakeKubectl), 0o755)) //nolint:gosec // an executable test double
	t.Setenv("LOG", log)
	t.Setenv("FAIL", fail)
	return dir, log, kubectl
}

func calls(t *testing.T, log string) string {
	t.Helper()
	raw, err := os.ReadFile(log)
	require.NoError(t, err)
	return string(raw)
}

func TestAllCapturesNodeAndClusterAndCleansUp(t *testing.T) {
	dir, log, kubectl := setup(t, "")
	out := filepath.Join(dir, "capture")
	bin := filepath.Join(dir, "mokka-capture-linux")
	require.NoError(t, os.WriteFile(bin, []byte("binary"), 0o600))

	code := run(context.Background(), []string{"-out", out, "-dump-binary", bin, "all", "node-a"}, io.Discard, io.Discard, kubectl)
	require.Equal(t, 0, code)

	for _, f := range []string{
		"node-a/smi-q.xml", "node-a/smi-q-d-FABRIC.txt", "node-a/nvml-dump.json", "node-a/host-ibstat.txt",
		"node-a/rdma-sysfs.txt", "node-a/cdi-toolkit-list.txt", "cluster/nodes.yaml",
		"cluster/dcgm-nvidia-dcgm-exporter-z.txt", "cluster/versions.txt",
	} {
		require.FileExists(t, filepath.Join(out, f))
	}
	xml, err := os.ReadFile(filepath.Join(out, "node-a", "smi-q.xml"))
	require.NoError(t, err)
	require.Equal(t, "answer to: -n gpu-operator exec nvidia-driver-daemonset-x -c nvidia-driver-ctr -- nvidia-smi -q -x\n", string(xml))
	crds, err := os.ReadFile(filepath.Join(out, "cluster", "crds-network.txt"))
	require.NoError(t, err)
	require.Equal(t, "customresourcedefinitions/nicclusterpolicies.mellanox.com\n", string(crds))

	got := calls(t, log)
	pod := hostPodName("node-a", fmtPID())
	require.Contains(t, got, "-n default delete pod "+pod+" --wait=false")
	require.Contains(t, got, "/tmp/mokka-capture-"+fmtPID()+" dump")
	require.Contains(t, got, "rm -f /tmp/mokka-capture-"+fmtPID())
}

// A host pod that never becomes ready is still deleted.
func TestHostPodIsDeletedWhenItFailsToStart(t *testing.T) {
	dir, log, kubectl := setup(t, "--for=condition=Ready")
	code := run(context.Background(), []string{"-out", filepath.Join(dir, "capture"), "host", "node-a"}, io.Discard, io.Discard, kubectl)
	require.Equal(t, 0, code)
	got := calls(t, log)
	require.Contains(t, got, "delete pod "+hostPodName("node-a", fmtPID()))
	require.NotContains(t, got, "ibstat")
}

// A failed command is recorded beside its output and does not stop the run.
func TestFailedCommandIsRecordedInErrorsLog(t *testing.T) {
	dir, _, kubectl := setup(t, "-q")
	out := filepath.Join(dir, "capture")
	require.Equal(t, 0, run(context.Background(), []string{"-out", out, "driver", "node-a"}, io.Discard, io.Discard, kubectl))
	errs, err := os.ReadFile(filepath.Join(out, "node-a", "errors.log"))
	require.NoError(t, err)
	require.Contains(t, string(errs), "FAILED: smi-q.xml")
	require.FileExists(t, filepath.Join(out, "node-a", "smi-L.txt"))
}

func TestUnreachableClusterStopsTheRun(t *testing.T) {
	dir, _, kubectl := setup(t, "nodes")
	require.Equal(t, 1, run(context.Background(), []string{"-out", filepath.Join(dir, "c"), "cluster"}, io.Discard, io.Discard, kubectl))
}

func TestNodeCommandsNeedANode(t *testing.T) {
	_, _, kubectl := setup(t, "")
	require.Equal(t, 2, run(context.Background(), []string{"driver"}, io.Discard, io.Discard, kubectl))
}

func TestHostPodNameFitsAPodName(t *testing.T) {
	name := hostPodName(strings.Repeat("n", 80), "123456")
	require.Len(t, name, 63)
	require.True(t, strings.HasPrefix(name, "capture-nnn"))
	require.True(t, strings.HasSuffix(name, "-123456"))
	require.Equal(t, "capture-node-a-1", hostPodName("node-a", "1"))
}

func fmtPID() string { return strconv.Itoa(os.Getpid()) }

// On a Mokka node the driver commands run in a stand-in pod that collect
// creates and deletes; no driver pod is looked up.
func TestMockRunsTheDriverCommandsInAStandIn(t *testing.T) {
	dir, log, kubectl := setup(t, "")
	out := filepath.Join(dir, "capture")
	require.Equal(t, 0, run(context.Background(), []string{"-out", out, "-mock", "driver", "node-a"}, io.Discard, io.Discard, kubectl))

	pod := standInName("node-a", fmtPID())
	xml, err := os.ReadFile(filepath.Join(out, "node-a", "smi-q.xml"))
	require.NoError(t, err)
	require.Equal(t, "answer to: -n default exec "+pod+" -c c -- nvidia-smi -q -x\n", string(xml))
	got := calls(t, log)
	require.Contains(t, got, "-n default wait --for=condition=Ready pod/"+pod)
	require.Contains(t, got, "-n default delete pod "+pod+" --wait=false")
	require.NotContains(t, got, "--field-selector")
	require.NotContains(t, got, "apt-get")
}

// -mock opts both pods into the mock with the NRI plugin's own annotation
// keys; without it the pods carry none.
func TestMockAnnotatesThePods(t *testing.T) {
	annotations := func(mock bool) map[string]string {
		c := &collector{cfg: config{mock: mock, hostPodImage: "image"}}
		var pod struct {
			Metadata struct {
				Name        string
				Annotations map[string]string
			}
		}
		require.NoError(t, yaml.Unmarshal([]byte(c.podManifest("p", "node-a")), &pod))
		require.Equal(t, "p", pod.Metadata.Name)
		return pod.Metadata.Annotations
	}
	require.Empty(t, annotations(false))
	nri := inject.DefaultConfig()
	require.Equal(t, map[string]string{
		nri.DeviceAnnotation:      "true",
		nri.InfiniBandAnnotation:  "true",
		nri.ImexChannelAnnotation: "true",
	}, annotations(true))
}

// Two matching pods, listed in reverse order, as during a driver rollout: the
// choice must not depend on the order kubectl lists them in.
func TestPodOnPicksBySortedName(t *testing.T) {
	dir := t.TempDir()
	kubectl := filepath.Join(dir, "kubectl")
	script := "#!/bin/sh\nprintf 'pod/nvidia-driver-daemonset-b\\npod/other\\npod/nvidia-driver-daemonset-a\\n'\n"
	require.NoError(t, os.WriteFile(kubectl, []byte(script), 0o755)) //nolint:gosec // an executable test double
	var log strings.Builder
	c := &collector{cfg: config{operatorNS: "gpu-operator"}, ctx: context.Background(), stderr: &log, kubectl: kubectl}
	require.Equal(t, "nvidia-driver-daemonset-a", c.podOn("node-a", "nvidia-driver-daemonset"))
	require.Contains(t, log.String(), "2 pods matching")
	require.Empty(t, c.podOn("node-a", "container-toolkit"))
}
