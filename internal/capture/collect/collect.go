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

// Package collect captures everything about a real GPU node that Mokka has to
// reproduce: NVML and nvidia-smi, the driver's /proc files, PCI and device
// files, NUMA, InfiniBand and RoCE NICs, CDI specs, and the Kubernetes view of
// the node. It backs `mokka-capture collect` and runs on the developer's
// machine, reaching the nodes through kubectl.
//
// Every command runs in the cluster; every output is written here, so each is
// a file under the output directory and nothing has to be copied out of a pod.
// A command that fails does not stop the run: its error is appended to
// errors.log beside the output, and a failure is data (an unsupported query is
// an answer too).
package collect

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/cmdline"
)

// The host pod is privileged and mounts the node's root, so its image is
// pinned by digest (the multi-arch index of ubuntu:24.04) rather than by a tag
// that can move.
const defaultHostPodImage = "ubuntu:24.04@sha256:fe81560990fae86dc4743202b2cb4d3df98d0bcf949173cecef15ce1127ef852"

type config struct {
	out, operatorNS, driverCtr, toolkitCtr, toolkitCTK string
	hostPodNS, hostPodImage, dumpBinary                string
	mock                                               bool
}

type collector struct {
	cfg    config
	ctx    context.Context
	stderr io.Writer
	// kubectl is the command run for every cluster call; tests replace it.
	kubectl string
	// suffix makes the names this run creates (host pod, binary in the driver
	// container) its own, so two runs against one node never collide.
	suffix string

	dir string // the node or cluster directory being written
	// driverPod, driverNS and driverCtr name the container the driver commands
	// run in: the GPU Operator's driver container, or with -mock the stand-in.
	driverPod, driverNS, driverCtr string
	hostPod                        string
	standIn                        string // the -mock stand-in pod, deleted like the host pod
}

// Command is `mokka-capture collect`. Its flags apply to every command under
// it, written before or after the command's name.
func Command() *cli.Command { return newCommand("kubectl") }

// newCommand is collect running kubectl for every cluster call; tests pass a
// stand-in.
func newCommand(kubectl string) *cli.Command {
	var cfg config
	action := func(ctx context.Context, cmd *cli.Command) error {
		// An interrupt cancels the run instead of ending the process, so the
		// pods it started are deleted.
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		c := &collector{cfg: cfg, ctx: ctx, stderr: cmd.Root().ErrWriter, kubectl: kubectl, suffix: strconv.Itoa(os.Getpid())}
		return cmdline.Exit(c.run(cmd.Name, cmd.StringArgs("node"), cmd.Root().Writer))
	}
	onNodes := func(name, usage string) *cli.Command {
		return &cli.Command{
			Name:      name,
			Usage:     usage,
			Arguments: []cli.Argument{&cli.StringArgs{Name: "node", UsageText: "node [node ...]", Max: -1}},
			Action:    action,
		}
	}
	return &cli.Command{
		Name:  "collect",
		Usage: "capture nodes and the cluster through kubectl",
		Description: "kubectl reaches the cluster as it is configured (KUBECONFIG, current context). " +
			"On a Mokka node, pass -mock: Mokka runs no driver container, so the driver commands run " +
			"in a temporary stand-in pod, and both pods opt into the mock.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "out",
				Value:       "./capture",
				Usage:       "output directory",
				Destination: &cfg.out,
			},
			&cli.StringFlag{
				Name:        "operator-namespace",
				Value:       "gpu-operator",
				Usage:       "GPU Operator namespace",
				Destination: &cfg.operatorNS,
			},
			&cli.StringFlag{
				Name:        "driver-container",
				Value:       "nvidia-driver-ctr",
				Usage:       "driver container name",
				Destination: &cfg.driverCtr,
			},
			&cli.StringFlag{
				Name:        "toolkit-container",
				Value:       "nvidia-container-toolkit-ctr",
				Usage:       "toolkit container name",
				Destination: &cfg.toolkitCtr,
			},
			&cli.StringFlag{
				Name:        "toolkit-ctk",
				Value:       "/usr/local/nvidia/toolkit/nvidia-ctk",
				Usage:       "path of nvidia-ctk in the toolkit container",
				Destination: &cfg.toolkitCTK,
			},
			&cli.StringFlag{
				Name:        "host-pod-namespace",
				Value:       "default",
				Usage:       "namespace for the temporary host pod",
				Destination: &cfg.hostPodNS,
			},
			&cli.StringFlag{
				Name:        "host-pod-image",
				Value:       defaultHostPodImage,
				Usage:       "image for the host pod; it is privileged, so pin it by digest",
				Destination: &cfg.hostPodImage,
			},
			&cli.StringFlag{
				Name:        "dump-binary",
				Usage:       "Linux mokka-capture to run dump with in the driver container (see build-dump); without it the NVML value dump is skipped",
				Destination: &cfg.dumpBinary,
			},
			&cli.BoolFlag{
				Name:        "mock",
				Usage:       "the nodes are Mokka nodes: run the driver commands in a temporary stand-in pod, and opt it and the host pod into the mock with the nvml-mock NRI annotations",
				Destination: &cfg.mock,
			},
		},
		Commands: []*cli.Command{
			onNodes("driver", "nvidia-smi, NVML dump and /proc/driver/nvidia, taken inside the GPU Operator driver container on each node"),
			onNodes("host", "PCI, NUMA, device files, NICs and CDI specs, taken from a temporary privileged pod on each node"),
			onNodes("network", "NICs, network interfaces, RDMA devices and ports, SR-IOV, kernel modules and kernel log, "+
				"plus the cluster's network objects; a subset of host, for adding network surfaces to an existing capture"),
			{Name: "cluster", Usage: "node labels, ResourceSlices, ClusterPolicy, DCGM metrics", Action: action},
			onNodes("all", "driver, host, then cluster"),
			{
				Name:   "build-dump",
				Usage:  "build a Linux mokka-capture for the NVML dump, for this machine's architecture (needs Docker and the repository)",
				Action: action,
			},
		},
		Action: cmdline.UnknownCommand,
	}
}

