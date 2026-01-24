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

var _ = Describe("BGPSync Controller", func() {
	Context("When reconciling a DynamicPrefix with BGP-enabled subnet", func() {
		const (
			dpName     = "test-bgp-dp"
			subnetName = "loadbalancers"
			community  = "65001:42"
		)

		ctx := context.Background()

		BeforeEach(func() {
			// Create DynamicPrefix with BGP-enabled subnet
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
							Offset:       0,
							PrefixLength: 64,
							BGP: &dynamicprefixiov1alpha1.SubnetBGPSpec{
								Advertise: true,
								Community: community,
							},
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
						CIDR: "2001:db8::/64",
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, dp)).To(Succeed())
		})

		AfterEach(func() {
			// Cleanup DynamicPrefix
			dp := &dynamicprefixiov1alpha1.DynamicPrefix{}
			dp.Name = dpName
			_ = k8sClient.Delete(ctx, dp)

			// Cleanup CiliumBGPAdvertisement
			adv := &unstructured.Unstructured{}
			adv.SetGroupVersionKind(CiliumBGPAdvertisementGVK)
			adv.SetName("dp-" + dpName + "-" + subnetName)
			_ = k8sClient.Delete(ctx, adv)
		})

		It("should create a CiliumBGPAdvertisement", func() {
			reconciler := &BGPSyncReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: dpName},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify CiliumBGPAdvertisement was created
			adv := &unstructured.Unstructured{}
			adv.SetGroupVersionKind(CiliumBGPAdvertisementGVK)
			advName := "dp-" + dpName + "-" + subnetName
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: advName}, adv)).To(Succeed())

			// Check labels
			labels := adv.GetLabels()
			Expect(labels).To(HaveKeyWithValue(LabelManagedBy, LabelManagedByValue))
			Expect(labels).To(HaveKeyWithValue(LabelDynamicPrefixName, dpName))
			Expect(labels).To(HaveKeyWithValue(LabelSubnetName, subnetName))

			// Check spec
			advertisements, found, err := unstructured.NestedSlice(adv.Object, "spec", "advertisements")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(advertisements).To(HaveLen(1))

			advSpec := advertisements[0].(map[string]interface{})
			Expect(advSpec["advertisementType"]).To(Equal("Service"))

			// Check service addresses
			service, found, err := unstructured.NestedMap(advSpec, "service")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(service["addresses"]).To(ContainElement("LoadBalancerIP"))

			// Check community
			communities, found, err := unstructured.NestedStringSlice(advSpec, "attributes", "communities", "standard")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(communities).To(ContainElement(community))
		})

		It("should update DynamicPrefix status with advertisement name", func() {
			reconciler := &BGPSyncReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: dpName},
			})
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated DynamicPrefix
			var dp dynamicprefixiov1alpha1.DynamicPrefix
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: dpName}, &dp)).To(Succeed())

			// Check status
			Expect(dp.Status.Subnets).To(HaveLen(1))
			Expect(dp.Status.Subnets[0].BGPAdvertisement).To(Equal("dp-" + dpName + "-" + subnetName))

			// Check condition
			var bgpCondition *metav1.Condition
			for i := range dp.Status.Conditions {
				if dp.Status.Conditions[i].Type == dynamicprefixiov1alpha1.ConditionTypeBGPAdvertisementReady {
					bgpCondition = &dp.Status.Conditions[i]
					break
				}
			}
			Expect(bgpCondition).NotTo(BeNil())
			Expect(bgpCondition.Status).To(Equal(metav1.ConditionTrue))
			Expect(bgpCondition.Reason).To(Equal("AdvertisementsReady"))
		})
	})

	Context("When reconciling a DynamicPrefix without BGP-enabled subnets", func() {
		const (
			dpName     = "test-no-bgp-dp"
			subnetName = "no-bgp-subnet"
		)

		ctx := context.Background()

		BeforeEach(func() {
			// Create DynamicPrefix without BGP
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
							Offset:       0,
							PrefixLength: 64,
							// No BGP spec
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
						CIDR: "2001:db8::/64",
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, dp)).To(Succeed())
		})

		AfterEach(func() {
			dp := &dynamicprefixiov1alpha1.DynamicPrefix{}
			dp.Name = dpName
			_ = k8sClient.Delete(ctx, dp)
		})

		It("should set BGPAdvertisementReady condition to False with NoBGPSubnets reason", func() {
			reconciler := &BGPSyncReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: dpName},
			})
			Expect(err).NotTo(HaveOccurred())

			// Fetch updated DynamicPrefix
			var dp dynamicprefixiov1alpha1.DynamicPrefix
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: dpName}, &dp)).To(Succeed())

			// Check condition
			var bgpCondition *metav1.Condition
			for i := range dp.Status.Conditions {
				if dp.Status.Conditions[i].Type == dynamicprefixiov1alpha1.ConditionTypeBGPAdvertisementReady {
					bgpCondition = &dp.Status.Conditions[i]
					break
				}
			}
			Expect(bgpCondition).NotTo(BeNil())
			Expect(bgpCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(bgpCondition.Reason).To(Equal("NoBGPSubnets"))
		})
	})

	Context("When BGP is disabled on a subnet that previously had it enabled", func() {
		const (
			dpName     = "test-bgp-disable-dp"
			subnetName = "was-bgp-enabled"
		)

		ctx := context.Background()

		BeforeEach(func() {
			// Create DynamicPrefix with BGP enabled
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
							Offset:       0,
							PrefixLength: 64,
							BGP: &dynamicprefixiov1alpha1.SubnetBGPSpec{
								Advertise: true,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, dp)).To(Succeed())

			dp.Status = dynamicprefixiov1alpha1.DynamicPrefixStatus{
				CurrentPrefix: "2001:db8::/48",
				Subnets: []dynamicprefixiov1alpha1.SubnetStatus{
					{
						Name: subnetName,
						CIDR: "2001:db8::/64",
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, dp)).To(Succeed())

			// Reconcile to create the advertisement
			reconciler := &BGPSyncReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: dpName},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify advertisement exists
			adv := &unstructured.Unstructured{}
			adv.SetGroupVersionKind(CiliumBGPAdvertisementGVK)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "dp-" + dpName + "-" + subnetName}, adv)).To(Succeed())
		})

		AfterEach(func() {
			dp := &dynamicprefixiov1alpha1.DynamicPrefix{}
			dp.Name = dpName
			_ = k8sClient.Delete(ctx, dp)

			adv := &unstructured.Unstructured{}
			adv.SetGroupVersionKind(CiliumBGPAdvertisementGVK)
			adv.SetName("dp-" + dpName + "-" + subnetName)
			_ = k8sClient.Delete(ctx, adv)
		})

		It("should delete the orphaned CiliumBGPAdvertisement", func() {
			// Update DynamicPrefix to disable BGP
			var dp dynamicprefixiov1alpha1.DynamicPrefix
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: dpName}, &dp)).To(Succeed())

			dp.Spec.Subnets[0].BGP.Advertise = false
			Expect(k8sClient.Update(ctx, &dp)).To(Succeed())

			// Reconcile
			reconciler := &BGPSyncReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: dpName},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify advertisement was deleted
			adv := &unstructured.Unstructured{}
			adv.SetGroupVersionKind(CiliumBGPAdvertisementGVK)
			err = k8sClient.Get(ctx, types.NamespacedName{Name: "dp-" + dpName + "-" + subnetName}, adv)
			Expect(err).To(HaveOccurred())
		})
	})
})

