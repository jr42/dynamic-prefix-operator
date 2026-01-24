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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dynamicprefixiov1alpha1 "github.com/jr42/dynamic-prefix-operator/api/v1alpha1"
)

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = dynamicprefixiov1alpha1.AddToScheme(scheme)
	// Register unstructured types for Cilium resources
	scheme.AddKnownTypeWithName(CiliumBGPAdvertisementGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(CiliumLBIPPoolGVK, &unstructured.Unstructured{})
	return scheme
}

func TestBGPSyncReconciler_Reconcile_CreateAdvertisement(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Create DynamicPrefix with BGP-enabled subnet
	dp := &dynamicprefixiov1alpha1.DynamicPrefix{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-dp",
			UID:  "test-uid-123",
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
					Name:         "loadbalancers",
					Offset:       0,
					PrefixLength: 64,
					BGP: &dynamicprefixiov1alpha1.SubnetBGPSpec{
						Advertise: true,
						Community: "65001:42",
					},
				},
			},
		},
		Status: dynamicprefixiov1alpha1.DynamicPrefixStatus{
			CurrentPrefix: "2001:db8::/48",
			Subnets: []dynamicprefixiov1alpha1.SubnetStatus{
				{
					Name: "loadbalancers",
					CIDR: "2001:db8::/64",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(dp).
		WithStatusSubresource(dp).
		Build()

	reconciler := &BGPSyncReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	// Reconcile
	_, err := reconciler.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-dp"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Verify CiliumBGPAdvertisement was created
	advName := "dp-test-dp-loadbalancers"
	adv := &unstructured.Unstructured{}
	adv.SetGroupVersionKind(CiliumBGPAdvertisementGVK)
	err = fakeClient.Get(ctx, types.NamespacedName{Name: advName}, adv)
	if err != nil {
		t.Fatalf("Failed to get CiliumBGPAdvertisement: %v", err)
	}

	// Check labels
	labels := adv.GetLabels()
	if labels[LabelManagedBy] != LabelManagedByValue {
		t.Errorf("Label %s = %q, want %q", LabelManagedBy, labels[LabelManagedBy], LabelManagedByValue)
	}
	if labels[LabelDynamicPrefixName] != "test-dp" {
		t.Errorf("Label %s = %q, want %q", LabelDynamicPrefixName, labels[LabelDynamicPrefixName], "test-dp")
	}
	if labels[LabelSubnetName] != "loadbalancers" {
		t.Errorf("Label %s = %q, want %q", LabelSubnetName, labels[LabelSubnetName], "loadbalancers")
	}

	// Check spec
	advertisements, found, err := unstructured.NestedSlice(adv.Object, "spec", "advertisements")
	if err != nil || !found {
		t.Fatalf("Failed to get spec.advertisements: found=%v, err=%v", found, err)
	}
	if len(advertisements) != 1 {
		t.Fatalf("Expected 1 advertisement, got %d", len(advertisements))
	}

	advSpec := advertisements[0].(map[string]interface{})
	if advSpec["advertisementType"] != "Service" {
		t.Errorf("advertisementType = %v, want Service", advSpec["advertisementType"])
	}

	// Check community
	communities, found, err := unstructured.NestedStringSlice(advSpec, "attributes", "communities", "standard")
	if err != nil || !found {
		t.Fatalf("Failed to get communities: found=%v, err=%v", found, err)
	}
	if len(communities) != 1 || communities[0] != "65001:42" {
		t.Errorf("communities = %v, want [65001:42]", communities)
	}
}

func TestBGPSyncReconciler_Reconcile_UpdateStatus(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Create DynamicPrefix with BGP-enabled subnet
	dp := &dynamicprefixiov1alpha1.DynamicPrefix{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-dp-status",
			UID:  "test-uid-456",
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
					Name:         "lb",
					Offset:       0,
					PrefixLength: 64,
					BGP: &dynamicprefixiov1alpha1.SubnetBGPSpec{
						Advertise: true,
					},
				},
			},
		},
		Status: dynamicprefixiov1alpha1.DynamicPrefixStatus{
			CurrentPrefix: "2001:db8::/48",
			Subnets: []dynamicprefixiov1alpha1.SubnetStatus{
				{
					Name: "lb",
					CIDR: "2001:db8::/64",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(dp).
		WithStatusSubresource(dp).
		Build()

	reconciler := &BGPSyncReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	// Reconcile
	_, err := reconciler.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-dp-status"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Fetch updated DynamicPrefix
	var updatedDP dynamicprefixiov1alpha1.DynamicPrefix
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "test-dp-status"}, &updatedDP)
	if err != nil {
		t.Fatalf("Failed to get updated DynamicPrefix: %v", err)
	}

	// Check status
	if len(updatedDP.Status.Subnets) != 1 {
		t.Fatalf("Expected 1 subnet in status, got %d", len(updatedDP.Status.Subnets))
	}
	expectedAdvName := "dp-test-dp-status-lb"
	if updatedDP.Status.Subnets[0].BGPAdvertisement != expectedAdvName {
		t.Errorf("BGPAdvertisement = %q, want %q", updatedDP.Status.Subnets[0].BGPAdvertisement, expectedAdvName)
	}

	// Check condition
	var bgpCondition *metav1.Condition
	for i := range updatedDP.Status.Conditions {
		if updatedDP.Status.Conditions[i].Type == dynamicprefixiov1alpha1.ConditionTypeBGPAdvertisementReady {
			bgpCondition = &updatedDP.Status.Conditions[i]
			break
		}
	}
	if bgpCondition == nil {
		t.Fatal("BGPAdvertisementReady condition not found")
	}
	if bgpCondition.Status != metav1.ConditionTrue {
		t.Errorf("BGPAdvertisementReady status = %v, want True", bgpCondition.Status)
	}
	if bgpCondition.Reason != "AdvertisementsReady" {
		t.Errorf("BGPAdvertisementReady reason = %q, want AdvertisementsReady", bgpCondition.Reason)
	}
}

func TestBGPSyncReconciler_Reconcile_NoBGPSubnets(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Create DynamicPrefix without BGP
	dp := &dynamicprefixiov1alpha1.DynamicPrefix{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-dp-no-bgp",
			UID:  "test-uid-789",
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
					Name:         "no-bgp",
					Offset:       0,
					PrefixLength: 64,
					// No BGP spec
				},
			},
		},
		Status: dynamicprefixiov1alpha1.DynamicPrefixStatus{
			CurrentPrefix: "2001:db8::/48",
			Subnets: []dynamicprefixiov1alpha1.SubnetStatus{
				{
					Name: "no-bgp",
					CIDR: "2001:db8::/64",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(dp).
		WithStatusSubresource(dp).
		Build()

	reconciler := &BGPSyncReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	// Reconcile
	_, err := reconciler.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-dp-no-bgp"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Fetch updated DynamicPrefix
	var updatedDP dynamicprefixiov1alpha1.DynamicPrefix
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "test-dp-no-bgp"}, &updatedDP)
	if err != nil {
		t.Fatalf("Failed to get updated DynamicPrefix: %v", err)
	}

	// Check condition
	var bgpCondition *metav1.Condition
	for i := range updatedDP.Status.Conditions {
		if updatedDP.Status.Conditions[i].Type == dynamicprefixiov1alpha1.ConditionTypeBGPAdvertisementReady {
			bgpCondition = &updatedDP.Status.Conditions[i]
			break
		}
	}
	if bgpCondition == nil {
		t.Fatal("BGPAdvertisementReady condition not found")
	}
	if bgpCondition.Status != metav1.ConditionFalse {
		t.Errorf("BGPAdvertisementReady status = %v, want False", bgpCondition.Status)
	}
	if bgpCondition.Reason != "NoBGPSubnets" {
		t.Errorf("BGPAdvertisementReady reason = %q, want NoBGPSubnets", bgpCondition.Reason)
	}
}

