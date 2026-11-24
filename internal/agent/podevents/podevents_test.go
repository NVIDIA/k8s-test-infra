// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

package podevents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
)

// TestEventsLandOnThePod checks the Event the API server receives: on the
// pod, in its namespace, with the agent as its source.
func TestEventsLandOnThePod(t *testing.T) {
	t.Parallel()
	posted := make(chan corev1.Event, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/namespaces/mokka/events" {
			http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		var e corev1.Event
		if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		posted <- e
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(e)
	}))
	t.Cleanup(srv.Close)

	client, err := coreClient(&rest.Config{Host: srv.URL})
	require.NoError(t, err)
	pod := &corev1.ObjectReference{APIVersion: "v1", Kind: "Pod", Namespace: "mokka", Name: "nvml-mock-abcde", UID: "uid-1"}
	r := New(client, pod, "node-a")
	t.Cleanup(r.Shutdown)

	r.Warning("NICDeclarationIgnored", "NIC declaration ignored")

	select {
	case e := <-posted:
		require.Equal(t, *pod, e.InvolvedObject)
		require.Equal(t, "mokka", e.Namespace)
		require.Equal(t, corev1.EventTypeWarning, e.Type)
		require.Equal(t, "NICDeclarationIgnored", e.Reason)
		require.Equal(t, "NIC declaration ignored", e.Message)
		require.Equal(t, corev1.EventSource{Component: Component, Host: "node-a"}, e.Source)
	case <-time.After(10 * time.Second):
		t.Fatal("no Event reached the API server")
	}
}

func TestFromEnvNeedsThePod(t *testing.T) {
	t.Setenv("POD_NAME", "")
	_, err := FromEnv("node-a")
	require.ErrorContains(t, err, "POD_NAME, POD_NAMESPACE and POD_UID are not all set")
}
