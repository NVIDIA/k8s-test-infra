// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Command mokka-kwok runs the rack-scoped KWOK data plane.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"

	"github.com/NVIDIA/k8s-test-infra/internal/logging"
	mokkaclient "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned"
)

const fieldManager = "mokka-kwok-data-plane"

type config struct {
	shard        string
	listen       string
	controlPlane string
	batch        batchOptions
}

func parseConfig(args []string) (config, error) {
	var c config
	if len(args) == 0 || args[0] != "data-plane" {
		return c, errors.New("usage: mokka-kwok data-plane --shard-name NAME")
	}
	flags := flag.NewFlagSet("data-plane", flag.ContinueOnError)
	flags.StringVar(&c.shard, "shard-name", "", "replica name on assigned racks")
	flags.StringVar(&c.listen, "listen-addr", ":8080", "health and readiness listener")
	flags.StringVar(&c.controlPlane, "control-plane-url", "http://nvml-mock-control-plane:8080", "base URL of the Mokka control-plane Service")
	flags.IntVar(&c.batch.size, "batch-size", 100, "maximum racks per request (1-100)")
	flags.DurationVar(&c.batch.window, "batch-window", time.Second, "maximum time to collect a batch")
	flags.IntVar(&c.batch.workers, "max-in-flight", 2, "maximum concurrent rack requests")
	if err := flags.Parse(args[1:]); err != nil {
		return c, err
	}
	if flags.NArg() != 0 || len(validation.IsDNS1123Label(c.shard)) != 0 {
		return c, errors.New("expected a DNS label for --shard-name and no positional arguments")
	}
	if _, _, err := net.SplitHostPort(c.listen); err != nil {
		return c, fmt.Errorf("invalid listen address: %w", err)
	}
	c.controlPlane = strings.TrimRight(c.controlPlane, "/")
	return c, c.validateRackClient()
}

func (c config) validateRackClient() error {
	u, err := url.Parse(c.controlPlane)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("expected an http(s) --control-plane-url")
	}
	if c.batch.size < 1 || c.batch.size > 100 || c.batch.window <= 0 || c.batch.workers < 1 {
		return errors.New("invalid rack batch limits")
	}
	return nil
}

func run(ctx context.Context, args []string) error {
	c, err := parseConfig(args)
	if err != nil {
		return err
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("load cluster configuration: %w", err)
	}
	cfg.UserAgent = fieldManager
	mokka, err := mokkaclient.NewForConfig(cfg)
	if err != nil {
		return err
	}
	kube, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	nodes, err := metadata.NewForConfig(cfg)
	if err != nil {
		return err
	}
	m := newModel(mokka, kube, nodes, c) //nolint:contextcheck // RunWithContext uses the generated context-aware list and watch callbacks.
	return m.run(ctx, c)
}

func main() {
	logger := logging.NewLogger(logging.Config{Level: logging.LevelInfo, Format: logging.FormatJSON})
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		zap.L().Error("KWOK data plane stopped", zap.Error(err))
		_ = logger.Sync()
		os.Exit(1)
	}
}
