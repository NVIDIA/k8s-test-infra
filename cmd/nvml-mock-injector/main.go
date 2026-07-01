// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

// Command nvml-mock-injector is a mutating admission webhook that overlays the
// mock GPU environment onto every pod (see package inject). It is deployed by
// the nvml-mock Helm chart with failurePolicy: Ignore.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/NVIDIA/k8s-test-infra/cmd/nvml-mock-injector/inject"
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func loadConfig() inject.Config {
	gpu, _ := strconv.Atoi(envOr("GPU_COUNT", "0"))
	return inject.Config{
		HostPath:          envOr("OVERLAY_HOST_PATH", "/var/lib/nvml-mock"),
		MountPath:         envOr("OVERLAY_MOUNT_PATH", "/opt/nvml-mock"),
		EnableIB:          !strings.EqualFold(os.Getenv("OVERLAY_IB"), "false"),
		EnablePCI:         !strings.EqualFold(os.Getenv("OVERLAY_PCI"), "false"),
		OptOutAnnotation:  envOr("OPT_OUT_ANNOTATION", "nvml-mock.nvidia.com/inject"),
		DevicesAnnotation: envOr("DEVICES_ANNOTATION", "nvml-mock.nvidia.com/devices"),
		GPUCount:          gpu,
	}
}

func main() {
	cfg := loadConfig()
	cert := envOr("TLS_CERT_FILE", "/tls/tls.crt")
	key := envOr("TLS_KEY_FILE", "/tls/tls.key")
	addr := envOr("LISTEN_ADDR", ":8443")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/mutate", func(w http.ResponseWriter, r *http.Request) {
		handleMutate(w, r, cfg)
	})

	log.Printf("nvml-mock-injector listening on %s (mountPath=%s ib=%t pci=%t gpuCount=%d)",
		addr, cfg.MountPath, cfg.EnableIB, cfg.EnablePCI, cfg.GPUCount)
	log.Fatal(http.ListenAndServeTLS(addr, cert, key, mux))
}

func handleMutate(w http.ResponseWriter, r *http.Request, cfg inject.Config) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var review admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &review); err != nil || review.Request == nil {
		http.Error(w, "invalid AdmissionReview", http.StatusBadRequest)
		return
	}
	req := review.Request

	resp := &admissionv1.AdmissionResponse{UID: req.UID, Allowed: true}
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		// Fail open: allow unmodified rather than block scheduling.
		log.Printf("decode pod %s/%s: %v (allowing unmodified)", req.Namespace, req.Name, err)
		writeReview(w, review, resp)
		return
	}

	ops, err := inject.Mutate(&pod, cfg)
	if err != nil {
		log.Printf("mutate pod %s/%s: %v (allowing unmodified)", req.Namespace, req.Name, err)
		writeReview(w, review, resp)
		return
	}
	if len(ops) > 0 {
		patch, mErr := json.Marshal(ops)
		if mErr != nil {
			log.Printf("marshal patch: %v (allowing unmodified)", mErr)
			writeReview(w, review, resp)
			return
		}
		pt := admissionv1.PatchTypeJSONPatch
		resp.PatchType = &pt
		resp.Patch = patch
	}
	writeReview(w, review, resp)
}

func writeReview(w http.ResponseWriter, in admissionv1.AdmissionReview, resp *admissionv1.AdmissionResponse) {
	out := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"},
		Response: resp,
	}
	_ = in
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		http.Error(w, fmt.Sprintf("encode response: %v", err), http.StatusInternalServerError)
	}
}
