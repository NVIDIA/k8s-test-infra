// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package podevents records the node agent's Kubernetes Events on its own pod,
// where `kubectl describe pod` and event-based alerting see them.
package podevents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
)

// Component is the source the Events name.
const Component = "mokka-node-agent"

// Recorder records Events on one pod.
type Recorder struct {
	broadcaster record.EventBroadcaster
	recorder    record.EventRecorder
	pod         *corev1.ObjectReference
}

// FromEnv returns a Recorder for the pod POD_NAME, POD_NAMESPACE and POD_UID
// name, which the chart sets from the downward API, writing through the pod's
// service account. It returns an error when the pod has no such identity or
// no API access, as outside a cluster; the agent then only logs.
func FromEnv(node string) (*Recorder, error) {
	name, namespace, uid := os.Getenv("POD_NAME"), os.Getenv("POD_NAMESPACE"), os.Getenv("POD_UID")
	if name == "" || namespace == "" || uid == "" {
		return nil, errors.New("POD_NAME, POD_NAMESPACE and POD_UID are not all set")
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("no API access: %w", err)
	}
	client, err := coreClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("no API access: %w", err)
	}
	pod := &corev1.ObjectReference{
		APIVersion: "v1", Kind: "Pod", Namespace: namespace, Name: name, UID: types.UID(uid),
	}
	return New(client, pod, node), nil
}

// coreScheme knows the core types alone. client-go's typed clients register
// every API group, which would double the agent's size for one resource.
func coreScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	return s
}

// coreClient is a REST client for the core API group.
func coreClient(cfg *rest.Config) (rest.Interface, error) {
	cfg = rest.CopyConfig(cfg)
	cfg.APIPath = "/api"
	cfg.GroupVersion = &schema.GroupVersion{Version: "v1"}
	cfg.NegotiatedSerializer = serializer.NewCodecFactory(coreScheme()).WithoutConversion()
	return rest.RESTClientFor(cfg)
}

// New returns a Recorder that records Events on pod through client, a REST
// client for the core API group.
func New(client rest.Interface, pod *corev1.ObjectReference, node string) *Recorder {
	b := record.NewBroadcaster()
	b.StartRecordingToSink(sink{client: client})
	return &Recorder{
		broadcaster: b,
		recorder:    b.NewRecorder(coreScheme(), corev1.EventSource{Component: Component, Host: node}),
		pod:         pod,
	}
}

// sink writes Events through the core REST client.
type sink struct{ client rest.Interface }

const requestTimeout = 10 * time.Second

func (s sink) Create(e *corev1.Event) (*corev1.Event, error) {
	return s.do(s.client.Post().Namespace(e.Namespace).Resource("events").Body(e))
}

func (s sink) Update(e *corev1.Event) (*corev1.Event, error) {
	return s.do(s.client.Put().Namespace(e.Namespace).Resource("events").Name(e.Name).Body(e))
}

func (s sink) Patch(e *corev1.Event, data []byte) (*corev1.Event, error) {
	return s.do(s.client.Patch(types.StrategicMergePatchType).
		Namespace(e.Namespace).Resource("events").Name(e.Name).Body(data))
}

func (s sink) do(r *rest.Request) (*corev1.Event, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	out := &corev1.Event{}
	return out, r.Do(ctx).Into(out)
}

// Warning flags something the agent degraded without failing, such as an
// ignored NIC declaration, where `kubectl describe pod` and event-based
// alerts see it; the pod itself stays ready.
func (r *Recorder) Warning(reason, message string) {
	r.recorder.Event(r.pod, corev1.EventTypeWarning, reason, message)
}

// Normal marks the end of what a Warning flagged, so the pod's events show
// that it recovered rather than only that it once degraded.
func (r *Recorder) Normal(reason, message string) {
	r.recorder.Event(r.pod, corev1.EventTypeNormal, reason, message)
}

// Shutdown releases the broadcaster's goroutine when the agent exits; an
// Event still queued is dropped, since the pod that would show it is going.
func (r *Recorder) Shutdown() { r.broadcaster.Shutdown() }
