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
	"net/netip"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dynamicprefixiov1alpha1 "github.com/jr42/dynamic-prefix-operator/api/v1alpha1"
	"github.com/jr42/dynamic-prefix-operator/internal/prefix"
)

var _ = Describe("DynamicPrefix Controller", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When creating a DynamicPrefix", func() {
		It("Should add finalizer and update status with prefix", func() {
			ctx := context.Background()

			dpName := "test-dp-basic"
			dp := &dynamicprefixiov1alpha1.DynamicPrefix{
				ObjectMeta: metav1.ObjectMeta{
					Name: dpName,
				},
				Spec: dynamicprefixiov1alpha1.DynamicPrefixSpec{
					Acquisition: dynamicprefixiov1alpha1.AcquisitionSpec{
						DHCPv6PD: &dynamicprefixiov1alpha1.DHCPv6PDSpec{
							Interface: "eth0",
						},
					},
					Subnets: []dynamicprefixiov1alpha1.SubnetSpec{
						{
							Name:         "loadbalancers",
							Offset:       0,
							PrefixLength: 64,
						},
						{
							Name:         "services",
							Offset:       1,
							PrefixLength: 64,
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, dp)).Should(Succeed())

			// Create reconciler with injectable mock receiver
			reconciler := &DynamicPrefixReconciler{
				Client:    k8sClient,
				Scheme:    k8sClient.Scheme(),
				receivers: make(map[string]prefix.Receiver),
			}

			// Inject a mock receiver with a prefix
			mockReceiver := prefix.NewMockReceiver(prefix.SourceDHCPv6PD)
			mockPrefix := netip.MustParsePrefix("2001:db8::/48")
			mockReceiver.SimulatePrefix(mockPrefix, time.Hour)

			reconciler.receivers[dpName] = mockReceiver

			// Trigger reconcile
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name: dpName,
				},
			}

			// First reconcile adds finalizer
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeTrue())

			// Second reconcile processes the prefix
			result, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify status was updated
			var updatedDP dynamicprefixiov1alpha1.DynamicPrefix
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: dpName}, &updatedDP)
				return err == nil && updatedDP.Status.CurrentPrefix != ""
			}, timeout, interval).Should(BeTrue())

			Expect(updatedDP.Status.CurrentPrefix).To(Equal("2001:db8::/48"))
			Expect(updatedDP.Status.PrefixSource).To(Equal(dynamicprefixiov1alpha1.PrefixSourceDHCPv6PD))
			Expect(updatedDP.Status.Subnets).To(HaveLen(2))
			Expect(updatedDP.Status.Subnets[0].Name).To(Equal("loadbalancers"))
			Expect(updatedDP.Status.Subnets[0].CIDR).To(Equal("2001:db8::/64"))
			Expect(updatedDP.Status.Subnets[1].Name).To(Equal("services"))
			Expect(updatedDP.Status.Subnets[1].CIDR).To(Equal("2001:db8:0:1::/64"))

			// Cleanup
			Expect(k8sClient.Delete(ctx, dp)).Should(Succeed())
		})
	})

	Context("When prefix changes", func() {
		It("Should update subnets and add to history", func() {
			ctx := context.Background()

			dpName := "test-dp-change"
			dp := &dynamicprefixiov1alpha1.DynamicPrefix{
				ObjectMeta: metav1.ObjectMeta{
					Name: dpName,
				},
				Spec: dynamicprefixiov1alpha1.DynamicPrefixSpec{
					Acquisition: dynamicprefixiov1alpha1.AcquisitionSpec{
						DHCPv6PD: &dynamicprefixiov1alpha1.DHCPv6PDSpec{
							Interface: "eth0",
						},
					},
					Subnets: []dynamicprefixiov1alpha1.SubnetSpec{
						{
							Name:         "services",
							Offset:       0,
							PrefixLength: 64,
						},
					},
					Transition: &dynamicprefixiov1alpha1.TransitionSpec{
						MaxPrefixHistory: 3,
					},
				},
			}

			Expect(k8sClient.Create(ctx, dp)).Should(Succeed())

			reconciler := &DynamicPrefixReconciler{
				Client:    k8sClient,
				Scheme:    k8sClient.Scheme(),
				receivers: make(map[string]prefix.Receiver),
			}

			// Start with first prefix
			mockReceiver := prefix.NewMockReceiver(prefix.SourceDHCPv6PD)
			prefix1 := netip.MustParsePrefix("2001:db8:1::/48")
			mockReceiver.SimulatePrefix(prefix1, time.Hour)
			reconciler.receivers[dpName] = mockReceiver

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{Name: dpName},
			}

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, req)
			// Process first prefix
			_, _ = reconciler.Reconcile(ctx, req)

			// Verify first prefix
			var updatedDP dynamicprefixiov1alpha1.DynamicPrefix
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: dpName}, &updatedDP)).Should(Succeed())
			Expect(updatedDP.Status.CurrentPrefix).To(Equal("2001:db8:1::/48"))
			Expect(updatedDP.Status.History).To(BeEmpty())

			// Simulate prefix change
			prefix2 := netip.MustParsePrefix("2001:db8:2::/48")
			mockReceiver.SimulatePrefix(prefix2, time.Hour)
			<-mockReceiver.Events() // drain the event

			// Reconcile with new prefix
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify new prefix and history
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: dpName}, &updatedDP)).Should(Succeed())
			Expect(updatedDP.Status.CurrentPrefix).To(Equal("2001:db8:2::/48"))
			Expect(updatedDP.Status.Subnets[0].CIDR).To(Equal("2001:db8:2::/64"))
			Expect(updatedDP.Status.History).To(HaveLen(1))
			Expect(updatedDP.Status.History[0].Prefix).To(Equal("2001:db8:1::/48"))
			Expect(updatedDP.Status.History[0].State).To(Equal(dynamicprefixiov1alpha1.PrefixStateDraining))

			// Cleanup
			Expect(k8sClient.Delete(ctx, dp)).Should(Succeed())
		})
	})

	Context("When deleting a DynamicPrefix", func() {
		It("Should remove finalizer and cleanup receiver", func() {
			ctx := context.Background()

			dpName := "test-dp-delete"
			dp := &dynamicprefixiov1alpha1.DynamicPrefix{
				ObjectMeta: metav1.ObjectMeta{
					Name: dpName,
				},
				Spec: dynamicprefixiov1alpha1.DynamicPrefixSpec{
					Acquisition: dynamicprefixiov1alpha1.AcquisitionSpec{
						DHCPv6PD: &dynamicprefixiov1alpha1.DHCPv6PDSpec{
							Interface: "eth0",
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, dp)).Should(Succeed())

			reconciler := &DynamicPrefixReconciler{
				Client:    k8sClient,
				Scheme:    k8sClient.Scheme(),
				receivers: make(map[string]prefix.Receiver),
			}

			mockReceiver := prefix.NewMockReceiver(prefix.SourceDHCPv6PD)
			_ = mockReceiver.Start(ctx)
			reconciler.receivers[dpName] = mockReceiver

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{Name: dpName},
			}

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, req)

			// Delete the resource
			Expect(k8sClient.Delete(ctx, dp)).Should(Succeed())

			// Reconcile should handle deletion
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify receiver was cleaned up
			Expect(mockReceiver.IsStarted()).To(BeFalse())
			Expect(reconciler.receivers).NotTo(HaveKey(dpName))
		})
	})

	Context("When no prefix is acquired yet", func() {
		It("Should set condition to WaitingForPrefix", func() {
			ctx := context.Background()

			dpName := "test-dp-waiting"
			dp := &dynamicprefixiov1alpha1.DynamicPrefix{
				ObjectMeta: metav1.ObjectMeta{
					Name: dpName,
				},
				Spec: dynamicprefixiov1alpha1.DynamicPrefixSpec{
					Acquisition: dynamicprefixiov1alpha1.AcquisitionSpec{
						DHCPv6PD: &dynamicprefixiov1alpha1.DHCPv6PDSpec{
							Interface: "eth0",
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, dp)).Should(Succeed())

			reconciler := &DynamicPrefixReconciler{
				Client:    k8sClient,
				Scheme:    k8sClient.Scheme(),
				receivers: make(map[string]prefix.Receiver),
			}

			// Create mock receiver without simulating a prefix
			mockReceiver := prefix.NewMockReceiver(prefix.SourceDHCPv6PD)
			reconciler.receivers[dpName] = mockReceiver

			req := reconcile.Request{
				NamespacedName: types.NamespacedName{Name: dpName},
			}

			// First reconcile adds finalizer
			_, _ = reconciler.Reconcile(ctx, req)

			// Second reconcile should see no prefix
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(10 * time.Second))

			// Verify condition
			var updatedDP dynamicprefixiov1alpha1.DynamicPrefix
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: dpName}, &updatedDP)).Should(Succeed())

			var foundCondition bool
			for _, c := range updatedDP.Status.Conditions {
				if c.Type == dynamicprefixiov1alpha1.ConditionTypePrefixAcquired {
					foundCondition = true
					Expect(c.Status).To(Equal(metav1.ConditionFalse))
					Expect(c.Reason).To(Equal("WaitingForPrefix"))
				}
			}
			Expect(foundCondition).To(BeTrue())

			// Cleanup
			Expect(k8sClient.Delete(ctx, dp)).Should(Succeed())
		})
	})
})