func TestAdvertisementNameGeneration(t *testing.T) {
	r := &BGPSyncReconciler{}

	tests := []struct {
		dpName     string
		subnetName string
		expected   string
	}{
		{
			dpName:     "home-ipv6",
			subnetName: "loadbalancers",
			expected:   "dp-home-ipv6-loadbalancers",
		},
		{
			dpName:     "test",
			subnetName: "lb",
			expected:   "dp-test-lb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.dpName+"-"+tt.subnetName, func(t *testing.T) {
			result := r.advertisementName(tt.dpName, tt.subnetName)
			if result != tt.expected {
				t.Errorf("advertisementName(%q, %q) = %q, want %q", tt.dpName, tt.subnetName, result, tt.expected)
			}
		})
	}
}

func TestGetSubnetsWithBGP(t *testing.T) {
	r := &BGPSyncReconciler{}

	tests := []struct {
		name     string
		dp       *dynamicprefixiov1alpha1.DynamicPrefix
		expected int
	}{
		{
			name: "no subnets",
			dp: &dynamicprefixiov1alpha1.DynamicPrefix{
				Spec: dynamicprefixiov1alpha1.DynamicPrefixSpec{},
			},
			expected: 0,
		},
		{
			name: "subnets without BGP",
			dp: &dynamicprefixiov1alpha1.DynamicPrefix{
				Spec: dynamicprefixiov1alpha1.DynamicPrefixSpec{
					Subnets: []dynamicprefixiov1alpha1.SubnetSpec{
						{Name: "test", PrefixLength: 64},
					},
				},
			},
			expected: 0,
		},
		{
			name: "subnets with BGP disabled",
			dp: &dynamicprefixiov1alpha1.DynamicPrefix{
				Spec: dynamicprefixiov1alpha1.DynamicPrefixSpec{
					Subnets: []dynamicprefixiov1alpha1.SubnetSpec{
						{
							Name:         "test",
							PrefixLength: 64,
							BGP:          &dynamicprefixiov1alpha1.SubnetBGPSpec{Advertise: false},
						},
					},
				},
			},
			expected: 0,
		},
		{
			name: "subnets with BGP enabled",
			dp: &dynamicprefixiov1alpha1.DynamicPrefix{
				Spec: dynamicprefixiov1alpha1.DynamicPrefixSpec{
					Subnets: []dynamicprefixiov1alpha1.SubnetSpec{
						{
							Name:         "test",
							PrefixLength: 64,
							BGP:          &dynamicprefixiov1alpha1.SubnetBGPSpec{Advertise: true},
						},
					},
				},
			},
			expected: 1,
		},
		{
			name: "mixed subnets",
			dp: &dynamicprefixiov1alpha1.DynamicPrefix{
				Spec: dynamicprefixiov1alpha1.DynamicPrefixSpec{
					Subnets: []dynamicprefixiov1alpha1.SubnetSpec{
						{
							Name:         "bgp-enabled",
							PrefixLength: 64,
							BGP:          &dynamicprefixiov1alpha1.SubnetBGPSpec{Advertise: true},
						},
						{
							Name:         "no-bgp",
							PrefixLength: 64,
						},
						{
							Name:         "also-bgp",
							PrefixLength: 64,
							BGP:          &dynamicprefixiov1alpha1.SubnetBGPSpec{Advertise: true},
						},
					},
				},
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.getSubnetsWithBGP(tt.dp)
			if len(result) != tt.expected {
				t.Errorf("getSubnetsWithBGP() returned %d subnets, want %d", len(result), tt.expected)
			}
		})
	}
}

