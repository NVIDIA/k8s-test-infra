// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package controlplane

import (
	"encoding/json"
	"errors"
	"go.uber.org/zap"
	"io"
	"net/http"
	"os"

	"github.com/NVIDIA/k8s-test-infra/internal/controlplane/controller"
	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
)

const (
	rackBatchPath            = "/v1alpha1/racks/state:batchGet"
	maxRackBatchSize         = 100
	maxConcurrentRackBatches = 4
	maxRackRequestBytes      = 128 << 10
)

type rackBatchReader func([]state.RackRequest) ([]state.RackResult, error)

func batchHandler(ready func() bool, read rackBatchReader, limit int) http.HandlerFunc {
	slots := make(chan struct{}, limit)
	return func(w http.ResponseWriter, r *http.Request) {
		if !ready() || read == nil {
			http.Error(w, "replica is not ready", http.StatusServiceUnavailable)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.Header().Set("Retry-After", "1")
			http.Error(w, "too many concurrent rack batches", http.StatusTooManyRequests)
			return
		}
		var request state.BatchRequest
		if err := decodeRackRequest(http.MaxBytesReader(w, r.Body, maxRackRequestBytes), &request); err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				http.Error(w, "rack request timed out", http.StatusRequestTimeout)
				return
			}
			http.Error(w, "expected 1 to 100 racks with a name, UID and nonnegative minimum generation", http.StatusBadRequest)
			return
		}
		results, err := read(request.Racks)
		if errors.Is(err, controller.ErrCacheNotReady) {
			http.Error(w, "replica is not ready", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			http.Error(w, "could not read rack state", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("Vary", "Accept-Encoding")
		if err := json.NewEncoder(w).Encode(state.BatchResponse{APIVersion: state.Version, Racks: results}); err != nil {
			zap.L().Debug("Could not write rack batch response", zap.Error(err))
		}
	}
}

var errInvalidRackBatch = errors.New("invalid rack batch")

func decodeRackRequest(body io.Reader, request *state.BatchRequest) error {
	dec := json.NewDecoder(body)
	if err := dec.Decode(request); err != nil {
		return err
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		if err != nil {
			return err
		}
		return errInvalidRackBatch
	}
	if len(request.Racks) == 0 || len(request.Racks) > maxRackBatchSize {
		return errInvalidRackBatch
	}
	for _, rack := range request.Racks {
		if rack.Name == "" || rack.UID == "" || rack.MinRackDeliveryGeneration < 0 {
			return errInvalidRackBatch
		}
	}
	return nil
}
