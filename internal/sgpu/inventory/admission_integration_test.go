// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

//go:build integration

package inventory

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"

	mokkav1alpha1 "github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
	mokkaclient "github.com/NVIDIA/k8s-test-infra/pkg/generated/clientset/versioned"
	mokkalisters "github.com/NVIDIA/k8s-test-infra/pkg/generated/listers/api/v1alpha1"
)

// TestSGPURackAdmissionGarbageCollector needs a disposable Kubernetes 1.30+ cluster
// with no Mokka installation and a running garbage collector. It installs the
// CRDs and the rendered admission policy, not a copy of its CEL expressions.
//
//	MOKKA_ADMISSION_KUBECONFIG=/path/to/disposable/kubeconfig go test -tags=integration \
//	  ./internal/sgpu/inventory -run '^TestSGPURackAdmissionGarbageCollector$' -count=1 -v
func TestSGPURackAdmissionGarbageCollector(t *testing.T) {
	kubeconfig := os.Getenv("MOKKA_ADMISSION_KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("set MOKKA_ADMISSION_KUBECONFIG to an otherwise unused disposable cluster")
	}
	ctx := t.Context()
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	require.NoError(t, err)
	config.Timeout = 10 * time.Second
	kube, err := kubernetes.NewForConfig(config)
	require.NoError(t, err)
	admin, err := mokkaclient.NewForConfig(config)
	require.NoError(t, err)
	_, err = admin.MokkaV1alpha1().SGPURacks().List(ctx, metav1.ListOptions{})
	require.True(t, apierrors.IsNotFound(err), "use an empty disposable cluster, not an existing Mokka installation: %v", err)

	root := filepath.Join("..", "..", "..")
	crds := admissionCommand(t, nil, "helm", "template", "admission-crds", filepath.Join(root, "deployments/mokka-crds/helm/mokka-crds"))
	admissionCommand(t, crds, "kubectl", "--kubeconfig", kubeconfig, "create", "-f", "-")
	t.Cleanup(func() {
		admissionCommand(t, crds, "kubectl", "--kubeconfig", kubeconfig, "delete", "-f", "-", "--wait=false")
	})
	admissionCommand(t, nil, "kubectl", "--kubeconfig", kubeconfig, "wait", "--for=condition=Established", "--timeout=60s",
		"crd/sgpuracks.mokka.nvidia.com", "crd/sgpuinventories.mokka.nvidia.com", "crd/sgpurackprofiles.mokka.nvidia.com")

	const namespace = "mokka-admission-test"
	const controller = "system:serviceaccount:" + namespace + ":admission-nvml-mock-control-plane"
	const untrusted = "system:serviceaccount:default:not-mokka"
	gcIdentities := []string{"system:serviceaccount:kube-system:generic-garbage-collector", "system:kube-controller-manager"}
	_, err = kube.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, kube.CoreV1().Namespaces().Delete(context.Background(), namespace, metav1.DeleteOptions{}))
	})
	// Give all test identities the same RBAC rights so negative cases exercise
	// admission, rather than accidentally passing because authorization failed.
	subjects := make([]rbacv1.Subject, 0, len(gcIdentities)+2)
	for _, username := range append(gcIdentities, controller, untrusted) {
		subjects = append(subjects, rbacv1.Subject{Kind: "User", APIGroup: rbacv1.GroupName, Name: username})
	}
	_, err = kube.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: namespace}, Subjects: subjects,
		RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "cluster-admin"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, kube.RbacV1().ClusterRoleBindings().Delete(context.Background(), namespace, metav1.DeleteOptions{}))
	})
	asUser := func(username string) *mokkaclient.Clientset {
		userConfig := rest.CopyConfig(config)
		userConfig.Impersonate = rest.ImpersonationConfig{UserName: username, Groups: []string{"system:authenticated"}}
		client, clientErr := mokkaclient.NewForConfig(userConfig)
		require.NoError(t, clientErr)
		return client
	}
	mokka := asUser(controller)
	profile, err := admin.MokkaV1alpha1().SGPURackProfiles().Create(ctx, testProfile("admission", "", 0, 1, 1), metav1.CreateOptions{})
	require.NoError(t, err)
	inventory, err := admin.MokkaV1alpha1().SGPUInventories().Create(ctx, testInventory("admission", "", profile.Name, 1), metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx := context.WithoutCancel(ctx)
		racks, listErr := mokka.MokkaV1alpha1().SGPURacks().List(cleanupCtx, metav1.ListOptions{})
		require.NoError(t, listErr)
		for i := range racks.Items {
			racks.Items[i].Finalizers = nil
			_, updateErr := mokka.MokkaV1alpha1().SGPURacks().Update(cleanupCtx, &racks.Items[i], metav1.UpdateOptions{})
			require.NoError(t, updateErr)
		}
		current, getErr := admin.MokkaV1alpha1().SGPUInventories().Get(cleanupCtx, inventory.Name, metav1.GetOptions{})
		if !apierrors.IsNotFound(getErr) {
			require.NoError(t, getErr)
			current.Finalizers = nil
			_, updateErr := admin.MokkaV1alpha1().SGPUInventories().Update(cleanupCtx, current, metav1.UpdateOptions{})
			require.NoError(t, updateErr)
		}
	})

	policy := admissionCommand(t, nil, "helm", "template", "admission", filepath.Join(root, "deployments/nvml-mock/helm/nvml-mock"),
		"--namespace", namespace, "--kube-version", "1.30.0", "--set", "controlPlane.enabled=true",
		"--set", "controlPlane.image.digest=sha256:"+strings.Repeat("0", 64), "--show-only", "templates/controlplane/admission.yaml")
	admissionCommand(t, policy, "kubectl", "--kubeconfig", kubeconfig, "create", "-f", "-")
	t.Cleanup(func() {
		admissionCommand(t, policy, "kubectl", "--kubeconfig", kubeconfig, "delete", "-f", "-")
	})

	reconcile := func(t *testing.T) {
		t.Helper()
		admissionReconcile(ctx, t, mokka, inventory.Name, profile)
	}
	reconcile(t)
	racks, err := mokka.MokkaV1alpha1().SGPURacks().List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, racks.Items, 1)
	rack := &racks.Items[0]
	rack.Finalizers = append(rack.Finalizers, metav1.FinalizerDeleteDependents, "test.mokka.nvidia.com/retain")
	rack, err = mokka.MokkaV1alpha1().SGPURacks().Update(ctx, rack, metav1.UpdateOptions{FieldManager: RackFieldManager})
	require.NoError(t, err)
	// CRD-backed policies need not populate observedGeneration. An actual denial
	// proves that both the policy and its binding have reached admission.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, updateErr := asUser(untrusted).MokkaV1alpha1().SGPURacks().Update(ctx, rack, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
		requireAdmissionDenied(c, updateErr)
	}, time.Minute, 200*time.Millisecond)

	t.Run("non-terminating and create", func(t *testing.T) {
		for _, username := range append(gcIdentities, untrusted) {
			client := asUser(username).MokkaV1alpha1().SGPURacks()
			removed := rack.DeepCopy()
			removed.Finalizers = slices.DeleteFunc(removed.Finalizers, func(f string) bool { return f == metav1.FinalizerDeleteDependents })
			_, updateErr := client.Update(ctx, removed, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
			requireAdmissionDenied(t, updateErr)
			created := rack.DeepCopy()
			created.Name += "-forbidden"
			created.UID, created.ResourceVersion, created.ManagedFields = "", "", nil
			_, createErr := client.Create(ctx, created, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
			requireAdmissionDenied(t, createErr)
		}
		_, updateErr := mokka.MokkaV1alpha1().SGPURacks().Update(ctx, rack, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
		require.NoError(t, updateErr, "Mokka retains its existing write access")
	})

	// A dependent keeps foregroundDeletion stable during dry-run mutation tests;
	// otherwise the real GC can remove it before the impersonated request arrives.
	dependent, err := kube.CoreV1().ConfigMaps(namespace).Create(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: "block-rack-deletion", Finalizers: []string{"test.mokka.nvidia.com/hold"},
		OwnerReferences: []metav1.OwnerReference{{APIVersion: mokkav1alpha1.SchemeGroupVersion.String(), Kind: "SGPURack",
			Name: rack.Name, UID: rack.UID, BlockOwnerDeletion: ptr.To(true)}},
	}}, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, patchErr := kube.CoreV1().ConfigMaps(namespace).Patch(context.WithoutCancel(ctx), dependent.Name, types.MergePatchType,
			[]byte(`{"metadata":{"finalizers":[]}}`), metav1.PatchOptions{})
		if !apierrors.IsNotFound(patchErr) {
			require.NoError(t, patchErr)
		}
	})
	require.NoError(t, admin.MokkaV1alpha1().SGPURacks().Delete(ctx, rack.Name, metav1.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationForeground)}))
	rack, err = mokka.MokkaV1alpha1().SGPURacks().Get(ctx, rack.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.NotNil(t, rack.DeletionTimestamp)
	require.Contains(t, rack.Finalizers, metav1.FinalizerDeleteDependents)
	require.Contains(t, rack.Finalizers, RackFinalizer)

	t.Run("terminating mutations", func(t *testing.T) {
		admissionTerminatingMutations(ctx, t, rack, gcIdentities, asUser)
		removed := rack.DeepCopy()
		removed.Finalizers = slices.DeleteFunc(removed.Finalizers, func(f string) bool { return f == metav1.FinalizerDeleteDependents })
		_, updateErr := asUser(untrusted).MokkaV1alpha1().SGPURacks().Update(ctx, removed, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
		requireAdmissionDenied(t, updateErr)
	})

	t.Run("orphan finalizer with absent optional metadata", func(t *testing.T) {
		orphan := rack.DeepCopy()
		orphan.ObjectMeta = metav1.ObjectMeta{Name: "admission-orphan", Finalizers: []string{RackFinalizer}}
		orphan, createErr := mokka.MokkaV1alpha1().SGPURacks().Create(ctx, orphan, metav1.CreateOptions{})
		require.NoError(t, createErr)
		require.NoError(t, admin.MokkaV1alpha1().SGPURacks().Delete(ctx, orphan.Name, metav1.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationOrphan)}))
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			current, getErr := mokka.MokkaV1alpha1().SGPURacks().Get(ctx, orphan.Name, metav1.GetOptions{})
			require.NoError(c, getErr)
			require.NotNil(c, current.DeletionTimestamp)
			require.Equal(c, []string{RackFinalizer}, current.Finalizers, "GC removes orphan but preserves Mokka cleanup")
		}, time.Minute, 200*time.Millisecond)
		current, getErr := mokka.MokkaV1alpha1().SGPURacks().Get(ctx, orphan.Name, metav1.GetOptions{})
		require.NoError(t, getErr)
		current.Finalizers = nil
		_, updateErr := mokka.MokkaV1alpha1().SGPURacks().Update(ctx, current, metav1.UpdateOptions{})
		require.NoError(t, updateErr)
	})

	t.Run("foreground rack and inventory deletion", func(t *testing.T) {
		rack.Finalizers = slices.DeleteFunc(rack.Finalizers, func(f string) bool { return f == "test.mokka.nvidia.com/retain" })
		_, updateErr := mokka.MokkaV1alpha1().SGPURacks().Update(ctx, rack, metav1.UpdateOptions{FieldManager: RackFieldManager})
		require.NoError(t, updateErr)
		require.NoError(t, admin.MokkaV1alpha1().SGPUInventories().Delete(ctx, inventory.Name, metav1.DeleteOptions{}))
		reconcile(t)
		retiring, getErr := mokka.MokkaV1alpha1().SGPURacks().Get(ctx, rack.Name, metav1.GetOptions{})
		require.NoError(t, getErr)
		require.NotContains(t, retiring.Finalizers, RackFinalizer, "Mokka has finished its cleanup")
		require.Contains(t, retiring.Finalizers, metav1.FinalizerDeleteDependents, "GC must finish foreground deletion")
		retiringInventory, getErr := admin.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
		require.NoError(t, getErr)
		require.Contains(t, retiringInventory.Finalizers, InventoryFinalizer, "inventory waits for the rack to disappear")

		_, patchErr := kube.CoreV1().ConfigMaps(namespace).Patch(ctx, dependent.Name, types.MergePatchType,
			[]byte(`{"metadata":{"finalizers":[]}}`), metav1.PatchOptions{})
		require.NoError(t, patchErr)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			_, getErr := mokka.MokkaV1alpha1().SGPURacks().Get(ctx, rack.Name, metav1.GetOptions{})
			require.True(c, apierrors.IsNotFound(getErr), "rack must be deleted by GC: %v", getErr)
		}, time.Minute, 200*time.Millisecond)
		reconcile(t)
		_, getErr = admin.MokkaV1alpha1().SGPUInventories().Get(ctx, inventory.Name, metav1.GetOptions{})
		require.True(t, apierrors.IsNotFound(getErr), "inventory deletion must complete: %v", getErr)
	})
}

