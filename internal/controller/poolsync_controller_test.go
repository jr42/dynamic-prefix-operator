/*
Copyright 2026 jr42.

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

package controller

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dynamicprefixiov1alpha1 "github.com/jr42/dynamic-prefix-operator/api/v1alpha1"
)

var _ = Describe("PoolSync Controller", func() {
	Context("When reconciling a CiliumLoadBalancerIPPool", func() {
		const (
			poolName   = "test-pool"
			dpName     = "test-dp"
			subnetName = "lb-pool"
			// Expected CIDR is calculated from base prefix 2001:db8::/48 with offset 0 and prefixLength 64
			subnetCIDR = "2001:db8::/64"
		)

		ctx := context.Background()

		BeforeEach(func() {
			// Create DynamicPrefix
			dp := &dynamicprefixiov1alpha1.DynamicPrefix{
				ObjectMeta: metav1.ObjectMeta{
					Name: dpName,
				},
				Spec: dynamicprefixiov1alpha1.DynamicPrefixSpec{
					Acquisition: dynamicprefixiov1alpha1.AcquisitionSpec{
						RouterAdvertisement: &dynamicprefixiov1alpha1.RouterAdvertisementSpec{
							Interface: "eth0",
							Enabled:   true,
						},
					},
					Subnets: []dynamicprefixiov1alpha1.SubnetSpec{
						{
							Name:         subnetName,
							Offset:       0, // First /64 subnet
							PrefixLength: 64,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, dp)).To(Succeed())

			// Update DynamicPrefix status
			dp.Status = dynamicprefixiov1alpha1.DynamicPrefixStatus{
				CurrentPrefix: "2001:db8::/48",
				Subnets: []dynamicprefixiov1alpha1.SubnetStatus{
					{
						Name: subnetName,
						CIDR: subnetCIDR,
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, dp)).To(Succeed())

			// Create CiliumLoadBalancerIPPool
			pool := &unstructured.Unstructured{}
			pool.SetGroupVersionKind(CiliumLBIPPoolGVK)
			pool.SetName(poolName)
			pool.SetAnnotations(map[string]string{
				AnnotationName:   dpName,
				AnnotationSubnet: subnetName,
			})
			// Set required spec fields
			Expect(unstructured.SetNestedField(pool.Object, []interface{}{}, "spec", "blocks")).To(Succeed())

			Expect(k8sClient.Create(ctx, pool)).To(Succeed())
		})

		AfterEach(func() {
			// Cleanup
			pool := &unstructured.Unstructured{}
			pool.SetGroupVersionKind(CiliumLBIPPoolGVK)
			pool.SetName(poolName)
			_ = k8sClient.Delete(ctx, pool)

			dp := &dynamicprefixiov1alpha1.DynamicPrefix{}
			dp.Name = dpName
			_ = k8sClient.Delete(ctx, dp)
		})

		It("should update pool spec.blocks with subnet CIDR", func() {
			reconciler := &PoolSyncReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: poolName},
			})
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated pool
			pool := &unstructured.Unstructured{}
			pool.SetGroupVersionKind(CiliumLBIPPoolGVK)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: poolName}, pool)).To(Succeed())

			// Check spec.blocks
			blocks, found, err := unstructured.NestedSlice(pool.Object, "spec", "blocks")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(blocks).To(HaveLen(1))

			block := blocks[0].(map[string]interface{})
			Expect(block["cidr"]).To(Equal(subnetCIDR))

			// Check last-sync annotation
			annotations := pool.GetAnnotations()
			Expect(annotations).To(HaveKey(AnnotationLastSync))
		})
	})

	Context("When reconciling a CiliumCIDRGroup", func() {
		const (
			groupName  = "test-cidr-group"
			dpName     = "test-dp-cidr"
			subnetName = "egress"
			// Expected CIDR is calculated from base prefix 2001:db8::/48 with offset 0 and prefixLength 64
			subnetCIDR = "2001:db8::/64"
		)

		ctx := context.Background()

		BeforeEach(func() {
			// Create DynamicPrefix
			dp := &dynamicprefixiov1alpha1.DynamicPrefix{
				ObjectMeta: metav1.ObjectMeta{
					Name: dpName,
				},
				Spec: dynamicprefixiov1alpha1.DynamicPrefixSpec{
					Acquisition: dynamicprefixiov1alpha1.AcquisitionSpec{
						RouterAdvertisement: &dynamicprefixiov1alpha1.RouterAdvertisementSpec{
							Interface: "eth0",
							Enabled:   true,
						},
					},
					Subnets: []dynamicprefixiov1alpha1.SubnetSpec{
						{
							Name:         subnetName,
							Offset:       0, // First /64 subnet
							PrefixLength: 64,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, dp)).To(Succeed())

			// Update DynamicPrefix status
			dp.Status = dynamicprefixiov1alpha1.DynamicPrefixStatus{
				CurrentPrefix: "2001:db8::/48",
				Subnets: []dynamicprefixiov1alpha1.SubnetStatus{
					{
						Name: subnetName,
						CIDR: subnetCIDR,
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, dp)).To(Succeed())

			// Create CiliumCIDRGroup
			group := &unstructured.Unstructured{}
			group.SetGroupVersionKind(CiliumCIDRGroupGVK)
			group.SetName(groupName)
			group.SetAnnotations(map[string]string{
				AnnotationName:   dpName,
				AnnotationSubnet: subnetName,
			})
			// Set required spec fields
			Expect(unstructured.SetNestedField(group.Object, []interface{}{}, "spec", "externalCIDRs")).To(Succeed())

			Expect(k8sClient.Create(ctx, group)).To(Succeed())
		})

		AfterEach(func() {
			// Cleanup
			group := &unstructured.Unstructured{}
			group.SetGroupVersionKind(CiliumCIDRGroupGVK)
			group.SetName(groupName)
			_ = k8sClient.Delete(ctx, group)

			dp := &dynamicprefixiov1alpha1.DynamicPrefix{}
			dp.Name = dpName
			_ = k8sClient.Delete(ctx, dp)
		})

		It("should update group spec.externalCIDRs with subnet CIDR", func() {
			reconciler := &PoolSyncReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: groupName},
			})
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated group
			group := &unstructured.Unstructured{}
			group.SetGroupVersionKind(CiliumCIDRGroupGVK)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: groupName}, group)).To(Succeed())

			// Check spec.externalCIDRs
			cidrs, found, err := unstructured.NestedStringSlice(group.Object, "spec", "externalCIDRs")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(cidrs).To(HaveLen(1))
			Expect(cidrs[0]).To(Equal(subnetCIDR))

			// Check last-sync annotation
			annotations := group.GetAnnotations()
			Expect(annotations).To(HaveKey(AnnotationLastSync))
		})
	})

	Context("When DynamicPrefix has no subnet specified", func() {
		const (
			poolName      = "test-pool-main"
			dpName        = "test-dp-main"
			currentPrefix = "2001:db8:abcd::/48"
		)

		ctx := context.Background()

		BeforeEach(func() {
			// Create DynamicPrefix without subnets
			dp := &dynamicprefixiov1alpha1.DynamicPrefix{
				ObjectMeta: metav1.ObjectMeta{
					Name: dpName,
				},
				Spec: dynamicprefixiov1alpha1.DynamicPrefixSpec{
					Acquisition: dynamicprefixiov1alpha1.AcquisitionSpec{
						RouterAdvertisement: &dynamicprefixiov1alpha1.RouterAdvertisementSpec{
							Interface: "eth0",
							Enabled:   true,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, dp)).To(Succeed())

			// Update DynamicPrefix status with just current prefix
			dp.Status = dynamicprefixiov1alpha1.DynamicPrefixStatus{
				CurrentPrefix: currentPrefix,
			}
			Expect(k8sClient.Status().Update(ctx, dp)).To(Succeed())

			// Create CiliumLoadBalancerIPPool without subnet annotation
			pool := &unstructured.Unstructured{}
			pool.SetGroupVersionKind(CiliumLBIPPoolGVK)
			pool.SetName(poolName)
			pool.SetAnnotations(map[string]string{
				AnnotationName: dpName,
				// No AnnotationSubnet - should use main prefix
			})
			Expect(unstructured.SetNestedField(pool.Object, []interface{}{}, "spec", "blocks")).To(Succeed())

			Expect(k8sClient.Create(ctx, pool)).To(Succeed())
		})

		AfterEach(func() {
			pool := &unstructured.Unstructured{}
			pool.SetGroupVersionKind(CiliumLBIPPoolGVK)
			pool.SetName(poolName)
			_ = k8sClient.Delete(ctx, pool)

			dp := &dynamicprefixiov1alpha1.DynamicPrefix{}
			dp.Name = dpName
			_ = k8sClient.Delete(ctx, dp)
		})

		It("should use the main prefix when no subnet is specified", func() {
			reconciler := &PoolSyncReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: poolName},
			})
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated pool
			pool := &unstructured.Unstructured{}
			pool.SetGroupVersionKind(CiliumLBIPPoolGVK)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: poolName}, pool)).To(Succeed())

			// Check spec.blocks uses main prefix
			blocks, found, err := unstructured.NestedSlice(pool.Object, "spec", "blocks")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(blocks).To(HaveLen(1))

			block := blocks[0].(map[string]interface{})
			Expect(block["cidr"]).To(Equal(currentPrefix))
		})
	})
})

func TestAnnotationConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{
			name:     "AnnotationName",
			constant: AnnotationName,
			expected: "dynamic-prefix.io/name",
		},
		{
			name:     "AnnotationSubnet",
			constant: AnnotationSubnet,
			expected: "dynamic-prefix.io/subnet",
		},
		{
			name:     "AnnotationLastSync",
			constant: AnnotationLastSync,
			expected: "dynamic-prefix.io/last-sync",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("%s = %q, want %q", tt.name, tt.constant, tt.expected)
			}
		})
	}
}

func TestGVKConstants(t *testing.T) {
	if CiliumLBIPPoolGVK.Group != "cilium.io" {
		t.Errorf("CiliumLBIPPoolGVK.Group = %q, want %q", CiliumLBIPPoolGVK.Group, "cilium.io")
	}
	if CiliumLBIPPoolGVK.Version != "v2alpha1" {
		t.Errorf("CiliumLBIPPoolGVK.Version = %q, want %q", CiliumLBIPPoolGVK.Version, "v2alpha1")
	}
	if CiliumLBIPPoolGVK.Kind != "CiliumLoadBalancerIPPool" {
		t.Errorf("CiliumLBIPPoolGVK.Kind = %q, want %q", CiliumLBIPPoolGVK.Kind, "CiliumLoadBalancerIPPool")
	}

	if CiliumCIDRGroupGVK.Group != "cilium.io" {
		t.Errorf("CiliumCIDRGroupGVK.Group = %q, want %q", CiliumCIDRGroupGVK.Group, "cilium.io")
	}
	if CiliumCIDRGroupGVK.Version != "v2alpha1" {
		t.Errorf("CiliumCIDRGroupGVK.Version = %q, want %q", CiliumCIDRGroupGVK.Version, "v2alpha1")
	}
	if CiliumCIDRGroupGVK.Kind != "CiliumCIDRGroup" {
		t.Errorf("CiliumCIDRGroupGVK.Kind = %q, want %q", CiliumCIDRGroupGVK.Kind, "CiliumCIDRGroup")
	}
}
