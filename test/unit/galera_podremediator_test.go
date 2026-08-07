/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package unit contains unit tests for PodRemediator integration helpers.
// These tests do not require a running cluster or envtest binaries.
package unit

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	controller "github.com/openstack-k8s-operators/mariadb-operator/internal/controller"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/internal/mariadb"
	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func makePVC(name, ns string, labels map[string]string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    labels,
		},
	}
}

func TestIsGaleraPVC(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{"galera PVC", map[string]string{"service": "mygalera-galera"}, true},
		{"no service label", map[string]string{"app": "galera"}, false},
		{"wrong suffix", map[string]string{"service": "mygalera-redis"}, false},
		{"nil labels", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := controller.IsGaleraPVC(makePVC("pvc-0", "ns", tc.labels))
			if got != tc.want {
				t.Errorf("IsGaleraPVC() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFindGaleraForPVC(t *testing.T) {
	// F4: use a fake client so the test fails gracefully (not panics) if
	// FindGaleraForPVC is ever extended to use r.Client.
	scheme := runtime.NewScheme()
	if err := mariadbv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	r := &controller.GaleraReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	tests := []struct {
		name      string
		labels    map[string]string
		ns        string
		wantName  string
		wantNs    string
		wantEmpty bool
	}{
		{
			name:     "standard galera PVC",
			labels:   map[string]string{"service": "openstack-galera-galera"},
			ns:       "openstack",
			wantName: "openstack-galera",
			wantNs:   "openstack",
		},
		{
			name:      "no service label",
			labels:    map[string]string{},
			ns:        "openstack",
			wantEmpty: true,
		},
		{
			name:      "service label without -galera suffix",
			labels:    map[string]string{"service": "some-other-app"},
			ns:        "openstack",
			wantEmpty: true,
		},
		{
			name:     "single-word galera name",
			labels:   map[string]string{"service": "galera-galera"},
			ns:       "default",
			wantName: "galera",
			wantNs:   "default",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reqs := r.FindGaleraForPVC(context.Background(), makePVC("mysql-db-galera-0", tc.ns, tc.labels))
			if tc.wantEmpty {
				if len(reqs) != 0 {
					t.Errorf("expected no requests, got %v", reqs)
				}
				return
			}
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request, got %d", len(reqs))
			}
			got := reqs[0].NamespacedName
			want := types.NamespacedName{Name: tc.wantName, Namespace: tc.wantNs}
			if got != want {
				t.Errorf("got %v, want %v", got, want)
			}
		})
	}
}

func TestQuorumThreshold(t *testing.T) {
	// Verify: floor(spec.Replicas/2)+1 gives the correct minimum for consent.
	//
	// AvailableReplicas counts only healthy, running pods; the dead node whose
	// PVC is stuck is already excluded. The question is: "can the surviving
	// cluster tolerate losing the dead node long enough for a new pod to rejoin?"
	tests := []struct {
		specReplicas int32
		available    int32
		wantConsent  bool
		note         string
	}{
		// 1-node: quorum=1.
		// {1, 1}: node is healthy → PVC can't actually be stuck at this point;
		// this case is unreachable at runtime but validates the formula boundary.
		// {1, 0}: node dead → 0 < 1, no consent — 1-node clusters cannot be auto-remediated.
		{1, 1, true, "1-node healthy: formula boundary (unreachable at runtime)"},
		{1, 0, false, "1-node dead: 0 < quorum(1), no auto-remediation possible"},

		// 3-node: quorum=2. The primary production scenario is {3,2,true}:
		// 1 node dead, 2 healthy nodes maintain Galera majority → consent granted.
		{3, 3, true, "3-node all healthy"},
		{3, 2, true, "3-node, 1 dead: 2 survivors have quorum — primary production scenario"},
		{3, 1, false, "3-node, 2 dead: 1 < quorum(2), cluster already lost quorum"},
		{3, 0, false, "3-node all dead"},

		// 5-node: quorum=3.
		{5, 5, true, "5-node all healthy"},
		{5, 4, true, "5-node, 1 dead: 4 survivors well above quorum(3)"},
		{5, 3, true, "5-node, 2 dead: 3 survivors exactly at quorum(3) — safe to proceed"},
		{5, 2, false, "5-node, 3 dead: 2 < quorum(3), cluster lost quorum"},
	}
	for _, tc := range tests {
		t.Run(tc.note, func(t *testing.T) {
			quorum := tc.specReplicas/2 + 1
			got := tc.available >= quorum
			if got != tc.wantConsent {
				t.Errorf("specReplicas=%d available=%d quorum=%d: consent=%v want %v",
					tc.specReplicas, tc.available, quorum, got, tc.wantConsent)
			}
		})
	}
}

func int32Ptr(i int32) *int32 { return &i }

// buildTestScheme registers all types needed by CheckForStuckPVCRequiringRemediation.
func buildTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		mariadbv1.AddToScheme,
		corev1.AddToScheme,
		appsv1.AddToScheme,
	} {
		if err := add(s); err != nil {
			t.Fatalf("AddToScheme: %v", err)
		}
	}
	return s
}