func admissionTerminatingMutations(ctx context.Context, t *testing.T, rack *mokkav1alpha1.SGPURack, identities []string, asUser func(string) *mokkaclient.Clientset) {
	t.Helper()
	cases := []struct {
		name   string
		mutate func(*mokkav1alpha1.SGPURack)
	}{
		{"no finalizer removed", func(r *mokkav1alpha1.SGPURack) { r.Finalizers = slices.Clone(rack.Finalizers) }},
		{"Mokka cleanup removed", func(r *mokkav1alpha1.SGPURack) {
			r.Finalizers = slices.DeleteFunc(r.Finalizers, func(f string) bool { return f == RackFinalizer })
		}},
		{"other finalizer removed", func(r *mokkav1alpha1.SGPURack) {
			r.Finalizers = slices.DeleteFunc(r.Finalizers, func(f string) bool { return f == "test.mokka.nvidia.com/retain" })
		}},
		{"all finalizers removed", func(r *mokkav1alpha1.SGPURack) { r.Finalizers = nil }},
		{"spec changed", func(r *mokkav1alpha1.SGPURack) {
			r.Spec.Nodes[0].NodeRef = &mokkav1alpha1.SGPUNodeReference{Name: "foreign", UID: "foreign"}
		}},
		{"labels changed", func(r *mokkav1alpha1.SGPURack) { r.Labels[InventoryNameLabel] = "foreign" }},
		{"labels removed", func(r *mokkav1alpha1.SGPURack) { r.Labels = nil }},
		{"annotations changed", func(r *mokkav1alpha1.SGPURack) { r.Annotations[InventoryUIDAnnotation] = "foreign" }},
		{"annotations removed", func(r *mokkav1alpha1.SGPURack) { r.Annotations = nil }},
		{"owner references changed", func(r *mokkav1alpha1.SGPURack) { r.OwnerReferences[0].UID = "foreign" }},
		{"owner references removed", func(r *mokkav1alpha1.SGPURack) { r.OwnerReferences = nil }},
	}
	for _, username := range identities {
		t.Run(username, func(t *testing.T) {
			client := asUser(username).MokkaV1alpha1().SGPURacks()
			removed := rack.DeepCopy()
			removed.Finalizers = slices.DeleteFunc(removed.Finalizers, func(f string) bool { return f == metav1.FinalizerDeleteDependents })
			_, err := client.Update(ctx, removed, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
			require.NoError(t, err, "trusted GC may remove only its finalizer")
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					mutated := removed.DeepCopy()
					tc.mutate(mutated)
					_, err := client.Update(ctx, mutated, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
					requireAdmissionDenied(t, err)
				})
			}
			// Kubernetes itself also rejects adding finalizers once deletion starts.
			added := removed.DeepCopy()
			added.Finalizers = append(added.Finalizers, "test.mokka.nvidia.com/new")
			_, err = client.Update(ctx, added, metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
			require.Error(t, err)
			require.True(t, apierrors.IsInvalid(err) || apierrors.IsForbidden(err), "%v", err)
		})
	}
}