// run runs the collect command named command on nodes.
//
//nolint:cyclop // one case per command, in the order the help lists them
func (c *collector) run(command string, nodes []string, stdout io.Writer) int {
	// An interrupted run must not leave a privileged pod on the node.
	defer func() {
		c.stopPod(c.ctx, &c.hostPod)
		c.stopPod(c.ctx, &c.standIn)
	}()

	needsNodes := map[string]bool{"driver": true, "host": true, "network": true, "all": true}
	if needsNodes[command] && len(nodes) == 0 {
		c.logf("error: %s needs at least one node", command)
		return 2
	}
	switch command {
	case "driver", "host", "network", "all":
		for _, node := range nodes {
			if err := c.requireCluster(); err != nil {
				c.logf("error: %v", err)
				return 1
			}
			switch command {
			case "driver":
				c.captureDriver(node)
			case "host":
				c.captureHost(node)
			case "network":
				c.captureNetwork(node)
			case "all":
				c.captureDriver(node)
				c.captureHost(node)
			}
			if c.ctx.Err() != nil {
				return 130
			}
		}
		if command == "network" || command == "all" {
			if err := c.requireCluster(); err != nil {
				c.logf("error: %v", err)
				return 1
			}
			if command == "all" {
				c.captureCluster()
			}
			c.captureClusterNetwork()
		}
	case "cluster":
		if err := c.requireCluster(); err != nil {
			c.logf("error: %v", err)
			return 1
		}
		c.captureCluster()
		c.captureClusterNetwork()
	case "build-dump":
		return c.buildDump(stdout)
	}
	if c.ctx.Err() != nil {
		return 130
	}
	c.report()
	return 0
}

func (c *collector) logf(format string, a ...any) {
	_, _ = fmt.Fprintf(c.stderr, format+"\n", a...)
}

