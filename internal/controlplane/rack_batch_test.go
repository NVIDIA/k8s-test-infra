// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package controlplane

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/iotest"

	state "github.com/NVIDIA/k8s-test-infra/pkg/rackstate/v1alpha1"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestRackBatchResponse(t *testing.T) {
	read := func(requests []state.RackRequest) ([]state.RackResult, error) {
		require.Len(t, requests, 3)
		return []state.RackResult{
			{Reference: requests[0].Object, Status: state.NotReady},
			{Rack: &state.Rack{Hardware: state.Hardware{Rack: requests[1].Object, RackDeliveryGeneration: 8}, Nodes: []state.Slot{}}},
			{Reference: requests[2].Object, Status: state.NotFound},
		}, nil
	}
	handler := newServer(DefaultConfig(), zap.NewNop(), func() bool { return true }, read).Router()
	req := httptest.NewRequest(http.MethodPost, rackBatchPath, strings.NewReader(`{"racks":[{"name":"a","uid":"one","minRackDeliveryGeneration":9},{"name":"b","uid":"two"},{"name":"c","uid":"three"}]}`))
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "gzip", w.Header().Get("Content-Encoding"))
	zipped, err := gzip.NewReader(w.Body)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, zipped.Close()) })
	var resp struct {
		APIVersion string
		Racks      []json.RawMessage
	}
	require.NoError(t, json.NewDecoder(zipped).Decode(&resp))
	require.Equal(t, state.Version, resp.APIVersion)
	require.Len(t, resp.Racks, 3)
	require.JSONEq(t, `{"rack":{"name":"a","uid":"one"},"status":"NotReady"}`, string(resp.Racks[0]))
	var rack state.Rack
	require.NoError(t, json.Unmarshal(resp.Racks[1], &rack))
	require.Equal(t, "b", rack.Hardware.Rack.Name)
	require.EqualValues(t, 8, rack.RackDeliveryGeneration)
	require.JSONEq(t, `{"rack":{"name":"c","uid":"three"},"status":"NotFound"}`, string(resp.Racks[2]))
	var entry state.RackResult
	for _, raw := range resp.Racks {
		require.NoError(t, json.Unmarshal(raw, &entry))
		require.Equal(t, entry.Status == "", entry.Rack != nil)
		require.NotEmpty(t, entry.Reference.UID)
		encoded, err := json.Marshal(entry)
		require.NoError(t, err)
		require.JSONEq(t, string(raw), string(encoded))
	}
}

func TestRackBatchLimits(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	reader := func(_ []state.RackRequest) ([]state.RackResult, error) { close(entered); <-release; return nil, nil }
	h := batchHandler(func() bool { return true }, reader, 1)
	send := func(handler http.Handler, body io.Reader) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, rackBatchPath, body))
		return w
	}
	single := `{"racks":[{"name":"a","uid":"1"}]}`
	reqBody := func(count int) *bytes.Reader {
		req := state.BatchRequest{}
		for range count {
			req.Racks = append(req.Racks, state.RackRequest{Object: state.Object{Name: "a", UID: "1"}})
		}
		raw, err := json.Marshal(req)
		require.NoError(t, err)
		return bytes.NewReader(raw)
	}
	for _, body := range []string{"{}", single + " {}", `{"racks":[{"name":"a"}]}`, `{"racks":[{"name":"a","uid":"1","minRackDeliveryGeneration":-1}]}`} {
		require.Equal(t, http.StatusBadRequest, send(h, strings.NewReader(body)).Code)
	}
	require.Equal(t, http.StatusBadRequest, send(h, reqBody(101)).Code)
	require.Equal(t, http.StatusServiceUnavailable, send(batchHandler(func() bool { return false }, reader, 1), strings.NewReader(single)).Code)
	done := make(chan *httptest.ResponseRecorder, 1)
	full := reqBody(100)
	go func() { done <- send(h, full) }()
	<-entered
	busy := send(h, strings.NewReader(single))
	close(release)
	require.Equal(t, http.StatusTooManyRequests, busy.Code)
	require.Equal(t, "1", busy.Header().Get("Retry-After"))
	require.Equal(t, http.StatusOK, (<-done).Code)
}

// Drive the same timeout error net/http gives the handler when its body read
// deadline expires. One slot suffices to verify that the next batch can run.
func TestRackBatchReleasesSlotAfterReadTimeout(t *testing.T) {
	server := NewServer(DefaultConfig(), zap.NewNop())
	require.Positive(t, server.http.ReadTimeout)
	require.Greater(t, server.http.WriteTimeout, server.http.ReadTimeout)
	h := batchHandler(func() bool { return true }, func([]state.RackRequest) ([]state.RackResult, error) { return nil, nil }, 1)
	send := func(body io.Reader) int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, rackBatchPath, body))
		return w.Code
	}
	require.Equal(t, http.StatusRequestTimeout, send(iotest.ErrReader(os.ErrDeadlineExceeded)))
	require.Equal(t, http.StatusOK, send(strings.NewReader(`{"racks":[{"name":"a","uid":"1"}]}`)))
}
