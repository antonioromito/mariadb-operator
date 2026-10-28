/*
Copyright 2026.

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

package functional_test

import (
	"fmt"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports

	remediationv1 "github.com/openstack-k8s-operators/infra-operator/apis/remediation/v1beta1"
	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadbCtrl "github.com/openstack-k8s-operators/mariadb-operator/internal/controller"
	"github.com/openstack-k8s-operators/mariadb-operator/internal/mariadb"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
)

var _ = Describe("Galera PVC remediation quorum", func() {
	It("grants consent only when a strict majority of replicas is available", func() {
		replicas := int32(3)
		for _, availableReplicas := range []int32{0, 1, 2} {
			name := fmt.Sprintf("quorum-%d", availableReplicas)
			instance := &mariadbv1.Galera{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: mariadbv1.GaleraSpec{
					GaleraSpecCore: mariadbv1.GaleraSpecCore{Replicas: &replicas},
				},
			}
			stsName := mariadb.StatefulSetName(instance.Name)
			pvcNames := make([]string, replicas)
			for i := int32(0); i < replicas; i++ {
				pvcName := mariadb.PVCName(stsName, i)
				pvcNames[i] = pvcName
				pvc := &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pvcName,
						Namespace: namespace,
						Annotations: map[string]string{
							remediationv1.PVCStuckOnNodeAnnotation: "worker-0",
							remediationv1.RequestIDAnnotation:      fmt.Sprintf("request-%d", i),
						},
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
						},
					},
				}
				Expect(k8sClient.Create(ctx, pvc)).To(Succeed())
				DeferCleanup(func() { Expect(k8sClient.Delete(ctx, pvc)).To(Succeed()) })
			}

			kclient, err := kubernetes.NewForConfig(cfg)
			Expect(err).NotTo(HaveOccurred())
			galeraHelper, err := helper.NewHelper(instance, k8sClient, kclient, scheme.Scheme, logr.Discard())
			Expect(err).NotTo(HaveOccurred())
			reconciler := &mariadbCtrl.GaleraReconciler{Client: k8sClient}

			Expect(reconciler.EnsurePVCAvailability(ctx, instance, galeraHelper, availableReplicas)).To(Succeed())
			Expect(instance.Status.UnavailablePods).To(HaveLen(3))
			for i, pvcName := range pvcNames {
				podName := mariadb.PodNameFromPVCName(pvcName)
				status := instance.Status.UnavailablePods[podName]
				Expect(status.Remediation).To(Equal(i == 0))

				pvc := &corev1.PersistentVolumeClaim{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: namespace}, pvc)).To(Succeed())
				if i == 0 && availableReplicas >= 2 {
					Expect(pvc.Annotations[remediationv1.SafeToDeleteAnnotation]).To(Equal("true"))
					Expect(pvc.Annotations[remediationv1.ConsentIDAnnotation]).To(Equal("request-0"))
				} else {
					Expect(pvc.Annotations[remediationv1.SafeToDeleteAnnotation]).To(BeEmpty())
					Expect(pvc.Annotations[remediationv1.ConsentIDAnnotation]).To(BeEmpty())
				}
			}
		}
	})
})