// errorsLog appends to errors.log in the current directory.
func (c *collector) errorsLog() io.WriteCloser {
	f, err := os.OpenFile(filepath.Join(c.dir, "errors.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nopCloser{io.Discard}
	}
	return f
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// capture runs argv and writes its standard output to file in the current
// directory. A failure is noted in errors.log and does not stop the run.
func (c *collector) capture(file string, stdin io.Reader, argv ...string) {
	if c.ctx.Err() != nil {
		return
	}
	out, err := os.Create(filepath.Join(c.dir, file))
	if err != nil {
		c.logf("FAILED: %s: %v", file, err)
		return
	}
	defer func() { _ = out.Close() }()
	errs := c.errorsLog()
	defer func() { _ = errs.Close() }()
	if err := c.exec(stdin, out, errs, argv...); err != nil {
		_, _ = fmt.Fprintf(errs, "FAILED: %s\n", file)
		c.logf("FAILED: %s", file)
	}
}

// captureText writes text, or notes the error, as capture does.
func (c *collector) captureText(file, text string, err error) {
	if err != nil {
		errs := c.errorsLog()
		_, _ = fmt.Fprintf(errs, "%v\nFAILED: %s\n", err, file)
		_ = errs.Close()
		c.logf("FAILED: %s", file)
	}
	if werr := os.WriteFile(filepath.Join(c.dir, file), []byte(text), 0o644); werr != nil {
		c.logf("FAILED: %s: %v", file, werr)
	}
}

// exec runs argv, with the first element "kubectl" meaning the configured
// kubectl.
func (c *collector) exec(stdin io.Reader, stdout, stderr io.Writer, argv ...string) error {
	name := argv[0]
	if name == "kubectl" {
		name = c.kubectl
	}
	cmd := exec.CommandContext(c.ctx, name, argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}

// kubectlOut runs kubectl and returns its standard output.
func (c *collector) kubectlOut(args ...string) (string, error) {
	var out, errb bytes.Buffer
	if err := c.exec(nil, &out, &errb, append([]string{"kubectl"}, args...)...); err != nil {
		return out.String(), fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// drv returns the argv that runs a command in the node's driver container.
func (c *collector) drv(argv ...string) []string {
	return append([]string{"kubectl", "-n", c.driverNS, "exec", c.driverPod, "-c", c.driverCtr, "--"}, argv...)
}

// host returns the argv that runs a command in the node's host pod.
func (c *collector) host(argv ...string) []string {
	return append([]string{"kubectl", "-n", c.cfg.hostPodNS, "exec", c.hostPod, "--"}, argv...)
}

func sh(script string) []string { return []string{"sh", "-c", script} }

// requireCluster fails when the API server is unreachable: a dropped
// connection yields empty files that look like answers, so it is checked
// before each node.
func (c *collector) requireCluster() error {
	if _, err := c.kubectlOut("get", "nodes"); err != nil {
		return fmt.Errorf("cannot reach the cluster; check KUBECONFIG and any tunnel: %w", err)
	}
	return nil
}

func (c *collector) enter(dir string) {
	c.dir = filepath.Join(c.cfg.out, dir)
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		c.logf("error: %v", err)
	}
}

// podOn returns the first pod in the operator namespace on node whose name
// contains part.
func (c *collector) podOn(node, part string) string {
	out, err := c.kubectlOut("-n", c.cfg.operatorNS, "get", "pods", "-o", "name", "--field-selector", "spec.nodeName="+node)
	if err != nil {
		c.logf("%v", err)
		return ""
	}
	// kubectl lists pods in no guaranteed order, and a rollout can leave two
	// matching pods on one node, so the choice is made by sorted name.
	var matches []string
	for _, name := range strings.Fields(out) {
		if strings.Contains(name, part) {
			matches = append(matches, strings.TrimPrefix(name, "pod/"))
		}
	}
	if len(matches) == 0 {
		return ""
	}
	slices.Sort(matches)
	if len(matches) > 1 {
		c.logf("%d pods matching %q on %s (%s); using %s", len(matches), part, node, strings.Join(matches, ", "), matches[0])
	}
	return matches[0]
}

// report lists empty files, which usually mean a dropped connection rather
// than an empty answer.
func (c *collector) report() {
	c.logf("\nCaptured under %s", c.cfg.out)
	var empty []string
	_ = filepath.WalkDir(c.cfg.out, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == "errors.log" {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Size() == 0 {
			empty = append(empty, path)
		}
		return nil
	})
	if len(empty) > 0 {
		c.logf("Empty files (rerun the command that produced each):\n%s", strings.Join(empty, "\n"))
	}
	c.logf("Commands that failed are listed in errors.log beside each output.")
	c.logf("The raw capture holds serial numbers, UUIDs and addresses; scrub it before sharing.")
}

func hostPodName(node, suffix string) string { return podName("capture-", node, suffix) }
func standInName(node, suffix string) string { return podName("capture-driver-", node, suffix) }

// podName is <prefix><node>-<suffix>, the node name cut so the pod name stays
// within 63 characters.
func podName(prefix, node, suffix string) string {
	keep := 63 - len(prefix) - len(suffix) - 1
	if len(node) > keep {
		node = node[:keep]
	}
	return prefix + node + "-" + suffix
}

var crdNetworkRE = regexp.MustCompile(`(?i)mellanox|nvidia|sriov|nfd|k8s\.cni|dranet|topograph`)

// cleanupTimeout bounds the host pod deletion that runs after an interrupt.
const cleanupTimeout = 30 * time.Second
