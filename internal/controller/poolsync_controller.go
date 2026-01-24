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
	"fmt"
	"net/netip"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dynamicprefixiov1alpha1 "github.com/jr42/dynamic-prefix-operator/api/v1alpha1"
	"github.com/jr42/dynamic-prefix-operator/internal/prefix"
)

const (
	// AnnotationName references the DynamicPrefix CR name.
	AnnotationName = "dynamic-prefix.io/name"
	// AnnotationSubnet specifies which subnet from status.subnets to use (Mode 2).
	AnnotationSubnet = "dynamic-prefix.io/subnet"
	// AnnotationAddressRange specifies which address range from status.addressRanges to use (Mode 1).
	AnnotationAddressRange = "dynamic-prefix.io/address-range"
	// AnnotationLastSync is the timestamp set by operator after update.
	AnnotationLastSync = "dynamic-prefix.io/last-sync"
)

var (
	// CiliumLBIPPoolGVK is the GroupVersionKind for CiliumLoadBalancerIPPool.
	CiliumLBIPPoolGVK = schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2alpha1",
		Kind:    "CiliumLoadBalancerIPPool",
	}

	// CiliumCIDRGroupGVK is the GroupVersionKind for CiliumCIDRGroup.
	CiliumCIDRGroupGVK = schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2alpha1",
		Kind:    "CiliumCIDRGroup",
	}
)

// poolConfiguration holds the resolved configuration for a pool update.
type poolConfiguration struct {
	// useAddressRange indicates whether to use start/end addresses (true) or CIDR (false).
	useAddressRange bool
	// start is the first address in the range (Mode 1 only).
	start string
	// end is the last address in the range (Mode 1 only).
	end string
	// cidr is the CIDR notation (Mode 2 or fallback).
	cidr string
}

// PoolSyncReconciler reconciles Cilium pool resources annotated with dynamic-prefix.io annotations.
type PoolSyncReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=cilium.io,resources=ciliumloadbalancerippools,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=cilium.io,resources=ciliumcidrgroups,verbs=get;list;watch;update;patch