func TestBGPSyncReconciler_Reconcile_DeleteOrphaned(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Create DynamicPrefix with BGP disabled
	dp := &dynamicprefixiov1alpha1.DynamicPrefix{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-dp-orphan",
			UID:  "test-uid-orphan",
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
					Name:         "was-bgp",
					Offset:       0,
					PrefixLength: 64,
					BGP: &dynamicprefixiov1alpha1.SubnetBGPSpec{
						Advertise: false, // BGP now disabled
					},
				},
			},
		},
		Status: dynamicprefixiov1alpha1.DynamicPrefixStatus{
			CurrentPrefix: "2001:db8::/48",
			Subnets: []dynamicprefixiov1alpha1.SubnetStatus{
				{
					Name:             "was-bgp",
					CIDR:             "2001:db8::/64",
					BGPAdvertisement: "dp-test-dp-orphan-was-bgp", // Old advertisement
				},
			},
		},
	}

	// Create an orphaned advertisement (from when BGP was enabled)
	orphanedAdv := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cilium.io/v2alpha1",
			"kind":       "CiliumBGPAdvertisement",
			"metadata": map[string]interface{}{
				"name": "dp-test-dp-orphan-was-bgp",
				"labels": map[string]interface{}{
					LabelManagedBy:         LabelManagedByValue,
					LabelDynamicPrefixName: "test-dp-orphan",
					LabelSubnetName:        "was-bgp",
				},
			},
			"spec": map[string]interface{}{
				"advertisements": []interface{}{
					map[string]interface{}{
						"advertisementType": "Service",
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(dp, orphanedAdv).
		WithStatusSubresource(dp).
		Build()

	reconciler := &BGPSyncReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	// Reconcile
	_, err := reconciler.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-dp-orphan"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Verify orphaned advertisement was deleted
	adv := &unstructured.Unstructured{}
	adv.SetGroupVersionKind(CiliumBGPAdvertisementGVK)
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "dp-test-dp-orphan-was-bgp"}, adv)
	if err == nil {
		t.Error("Expected orphaned advertisement to be deleted, but it still exists")
	} else if client.IgnoreNotFound(err) != nil {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestBGPSyncReconciler_Reconcile_WithPoolSelector(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()

	// Create DynamicPrefix with BGP-enabled subnet
	dp := &dynamicprefixiov1alpha1.DynamicPrefix{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-dp-selector",
			UID:  "test-uid-selector",
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
					Name:         "with-selector",
					Offset:       0,
					PrefixLength: 64,
					BGP: &dynamicprefixiov1alpha1.SubnetBGPSpec{
						Advertise: true,
					},
				},
			},
		},
		Status: dynamicprefixiov1alpha1.DynamicPrefixStatus{
			CurrentPrefix: "2001:db8::/48",
			Subnets: []dynamicprefixiov1alpha1.SubnetStatus{
				{
					Name: "with-selector",
					CIDR: "2001:db8::/64",
				},
			},
		},
	}

	// Create a CiliumLoadBalancerIPPool with serviceSelector
	pool := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cilium.io/v2alpha1",
			"kind":       "CiliumLoadBalancerIPPool",
			"metadata": map[string]interface{}{
				"name": "test-pool",
				"annotations": map[string]interface{}{
					AnnotationName:   "test-dp-selector",
					AnnotationSubnet: "with-selector",
				},
			},
			"spec": map[string]interface{}{
				"blocks": []interface{}{},
				"serviceSelector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"app": "nginx",
					},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(dp, pool).
		WithStatusSubresource(dp).
		Build()

	reconciler := &BGPSyncReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	// Reconcile
	_, err := reconciler.Reconcile(ctx, reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "test-dp-selector"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	// Verify CiliumBGPAdvertisement was created with selector
	advName := "dp-test-dp-selector-with-selector"
	adv := &unstructured.Unstructured{}
	adv.SetGroupVersionKind(CiliumBGPAdvertisementGVK)
	err = fakeClient.Get(ctx, types.NamespacedName{Name: advName}, adv)
	if err != nil {
		t.Fatalf("Failed to get CiliumBGPAdvertisement: %v", err)
	}

	// Check spec has selector
	advertisements, found, err := unstructured.NestedSlice(adv.Object, "spec", "advertisements")
	if err != nil || !found {
		t.Fatalf("Failed to get spec.advertisements: found=%v, err=%v", found, err)
	}
	if len(advertisements) != 1 {
		t.Fatalf("Expected 1 advertisement, got %d", len(advertisements))
	}

	advSpec := advertisements[0].(map[string]interface{})
	selector, found, err := unstructured.NestedMap(advSpec, "selector")
	if err != nil || !found {
		t.Fatalf("Failed to get selector: found=%v, err=%v", found, err)
	}
	matchLabels, found, err := unstructured.NestedStringMap(selector, "matchLabels")
	if err != nil || !found {
		t.Fatalf("Failed to get matchLabels: found=%v, err=%v", found, err)
	}
	if matchLabels["app"] != "nginx" {
		t.Errorf("matchLabels[app] = %q, want nginx", matchLabels["app"])
	}
}

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
