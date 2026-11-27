//go:build e2e

// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// podresources reads the real kubelet socket from inside a Kind worker.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	podresources "k8s.io/kubelet/pkg/apis/podresources/v1"
)

func main() {
	list := flag.Bool("list", false, "read pod allocations instead of node devices")
	flag.Parse()
	if err := read(*list); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func read(list bool) (result error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := grpc.NewClient("passthrough:///kubelet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", "/var/lib/kubelet/pod-resources/kubelet.sock")
		}))
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, connection.Close()) }()
	client := podresources.NewPodResourcesListerClient(connection)
	var response any
	if list {
		response, err = client.List(ctx, &podresources.ListPodResourcesRequest{})
	} else {
		response, err = client.GetAllocatableResources(ctx, &podresources.AllocatableResourcesRequest{})
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(response)
}