// Reconcile handles pool synchronization for annotated Cilium resources.
func (r *PoolSyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Determine resource type from request
	// Try to fetch as CiliumLoadBalancerIPPool first
	pool := &unstructured.Unstructured{}
	pool.SetGroupVersionKind(CiliumLBIPPoolGVK)

	if err := r.Get(ctx, req.NamespacedName, pool); err != nil {
		// Try CiliumCIDRGroup
		pool = &unstructured.Unstructured{}
		pool.SetGroupVersionKind(CiliumCIDRGroupGVK)
		if err := r.Get(ctx, req.NamespacedName, pool); err != nil {
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}

	// Get annotations
	annotations := pool.GetAnnotations()
	if annotations == nil {
		return ctrl.Result{}, nil
	}

	dpName, hasName := annotations[AnnotationName]
	subnetName, hasSubnet := annotations[AnnotationSubnet]
	addressRangeName, hasAddressRange := annotations[AnnotationAddressRange]

	if !hasName {
		// No dynamic-prefix.io/name annotation, nothing to do
		return ctrl.Result{}, nil
	}

	log.Info("Syncing pool", "pool", req.Name, "dynamicPrefix", dpName, "subnet", subnetName, "addressRange", addressRangeName)

	// Fetch the referenced DynamicPrefix
	var dp dynamicprefixiov1alpha1.DynamicPrefix
	if err := r.Get(ctx, types.NamespacedName{Name: dpName}, &dp); err != nil {
		log.Error(err, "Failed to get DynamicPrefix", "name", dpName)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Build pool configurations for current prefix and historical prefixes
	configs, err := r.buildPoolConfigurations(ctx, &dp, hasAddressRange, addressRangeName, hasSubnet, subnetName)
	if err != nil {
		log.Info("Failed to build pool configurations", "error", err.Error())
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	if len(configs) == 0 {
		log.Info("No pool configurations generated")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Update the pool based on its type
	gvk := pool.GetObjectKind().GroupVersionKind()
	var updateErr error

	switch gvk.Kind {
	case "CiliumLoadBalancerIPPool":
		updateErr = r.updateLoadBalancerIPPool(ctx, pool, configs)
	case "CiliumCIDRGroup":
		// CIDRGroup doesn't support start/end ranges, use CIDR only
		updateErr = r.updateCIDRGroup(ctx, pool, configs)
	default:
		log.Info("Unknown pool type", "kind", gvk.Kind)
		return ctrl.Result{}, nil
	}

	if updateErr != nil {
		log.Error(updateErr, "Failed to update pool")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	log.Info("Pool synced successfully", "pool", req.Name, "blockCount", len(configs))
	return ctrl.Result{}, nil
}

// buildPoolConfigurations builds pool configurations for current prefix and historical prefixes.
func (r *PoolSyncReconciler) buildPoolConfigurations(
	ctx context.Context,
	dp *dynamicprefixiov1alpha1.DynamicPrefix,
	hasAddressRange bool,
	addressRangeName string,
	hasSubnet bool,
	subnetName string,
) ([]poolConfiguration, error) {
	if dp.Status.CurrentPrefix == "" {
		return nil, fmt.Errorf("DynamicPrefix has no current prefix")
	}

	maxHistory := r.getMaxHistory(dp)

	if hasAddressRange && addressRangeName != "" {
		return r.buildAddressRangeConfigs(ctx, dp, addressRangeName, maxHistory)
	}

	if hasSubnet && subnetName != "" {
		return r.buildSubnetConfigs(ctx, dp, subnetName, maxHistory)
	}

	return r.buildRawPrefixConfigs(dp, maxHistory), nil
}

// getMaxHistory returns the maximum number of historical prefixes to retain.
func (r *PoolSyncReconciler) getMaxHistory(dp *dynamicprefixiov1alpha1.DynamicPrefix) int {
	if dp.Spec.Transition != nil && dp.Spec.Transition.MaxPrefixHistory > 0 {
		return dp.Spec.Transition.MaxPrefixHistory
	}
	return 2 // Default
}

// buildAddressRangeConfigs builds configurations for address range mode.
func (r *PoolSyncReconciler) buildAddressRangeConfigs(
	ctx context.Context,
	dp *dynamicprefixiov1alpha1.DynamicPrefix,
	addressRangeName string,
	maxHistory int,
) ([]poolConfiguration, error) {
	log := logf.FromContext(ctx)
	var configs []poolConfiguration

	// Find the address range spec
	rangeSpec := r.findAddressRangeSpec(dp, addressRangeName)

	// Get current config from status or calculate from spec
	currentConfig := r.findAddressRangeInStatus(dp, addressRangeName)
	if currentConfig == nil {
		if rangeSpec == nil {
			return nil, fmt.Errorf("address range %q not found in status or spec", addressRangeName)
		}
		calculated, err := r.calculateAddressRangeConfig(dp.Status.CurrentPrefix, rangeSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate address range for current prefix: %w", err)
		}
		currentConfig = &calculated
	}
	configs = append(configs, *currentConfig)

	// Calculate for historical prefixes
	if rangeSpec != nil {
		for i, histEntry := range dp.Status.History {
			if i >= maxHistory {
				break
			}
			histConfig, err := r.calculateAddressRangeConfig(histEntry.Prefix, rangeSpec)
			if err != nil {
				log.V(1).Info("Failed to calculate address range for historical prefix",
					"prefix", histEntry.Prefix, "error", err.Error())
				continue
			}
			configs = append(configs, histConfig)
		}
	}

	return configs, nil
}

// buildSubnetConfigs builds configurations for subnet mode.
func (r *PoolSyncReconciler) buildSubnetConfigs(
	ctx context.Context,
	dp *dynamicprefixiov1alpha1.DynamicPrefix,
	subnetName string,
	maxHistory int,
) ([]poolConfiguration, error) {
	log := logf.FromContext(ctx)
	var configs []poolConfiguration

	// Find the subnet spec
	subnetSpec := r.findSubnetSpec(dp, subnetName)

	// Get current config from status or calculate from spec
	currentConfig := r.findSubnetInStatus(dp, subnetName)
	if currentConfig == nil {
		if subnetSpec == nil {
			return nil, fmt.Errorf("subnet %q not found in status or spec", subnetName)
		}
		calculated, err := r.calculateSubnetConfig(dp.Status.CurrentPrefix, subnetSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate subnet for current prefix: %w", err)
		}
		currentConfig = &calculated
	}
	configs = append(configs, *currentConfig)

	// Calculate for historical prefixes
	if subnetSpec != nil {
		for i, histEntry := range dp.Status.History {
			if i >= maxHistory {
				break
			}
			histConfig, err := r.calculateSubnetConfig(histEntry.Prefix, subnetSpec)
			if err != nil {
				log.V(1).Info("Failed to calculate subnet for historical prefix",
					"prefix", histEntry.Prefix, "error", err.Error())
				continue
			}
			configs = append(configs, histConfig)
		}
	}

	return configs, nil
}

// buildRawPrefixConfigs builds configurations using raw prefixes (no address range or subnet).
func (r *PoolSyncReconciler) buildRawPrefixConfigs(
	dp *dynamicprefixiov1alpha1.DynamicPrefix,
	maxHistory int,
) []poolConfiguration {
	configs := []poolConfiguration{{
		useAddressRange: false,
		cidr:            dp.Status.CurrentPrefix,
	}}

	for i, histEntry := range dp.Status.History {
		if i >= maxHistory {
			break
		}
		configs = append(configs, poolConfiguration{
			useAddressRange: false,
			cidr:            histEntry.Prefix,
		})
	}

	return configs
}

// findAddressRangeSpec finds an address range spec by name.
func (r *PoolSyncReconciler) findAddressRangeSpec(
	dp *dynamicprefixiov1alpha1.DynamicPrefix,
	name string,
) *dynamicprefixiov1alpha1.AddressRangeSpec {
	for i := range dp.Spec.AddressRanges {
		if dp.Spec.AddressRanges[i].Name == name {
			return &dp.Spec.AddressRanges[i]
		}
	}
	return nil
}

// findAddressRangeInStatus finds an address range in status by name.
func (r *PoolSyncReconciler) findAddressRangeInStatus(
	dp *dynamicprefixiov1alpha1.DynamicPrefix,
	name string,
) *poolConfiguration {
	for _, ar := range dp.Status.AddressRanges {
		if ar.Name == name {
			return &poolConfiguration{
				useAddressRange: true,
				start:           ar.Start,
				end:             ar.End,
				cidr:            ar.CIDR,
			}
		}
	}
	return nil
}

// findSubnetSpec finds a subnet spec by name.
func (r *PoolSyncReconciler) findSubnetSpec(
	dp *dynamicprefixiov1alpha1.DynamicPrefix,
	name string,
) *dynamicprefixiov1alpha1.SubnetSpec {
	for i := range dp.Spec.Subnets {
		if dp.Spec.Subnets[i].Name == name {
			return &dp.Spec.Subnets[i]
		}
	}
	return nil
}

// findSubnetInStatus finds a subnet in status by name.
func (r *PoolSyncReconciler) findSubnetInStatus(
	dp *dynamicprefixiov1alpha1.DynamicPrefix,
	name string,
) *poolConfiguration {
	for _, s := range dp.Status.Subnets {
		if s.Name == name {
			return &poolConfiguration{
				useAddressRange: false,
				cidr:            s.CIDR,
			}
		}
	}
	return nil
}

// calculateAddressRangeConfig calculates a pool configuration from a prefix and address range spec.
func (r *PoolSyncReconciler) calculateAddressRangeConfig(
	prefixStr string,
	rangeSpec *dynamicprefixiov1alpha1.AddressRangeSpec,
) (poolConfiguration, error) {
	basePrefix, err := netip.ParsePrefix(prefixStr)
	if err != nil {
		return poolConfiguration{}, fmt.Errorf("invalid prefix %q: %w", prefixStr, err)
	}

	cfg := prefix.AddressRangeConfig{
		Name:  rangeSpec.Name,
		Start: rangeSpec.Start,
		End:   rangeSpec.End,
	}

	ar, err := prefix.CalculateAddressRange(basePrefix, cfg)
	if err != nil {
		return poolConfiguration{}, err
	}

	return poolConfiguration{
		useAddressRange: true,
		start:           ar.Start.String(),
		end:             ar.End.String(),
		cidr:            prefix.RangeToCIDR(ar.Start, ar.End).String(),
	}, nil
}

// calculateSubnetConfig calculates a pool configuration from a prefix and subnet spec.
func (r *PoolSyncReconciler) calculateSubnetConfig(
	prefixStr string,
	subnetSpec *dynamicprefixiov1alpha1.SubnetSpec,
) (poolConfiguration, error) {
	basePrefix, err := netip.ParsePrefix(prefixStr)
	if err != nil {
		return poolConfiguration{}, fmt.Errorf("invalid prefix %q: %w", prefixStr, err)
	}

	cfg := prefix.SubnetConfig{
		Name:         subnetSpec.Name,
		Offset:       subnetSpec.Offset,
		PrefixLength: subnetSpec.PrefixLength,
	}

	subnet, err := prefix.CalculateSubnet(basePrefix, cfg)
	if err != nil {
		return poolConfiguration{}, err
	}

	return poolConfiguration{
		useAddressRange: false,
		cidr:            subnet.CIDR.String(),
	}, nil
}

// updateLoadBalancerIPPool updates a CiliumLoadBalancerIPPool with the new configurations.
// It supports both CIDR-based blocks (Mode 2) and start/end address ranges (Mode 1).
// Multiple blocks are created for current prefix plus historical prefixes.
func (r *PoolSyncReconciler) updateLoadBalancerIPPool(ctx context.Context, pool *unstructured.Unstructured, configs []poolConfiguration) error {
	// CiliumLoadBalancerIPPool spec.blocks is a list of IP blocks
	// Format can be either:
	// - spec.blocks[].cidr for CIDR-based allocation
	// - spec.blocks[].start + spec.blocks[].stop for address range (Cilium uses "stop" not "end")
	blocks := make([]interface{}, 0, len(configs))

	for _, config := range configs {
		var block map[string]interface{}
		if config.useAddressRange && config.start != "" && config.end != "" {
			// Use start/stop for precise address range (Mode 1)
			block = map[string]interface{}{
				"start": config.start,
				"stop":  config.end,
			}
		} else {
			// Use CIDR (Mode 2 or fallback)
			block = map[string]interface{}{
				"cidr": config.cidr,
			}
		}
		blocks = append(blocks, block)
	}

	if err := unstructured.SetNestedField(pool.Object, blocks, "spec", "blocks"); err != nil {
		return fmt.Errorf("failed to set spec.blocks: %w", err)
	}

	// Update last-sync annotation
	r.setLastSyncAnnotation(pool)

	return r.Update(ctx, pool)
}

// updateCIDRGroup updates a CiliumCIDRGroup with the new CIDRs.
// Multiple CIDRs are added for current prefix plus historical prefixes.
func (r *PoolSyncReconciler) updateCIDRGroup(ctx context.Context, pool *unstructured.Unstructured, configs []poolConfiguration) error {
	// CiliumCIDRGroup spec.externalCIDRs is a list of CIDR strings
	externalCIDRs := make([]interface{}, 0, len(configs))

	for _, config := range configs {
		externalCIDRs = append(externalCIDRs, config.cidr)
	}

	if err := unstructured.SetNestedField(pool.Object, externalCIDRs, "spec", "externalCIDRs"); err != nil {
		return fmt.Errorf("failed to set spec.externalCIDRs: %w", err)
	}

	// Update last-sync annotation
	r.setLastSyncAnnotation(pool)

	return r.Update(ctx, pool)
}

// setLastSyncAnnotation sets the last-sync annotation to the current timestamp.
func (r *PoolSyncReconciler) setLastSyncAnnotation(pool *unstructured.Unstructured) {
	annotations := pool.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[AnnotationLastSync] = time.Now().UTC().Format(time.RFC3339)
	pool.SetAnnotations(annotations)
}

// SetupWithManager sets up the controller with the Manager.
func (r *PoolSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create predicate for resources with dynamic-prefix.io/name annotation
	hasAnnotation := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		annotations := obj.GetAnnotations()
		if annotations == nil {
			return false
		}
		_, ok := annotations[AnnotationName]
		return ok
	})

	// Watch CiliumLoadBalancerIPPool
	lbIPPool := &unstructured.Unstructured{}
	lbIPPool.SetGroupVersionKind(CiliumLBIPPoolGVK)

	// Watch CiliumCIDRGroup
	cidrGroup := &unstructured.Unstructured{}
	cidrGroup.SetGroupVersionKind(CiliumCIDRGroupGVK)

	// Build controller
	controllerBuilder := ctrl.NewControllerManagedBy(mgr).
		Named("poolsync")

	// Add watch for CiliumLoadBalancerIPPool (if CRD exists)
	controllerBuilder = controllerBuilder.
		For(lbIPPool, builder.WithPredicates(hasAnnotation))

	// Add watch for CiliumCIDRGroup
	controllerBuilder = controllerBuilder.
		Watches(cidrGroup, &handler.EnqueueRequestForObject{}, builder.WithPredicates(hasAnnotation))

	// Watch DynamicPrefix and enqueue referencing pools
	controllerBuilder = controllerBuilder.
		Watches(&dynamicprefixiov1alpha1.DynamicPrefix{}, handler.EnqueueRequestsFromMapFunc(r.findReferencingPools))

	return controllerBuilder.Complete(r)
}

// findReferencingPools finds all pools that reference the given DynamicPrefix.
func (r *PoolSyncReconciler) findReferencingPools(ctx context.Context, obj client.Object) []reconcile.Request {
	dp, ok := obj.(*dynamicprefixiov1alpha1.DynamicPrefix)
	if !ok {
		return nil
	}

	log := logf.FromContext(ctx)
	var requests []reconcile.Request

	// List CiliumLoadBalancerIPPools
	lbPoolList := &unstructured.UnstructuredList{}
	lbPoolList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2alpha1",
		Kind:    "CiliumLoadBalancerIPPoolList",
	})

	if err := r.List(ctx, lbPoolList); err == nil {
		for _, pool := range lbPoolList.Items {
			if annotations := pool.GetAnnotations(); annotations != nil {
				if annotations[AnnotationName] == dp.Name {
					requests = append(requests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      pool.GetName(),
							Namespace: pool.GetNamespace(),
						},
					})
				}
			}
		}
	} else {
		log.V(1).Info("Failed to list CiliumLoadBalancerIPPools", "error", err)
	}

	// List CiliumCIDRGroups
	cidrGroupList := &unstructured.UnstructuredList{}
	cidrGroupList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2alpha1",
		Kind:    "CiliumCIDRGroupList",
	})

	if err := r.List(ctx, cidrGroupList); err == nil {
		for _, group := range cidrGroupList.Items {
			if annotations := group.GetAnnotations(); annotations != nil {
				if annotations[AnnotationName] == dp.Name {
					requests = append(requests, reconcile.Request{
						NamespacedName: types.NamespacedName{
							Name:      group.GetName(),
							Namespace: group.GetNamespace(),
						},
					})
				}
			}
		}
	} else {
		log.V(1).Info("Failed to list CiliumCIDRGroups", "error", err)
	}

	if len(requests) > 0 {
		log.Info("DynamicPrefix changed, enqueuing referencing pools", "dynamicPrefix", dp.Name, "poolCount", len(requests))
	}

	return requests
}
