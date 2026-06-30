// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// nvml-mock-injector is a mutating admission webhook that maps pod labels and
// annotations to tiered CDI device injections for nvml-mock.
package main

import (
	"flag"
	"net/http"
	"os"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/klog/v2"

	"github.com/NVIDIA/k8s-test-infra/cmd/nvml-mock-injector/tier"
)

var (
	scheme          = runtime.NewScheme()
	admissionCodecs = serializer.NewCodecFactory(scheme)
)

func init() {
	_ = admissionv1.AddToScheme(scheme)
}

func main() {
	klog.InitFlags(nil)

	tlsCert := flag.String("tls-cert", envOr("TLS_CERT", "/etc/webhook/certs/tls.crt"), "TLS certificate file")
	tlsKey := flag.String("tls-key", envOr("TLS_KEY", "/etc/webhook/certs/tls.key"), "TLS private key file")
	mutatePort := flag.String("port", envOr("PORT", "8443"), "HTTPS port for the /mutate webhook")
	healthPort := flag.String("health-port", envOr("HEALTH_PORT", "8080"), "HTTP port for /healthz probes")
	gpuOperatorComponents := flag.String(
		"gpu-operator-components",
		envOr("GPU_OPERATOR_COMPONENTS", strings.Join(tier.DefaultGPUOperatorComponents, ",")),
		"comma-separated GPU Operator component labels that receive the gpu tier",
	)
	flag.Parse()

	cfg := tier.Config{GPUOperatorComponents: map[string]struct{}{}}
	for _, component := range strings.Split(*gpuOperatorComponents, ",") {
		if component = strings.TrimSpace(component); component != "" {
			cfg.GPUOperatorComponents[component] = struct{}{}
		}
	}

	mutateMux := http.NewServeMux()
	mutateMux.HandleFunc("/mutate", handleMutate(cfg))

	mutateServer := &http.Server{
		Addr:    ":" + *mutatePort,
		Handler: mutateMux,
	}

	healthMux := http.NewServeMux()
	healthMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	healthServer := &http.Server{
		Addr:    ":" + *healthPort,
		Handler: healthMux,
	}

	errCh := make(chan error, 2)
	go func() {
		klog.InfoS("starting health server", "addr", healthServer.Addr)
		errCh <- healthServer.ListenAndServe()
	}()
	go func() {
		klog.InfoS("starting mutating webhook", "addr", mutateServer.Addr, "cert", *tlsCert, "key", *tlsKey)
		errCh <- mutateServer.ListenAndServeTLS(*tlsCert, *tlsKey)
	}()

	if err := <-errCh; err != nil && err != http.ErrServerClosed {
		klog.ErrorS(err, "server exited")
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
