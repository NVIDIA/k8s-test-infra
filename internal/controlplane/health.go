// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package controlplane

import (
	"fmt"
	"net/http"
)

// Healthz is the liveness probe. It reports 200 while the handler is
// reachable: any failure at this layer is already transport-level, and deeper
// dependency checks belong on Readyz so a hung dependency does not trigger
// pod restarts.
func Healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintln(w, "ok")
}

// Readyz is the readiness probe used when no dependency gate is configured.
func Readyz(w http.ResponseWriter, _ *http.Request) {
	readyzWhen(func() bool { return true })(w, nil)
}

func readyzWhen(ready func() bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if !ready() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	}
}