func TestBuildAdvertisementSpec(t *testing.T) {
	r := &BGPSyncReconciler{}

	tests := []struct {
		name              string
		subnet            *dynamicprefixiov1alpha1.SubnetSpec
		poolSelector      map[string]interface{}
		expectCommunity   bool
		expectedCommunity string
		expectSelector    bool
	}{
		{
			name: "basic subnet without community",
			subnet: &dynamicprefixiov1alpha1.SubnetSpec{
				Name:         "test",
				PrefixLength: 64,
				BGP:          &dynamicprefixiov1alpha1.SubnetBGPSpec{Advertise: true},
			},
			poolSelector:    nil,
			expectCommunity: false,
			expectSelector:  false,
		},
		{
			name: "subnet with community",
			subnet: &dynamicprefixiov1alpha1.SubnetSpec{
				Name:         "test",
				PrefixLength: 64,
				BGP: &dynamicprefixiov1alpha1.SubnetBGPSpec{
					Advertise: true,
					Community: "65001:42",
				},
			},
			poolSelector:      nil,
			expectCommunity:   true,
			expectedCommunity: "65001:42",
			expectSelector:    false,
		},
		{
			name: "subnet with pool selector",
			subnet: &dynamicprefixiov1alpha1.SubnetSpec{
				Name:         "test",
				PrefixLength: 64,
				BGP:          &dynamicprefixiov1alpha1.SubnetBGPSpec{Advertise: true},
			},
			poolSelector: map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app": "nginx",
				},
			},
			expectCommunity: false,
			expectSelector:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := r.buildAdvertisementSpec(tt.subnet, tt.poolSelector)

			advertisements, ok := spec["advertisements"].([]interface{})
			if !ok || len(advertisements) != 1 {
				t.Fatalf("expected 1 advertisement, got %v", spec["advertisements"])
			}

			adv := advertisements[0].(map[string]interface{})

			// Check advertisementType
			if adv["advertisementType"] != "Service" {
				t.Errorf("advertisementType = %v, want Service", adv["advertisementType"])
			}

			// Check community
			if tt.expectCommunity {
				attrs, ok := adv["attributes"].(map[string]interface{})
				if !ok {
					t.Fatalf("expected attributes, got nil")
				}
				communities, ok := attrs["communities"].(map[string]interface{})
				if !ok {
					t.Fatalf("expected communities, got nil")
				}
				standard, ok := communities["standard"].([]interface{})
				if !ok || len(standard) == 0 {
					t.Fatalf("expected standard communities, got %v", communities["standard"])
				}
				if standard[0] != tt.expectedCommunity {
					t.Errorf("community = %v, want %v", standard[0], tt.expectedCommunity)
				}
			}

			// Check selector
			if tt.expectSelector {
				if adv["selector"] == nil {
					t.Error("expected selector, got nil")
				}
			}
		})
	}
}

func TestCiliumBGPAdvertisementGVK(t *testing.T) {
	if CiliumBGPAdvertisementGVK.Group != "cilium.io" {
		t.Errorf("CiliumBGPAdvertisementGVK.Group = %q, want %q", CiliumBGPAdvertisementGVK.Group, "cilium.io")
	}
	if CiliumBGPAdvertisementGVK.Version != "v2alpha1" {
		t.Errorf("CiliumBGPAdvertisementGVK.Version = %q, want %q", CiliumBGPAdvertisementGVK.Version, "v2alpha1")
	}
	if CiliumBGPAdvertisementGVK.Kind != "CiliumBGPAdvertisement" {
		t.Errorf("CiliumBGPAdvertisementGVK.Kind = %q, want %q", CiliumBGPAdvertisementGVK.Kind, "CiliumBGPAdvertisement")
	}
}

func TestLabelConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{
			name:     "LabelManagedBy",
			constant: LabelManagedBy,
			expected: "app.kubernetes.io/managed-by",
		},
		{
			name:     "LabelManagedByValue",
			constant: LabelManagedByValue,
			expected: "dynamic-prefix-operator",
		},
		{
			name:     "LabelDynamicPrefixName",
			constant: LabelDynamicPrefixName,
			expected: "dynamic-prefix.io/name",
		},
		{
			name:     "LabelSubnetName",
			constant: LabelSubnetName,
			expected: "dynamic-prefix.io/subnet",
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
