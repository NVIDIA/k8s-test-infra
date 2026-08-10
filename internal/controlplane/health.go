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

// Readyz is the readiness probe. The init slice has no downstream dependencies,
// so this is 200 whenever the handler is reachable. Follow-up work adds a
// probe registry here for Redis / kube-apiserver / etc.
func Readyz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintln(w, "ok")
}
