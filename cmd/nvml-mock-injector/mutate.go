// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/NVIDIA/k8s-test-infra/cmd/nvml-mock-injector/tier"
)

func mutatePod(pod *corev1.Pod, cfg tier.Config) (*corev1.Pod, error) {
	t, err := cfg.Resolve(pod)
	if err != nil {
		return nil, err
	}
	if t == "" {
		return pod, nil
	}
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	device := tier.CDIDeviceForTier(t)
	pod.Annotations[tier.CDIAnnotMock] = device
	pod.Annotations[tier.AnnotationInjectedTier] = t
	if t == tier.TierGPU {
		pod.Annotations[tier.CDIAnnotGPU] = "all"
	}
	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName == "" {
		nvidia := tier.RuntimeNvidia
		pod.Spec.RuntimeClassName = &nvidia
	}
	return pod, nil
}

func buildPatch(original, mutated []byte) ([]byte, error) {
	var origPod, mutPod corev1.Pod
	if err := json.Unmarshal(original, &origPod); err != nil {
		return nil, fmt.Errorf("unmarshal original pod: %w", err)
	}
	if err := json.Unmarshal(mutated, &mutPod); err != nil {
		return nil, fmt.Errorf("unmarshal mutated pod: %w", err)
	}
	return buildPodJSONPatch(&origPod, &mutPod)
}

type jsonPatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value,omitempty"`
}

func buildPodJSONPatch(orig, mut *corev1.Pod) ([]byte, error) {
	ops := make([]jsonPatchOp, 0, len(mut.Annotations)+1)

	for key, value := range mut.Annotations {
		if orig.Annotations[key] == value {
			continue
		}
		op := "add"
		if _, ok := orig.Annotations[key]; ok {
			op = "replace"
		}
		ops = append(ops, jsonPatchOp{
			Op:    op,
			Path:  "/metadata/annotations/" + jsonPointerEscape(key),
			Value: value,
		})
	}

	origRuntime := ""
	if orig.Spec.RuntimeClassName != nil {
		origRuntime = *orig.Spec.RuntimeClassName
	}
	mutRuntime := ""
	if mut.Spec.RuntimeClassName != nil {
		mutRuntime = *mut.Spec.RuntimeClassName
	}
	if mutRuntime != "" && mutRuntime != origRuntime {
		op := "add"
		if orig.Spec.RuntimeClassName != nil {
			op = "replace"
		}
		ops = append(ops, jsonPatchOp{
			Op:    op,
			Path:  "/spec/runtimeClassName",
			Value: mutRuntime,
		})
	}

	return json.Marshal(ops)
}

func jsonPointerEscape(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

func mutateAdmissionReview(req admissionv1.AdmissionReview, cfg tier.Config) admissionv1.AdmissionResponse {
	if req.Request == nil {
		return admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Status:  metav1.StatusFailure,
				Message: "missing admission request",
			},
		}
	}

	uid := req.Request.UID
	if req.Request.Kind.Kind != "Pod" {
		return admissionv1.AdmissionResponse{
			UID:     uid,
			Allowed: true,
		}
	}

	pod := &corev1.Pod{}
	if err := json.Unmarshal(req.Request.Object.Raw, pod); err != nil {
		return admissionv1.AdmissionResponse{
			UID:     uid,
			Allowed: false,
			Result: &metav1.Status{
				Status:  metav1.StatusFailure,
				Message: fmt.Sprintf("could not unmarshal pod: %v", err),
			},
		}
	}

	resolvedTier, err := cfg.Resolve(pod)
	if err != nil {
		return admissionv1.AdmissionResponse{
			UID:     uid,
			Allowed: false,
			Result: &metav1.Status{
				Status:  metav1.StatusFailure,
				Message: err.Error(),
			},
		}
	}
	if resolvedTier == "" {
		return admissionv1.AdmissionResponse{
			UID:     uid,
			Allowed: true,
		}
	}

	original := append([]byte(nil), req.Request.Object.Raw...)
	mutatedPod, err := mutatePod(pod.DeepCopy(), cfg)
	if err != nil {
		return admissionv1.AdmissionResponse{
			UID:     uid,
			Allowed: false,
			Result: &metav1.Status{
				Status:  metav1.StatusFailure,
				Message: err.Error(),
			},
		}
	}

	mutated, err := json.Marshal(mutatedPod)
	if err != nil {
		return admissionv1.AdmissionResponse{
			UID:     uid,
			Allowed: false,
			Result: &metav1.Status{
				Status:  metav1.StatusFailure,
				Message: fmt.Sprintf("could not marshal mutated pod: %v", err),
			},
		}
	}

	patch, err := buildPatch(original, mutated)
	if err != nil {
		return admissionv1.AdmissionResponse{
			UID:     uid,
			Allowed: false,
			Result: &metav1.Status{
				Status:  metav1.StatusFailure,
				Message: fmt.Sprintf("could not build patch: %v", err),
			},
		}
	}

	patchType := admissionv1.PatchTypeJSONPatch
	return admissionv1.AdmissionResponse{
		UID:       uid,
		Allowed:   true,
		PatchType: &patchType,
		Patch:     patch,
	}
}

func handleMutate(cfg tier.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			klog.ErrorS(err, "failed to read admission review body")
			http.Error(w, "could not read request body", http.StatusBadRequest)
			return
		}

		review := admissionv1.AdmissionReview{}
		if _, _, err := admissionCodecs.UniversalDeserializer().Decode(body, nil, &review); err != nil {
			klog.ErrorS(err, "failed to decode admission review")
			http.Error(w, "could not decode admission review", http.StatusBadRequest)
			return
		}

		admissionResp := mutateAdmissionReview(review, cfg)
		review.Response = &admissionResp

		out, err := json.Marshal(review)
		if err != nil {
			klog.ErrorS(err, "failed to marshal admission review response")
			http.Error(w, "could not marshal response", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(out); err != nil {
			klog.ErrorS(err, "failed to write admission review response")
		}
	}
}