// makeTestHelper constructs a lib-common Helper backed by the given fake client.
// kclient is nil because our test code path never reaches ExecInPod (r.config == nil).
func makeTestHelper(t *testing.T, instance *mariadbv1.Galera, c client.Client, s *runtime.Scheme) *helper.Helper {
	t.Helper()
	h, err := helper.NewHelper(instance, c, nil, s, logr.Discard())
	if err != nil {
		t.Fatalf("helper.NewHelper: %v", err)
	}
	return h
}

// makeTestInstance creates a Galera instance with a stable UID so StatefulSetLabels
// produces deterministic label selectors usable with the fake client.
func makeTestInstance(ns, name string, replicas int32) *mariadbv1.Galera {
	return &mariadbv1.Galera{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			UID:       types.UID("test-uid-" + name),
		},
		Spec: mariadbv1.GaleraSpec{
			GaleraSpecCore: mariadbv1.GaleraSpecCore{
				Replicas: int32Ptr(replicas),
			},
		},
	}
}

// makeGaleraPVC creates a PVC with labels matching StatefulSetLabels(instance).
func makeGaleraPVC(name string, instance *mariadbv1.Galera, annotations map[string]string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   instance.Namespace,
			Labels:      mariadb.StatefulSetLabels(instance),
			Annotations: annotations,
		},
	}
}

// makeTestSTS creates a StatefulSet for the quorum gate with the given AvailableReplicas.
func makeTestSTS(instance *mariadbv1.Galera, available int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mariadb.StatefulSetName(instance.Name),
			Namespace: instance.Namespace,
		},
		Status: appsv1.StatefulSetStatus{
			AvailableReplicas: available,
		},
	}
}

// TestPVCRemediationStatus_NoCandidates: no stuck PVCs → status is nil.
func TestPVCRemediationStatus_NoCandidates(t *testing.T) {
	s := buildTestScheme(t)
	instance := makeTestInstance("test-ns", "galera", 3)
	pvc := makeGaleraPVC("mysql-db-galera-galera-0", instance, nil)

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(instance, pvc).Build()
	h := makeTestHelper(t, instance, c, s)
	r := &controller.GaleraReconciler{Client: c}

	if err := r.CheckForStuckPVCRequiringRemediation(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if instance.Status.PVCRemediation != nil {
		t.Errorf("expected nil PVCRemediation when no PVCs are annotated, got %v", instance.Status.PVCRemediation)
	}
}

// TestPVCRemediationStatus_OneStuck: PVC annotated as stuck, quorum gate blocks consent
// (AvailableReplicas=0). Status must reflect the stuck PVC with ConsentGranted=false.
func TestPVCRemediationStatus_OneStuck(t *testing.T) {
	s := buildTestScheme(t)
	instance := makeTestInstance("test-ns", "galera", 3)
	pvc := makeGaleraPVC("mysql-db-galera-galera-0", instance, map[string]string{
		"remediation.openstack.org/pvc-stuck-on-node": "worker-0",
	})
	sts := makeTestSTS(instance, 0) // AvailableReplicas=0 < quorum=2 → blocks consent

	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(instance, pvc).
		WithStatusSubresource(sts).
		WithObjects(sts).
		Build()
	h := makeTestHelper(t, instance, c, s)
	r := &controller.GaleraReconciler{Client: c}

	if err := r.CheckForStuckPVCRequiringRemediation(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if instance.Status.PVCRemediation == nil {
		t.Fatal("expected non-nil PVCRemediation when a PVC is stuck")
	}
	entry, ok := instance.Status.PVCRemediation[pvc.Name]
	if !ok {
		t.Fatalf("expected entry for %s in PVCRemediation", pvc.Name)
	}
	if entry.StuckNode != "worker-0" {
		t.Errorf("StuckNode: got %q, want %q", entry.StuckNode, "worker-0")
	}
	if entry.ConsentGranted {
		t.Error("ConsentGranted should be false: quorum gate must block consent")
	}
}

// TestPVCRemediationStatus_AlreadyConsented: PVC already has safe-to-delete=true.
// Not a candidate for new consent; status must show ConsentGranted=true.
func TestPVCRemediationStatus_AlreadyConsented(t *testing.T) {
	s := buildTestScheme(t)
	instance := makeTestInstance("test-ns", "galera", 3)
	pvc := makeGaleraPVC("mysql-db-galera-galera-0", instance, map[string]string{
		"remediation.openstack.org/pvc-stuck-on-node": "worker-0",
		"remediation.openstack.org/safe-to-delete":    "true",
	})

	// No STS needed: candidates is empty so function returns before STS lookup.
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(instance, pvc).Build()
	h := makeTestHelper(t, instance, c, s)
	r := &controller.GaleraReconciler{Client: c}

	if err := r.CheckForStuckPVCRequiringRemediation(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if instance.Status.PVCRemediation == nil {
		t.Fatal("expected non-nil PVCRemediation for already-consented PVC")
	}
	entry, ok := instance.Status.PVCRemediation[pvc.Name]
	if !ok {
		t.Fatalf("expected entry for %s", pvc.Name)
	}
	if !entry.ConsentGranted {
		t.Error("ConsentGranted should be true for already-consented PVC")
	}
}
