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
	"github.com/urfave/cli/v3"
	"go.yaml.in/yaml/v3"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/cmdline"
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

// runCLI runs `mokka-capture collect` with args against kubectl and returns
// its exit code.
func runCLI(kubectl string, args ...string) int {
	root := &cli.Command{Name: "mokka-capture", Commands: []*cli.Command{newCommand(kubectl)}}
	return cmdline.Run(context.Background(), root, append([]string{"collect"}, args...), io.Discard, io.Discard)
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

	code := runCLI(kubectl, "-out", out, "-dump-binary", bin, "all", "node-a")
	require.Equal(t, 0, code)

	for _, f := range []string{
		"node-a/smi-q.xml", "node-a/smi-q-d-FABRIC.txt", "node-a/nvml-dump.json", "node-a/host-ibstat.txt",
		"node-a/rdma-sysfs.txt", "node-a/pci-vpd-fields.txt", "node-a/fw-flint.txt", "node-a/cdi-toolkit-list.txt", "cluster/nodes.yaml",
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
	code := runCLI(kubectl, "-out", filepath.Join(dir, "capture"), "host", "node-a")
	require.Equal(t, 0, code)
	got := calls(t, log)
	require.Contains(t, got, "delete pod "+hostPodName("node-a", fmtPID()))
	require.NotContains(t, got, "ibstat")
}

// A failed command is recorded beside its output and does not stop the run.
func TestFailedCommandIsRecordedInErrorsLog(t *testing.T) {
	dir, _, kubectl := setup(t, "-q")
	out := filepath.Join(dir, "capture")
	require.Equal(t, 0, runCLI(kubectl, "-out", out, "driver", "node-a"))
	errs, err := os.ReadFile(filepath.Join(out, "node-a", "errors.log"))
	require.NoError(t, err)
	require.Contains(t, string(errs), "FAILED: smi-q.xml")
	require.FileExists(t, filepath.Join(out, "node-a", "smi-L.txt"))
}

// collect's flags apply to its commands wherever they are written.
func TestFlagsMayFollowTheCommand(t *testing.T) {
	dir, _, kubectl := setup(t, "")
	out := filepath.Join(dir, "capture")
	require.Equal(t, 0, runCLI(kubectl, "driver", "node-a", "-out", out, "--operator-namespace", "ops"))
	xml, err := os.ReadFile(filepath.Join(out, "node-a", "smi-q.xml"))
	require.NoError(t, err)
	require.Equal(t, "answer to: -n ops exec nvidia-driver-daemonset-x -c nvidia-driver-ctr -- nvidia-smi -q -x\n", string(xml))
}

func TestUnreachableClusterStopsTheRun(t *testing.T) {
	dir, _, kubectl := setup(t, "nodes")
	require.Equal(t, 1, runCLI(kubectl, "-out", filepath.Join(dir, "c"), "cluster"))
}

func TestNodeCommandsNeedANode(t *testing.T) {
	_, _, kubectl := setup(t, "")
	require.Equal(t, 2, runCLI(kubectl, "driver"))
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
	require.Equal(t, 0, runCLI(kubectl, "-out", out, "-mock", "driver", "node-a"))

	pod := standInName("node-a", fmtPID())
	xml, err := os.ReadFile(filepath.Join(out, "node-a", "smi-q.xml"))
	require.NoError(t, err)
	require.Equal(t, "answer to: -n default exec "+pod+" -c c -- nvidia-smi -q -x\n", string(xml))
	got := calls(t, log)
	require.Contains(t, got, "-n default wait --for=condition=Ready pod/"+pod)
	require.Contains(t, got, "-n default delete pod "+pod+" --wait=false")
	require.NotContains(t, got, "--field-selector")
	require.NotContains(t, got, "apt-get")

	// The mock stages the MIG table under its driver root, not in /proc.
	minors, err := os.ReadFile(filepath.Join(out, "node-a", "proc-mig-minors.txt"))
	require.NoError(t, err)
	require.Equal(t, "answer to: -n default exec "+pod+` -c c -- sh -c cat "${MOCK_NVML_CONFIG%/config/config.yaml}/proc/driver/nvidia-caps/mig-minors"`+"\n", string(minors))
	version, err := os.ReadFile(filepath.Join(out, "node-a", "proc-nvidia-version.txt"))
	require.NoError(t, err)
	require.Equal(t, "answer to: -n default exec "+pod+" -c c -- cat /proc/driver/nvidia/version\n", string(version))
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

func TestExecInTakesHostCapturesInAnExistingPod(t *testing.T) {
	dir, log, kubectl := setup(t, "")
	out := filepath.Join(dir, "capture")
	require.Equal(t, 0, runCLI(kubectl, "-out", out, "-exec-in", "net-op/driver-daemonset/ctr", "network", "node-a"))

	got := calls(t, log)
	require.NotContains(t, got, "apply -f -", "-exec-in must not create a pod")
	require.NotContains(t, got, "apt-get", "-exec-in must not install anything")
	require.NotContains(t, got, "delete pod", "-exec-in must not delete the pod it borrowed")
	require.Contains(t, got, "-n net-op get pods -o name --field-selector spec.nodeName=node-a")
	require.Contains(t, got, "-n net-op exec nvidia-driver-daemonset-x -c ctr -- ip -d link show")
	require.Contains(t, got, "-n net-op exec -i nvidia-driver-daemonset-x -c ctr -- sh -s")
}

func TestExecInSkipsANodeWithoutTheNamedPod(t *testing.T) {
	dir, log, kubectl := setup(t, "")
	out := filepath.Join(dir, "capture")
	require.Equal(t, 0, runCLI(kubectl, "-out", out, "-exec-in", "net-op/no-such-pod", "host", "node-a"))
	require.NotContains(t, calls(t, log), " exec ")
}

func TestExecInNeedsANamespaceAndAPodName(t *testing.T) {
	for _, v := range []string{"pod-only", "/part", "ns/", "a/b/c/d"} {
		_, err := parseExecIn(v)
		require.Error(t, err, v)
	}
	target, err := parseExecIn("ns/part")
	require.NoError(t, err)
	require.Equal(t, &podTarget{namespace: "ns", part: "part"}, target)
	dir, _, kubectl := setup(t, "")
	require.Equal(t, 2, runCLI(kubectl, "-out", filepath.Join(dir, "c"), "-exec-in", "bad", "host", "node-a"))
}

// A dropped connection is retried, with the same standard input; an answer
// from the node, even an error, is not.
func TestACommandWhoseConnectionDroppedIsRunAgain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	kubectl := filepath.Join(dir, "kubectl")
	script := `#!/bin/sh
echo "$*" >> "$0.log"
n=$(grep -c . "$0.log")
case "$*" in
  *uname*) [ "$n" -ge 2 ] || { echo "Unable to connect to the server: net/http: TLS handshake timeout" >&2; exit 1; }; echo "Linux node";;
  *" -i "*) [ "$n" -ge 4 ] || { echo "error dialing backend: EOF" >&2; exit 1; }; cat;;
  *ibstat*) echo "Error from server: boom" >&2; exit 1;;
esac
`
	require.NoError(t, os.WriteFile(kubectl, []byte(script), 0o755)) //nolint:gosec // an executable test double
	c := &collector{ctx: context.Background(), stderr: io.Discard, kubectl: kubectl, dir: dir}

	c.capture("host-uname.txt", nil, "kubectl", "exec", "--", "uname", "-a")
	c.capture("rdma-sysfs.txt", strings.NewReader("echo script\n"), "kubectl", "exec", "-i", "--", "sh", "-s")
	c.capture("host-ibstat.txt", nil, "kubectl", "exec", "--", "ibstat")

	for file, want := range map[string]string{"host-uname.txt": "Linux node\n", "rdma-sysfs.txt": "echo script\n", "host-ibstat.txt": ""} {
		got, err := os.ReadFile(filepath.Join(dir, file))
		require.NoError(t, err)
		require.Equal(t, want, string(got), file)
	}
	errs, err := os.ReadFile(filepath.Join(dir, "errors.log"))
	require.NoError(t, err)
	require.Contains(t, string(errs), "--- host-uname.txt, try 1\nUnable to connect to the server: net/http: TLS handshake timeout\n")
	require.NotContains(t, string(errs), "host-uname.txt, try 2", "a try that succeeded quietly leaves no heading")
	require.Contains(t, string(errs), "--- host-ibstat.txt, try 1\nError from server: boom\nFAILED: host-ibstat.txt\n")
	require.NotContains(t, string(errs), "FAILED: host-uname.txt")
	require.NotContains(t, string(errs), "FAILED: rdma-sysfs.txt")
	require.Contains(t, string(errs), "FAILED: host-ibstat.txt")
	calls, err := os.ReadFile(kubectl + ".log")
	require.NoError(t, err)
	require.Equal(t, 5, strings.Count(string(calls), "\n"), "two tries each for the dropped connections, one for the error")
}

// network reads the NodeFeatures of the nodes it captures, not every node's;
// cluster reads them all.
func TestNetworkTakesOnlyItsNodesNodeFeatures(t *testing.T) {
	dir, log, kubectl := setup(t, "")
	require.Equal(t, 0, runCLI(kubectl, "-out", filepath.Join(dir, "a"), "-exec-in", "ns/pod", "network", "node-a", "node-b"))
	require.Contains(t, calls(t, log), "get nodefeatures -A -o yaml -l nfd.node.kubernetes.io/node-name in (node-a,node-b)\n")
	require.NoError(t, os.Remove(log))
	require.Equal(t, 0, runCLI(kubectl, "-out", filepath.Join(dir, "b"), "cluster"))
	require.Contains(t, calls(t, log), "get nodefeatures -A -o yaml\n")
}
