// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	options := defaultOptions()
	options.addFlags(flag.CommandLine)
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := options.run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "mokka-controller: %v\n", err)
		os.Exit(1)
	}
}