func requireAdmissionDenied(t require.TestingT, err error) {
	require.True(t, apierrors.IsInvalid(err) || apierrors.IsForbidden(err), "expected admission denial, got %v", err)
	require.ErrorContains(t, err, "SGPURack writes are restricted")
}

func admissionReconcile(ctx context.Context, t *testing.T, mokka *mokkaclient.Clientset, name string, profile *mokkav1alpha1.SGPURackProfile) {
	t.Helper()
	inventoryIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	profileIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	rackIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, Indexers())
	nodeIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	inventory, err := mokka.MokkaV1alpha1().SGPUInventories().Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)
	require.NoError(t, inventoryIndexer.Add(inventory))
	require.NoError(t, profileIndexer.Add(profile))
	racks, err := mokka.MokkaV1alpha1().SGPURacks().List(ctx, metav1.ListOptions{})
	require.NoError(t, err)
	for i := range racks.Items {
		require.NoError(t, rackIndexer.Add(racks.Items[i].DeepCopy()))
	}
	reconciler := NewReconciler(newListerCache(mokkalisters.NewSGPUInventoryLister(inventoryIndexer),
		mokkalisters.NewSGPURackProfileLister(profileIndexer), rackIndexer, newNodeLister(nodeIndexer)),
		mokka.MokkaV1alpha1().SGPUInventories(), mokka.MokkaV1alpha1().SGPURacks(), nil)
	result, err := reconciler.Reconcile(ctx, name)
	// The terminal live check requests a retry while GC still holds the rack.
	if !errors.Is(err, ErrRackCacheStale) {
		require.NoError(t, err)
	}
	require.Empty(t, result.CleanupNeeded, "unassigned rack needs no node projection cleanup")
}

func admissionCommand(t *testing.T, stdin []byte, name string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	require.NoError(t, err, "%s %s: %s", name, strings.Join(args, " "), stderr.String())
	return out
}
