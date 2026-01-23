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
)

const (
	// AnnotationName references the DynamicPrefix CR name.
	AnnotationName = "dynamic-prefix.io/name"
	// AnnotationSubnet specifies which subnet from status.subnets to use.
	AnnotationSubnet = "dynamic-prefix.io/subnet"
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

	if !hasName {
		// No dynamic-prefix.io/name annotation, nothing to do
		return ctrl.Result{}, nil
	}

	log.Info("Syncing pool", "pool", req.Name, "dynamicPrefix", dpName, "subnet", subnetName)

	// Fetch the referenced DynamicPrefix
	var dp dynamicprefixiov1alpha1.DynamicPrefix
	if err := r.Get(ctx, types.NamespacedName{Name: dpName}, &dp); err != nil {
		log.Error(err, "Failed to get DynamicPrefix", "name", dpName)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Find the subnet CIDR
	var cidr string
	if hasSubnet && subnetName != "" {
		// Look for specific subnet
		for _, s := range dp.Status.Subnets {
			if s.Name == subnetName {
				cidr = s.CIDR
				break
			}
		}
		if cidr == "" {
			log.Info("Subnet not found in DynamicPrefix status", "subnet", subnetName)
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
	} else {
		// Use the main prefix
		if dp.Status.CurrentPrefix == "" {
			log.Info("DynamicPrefix has no current prefix")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		cidr = dp.Status.CurrentPrefix
	}

	// Update the pool based on its type
	gvk := pool.GetObjectKind().GroupVersionKind()
	var updateErr error

	switch gvk.Kind {
	case "CiliumLoadBalancerIPPool":
		updateErr = r.updateLoadBalancerIPPool(ctx, pool, cidr)
	case "CiliumCIDRGroup":
		updateErr = r.updateCIDRGroup(ctx, pool, cidr)
	default:
		log.Info("Unknown pool type", "kind", gvk.Kind)
		return ctrl.Result{}, nil
	}

	if updateErr != nil {
		log.Error(updateErr, "Failed to update pool")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	log.Info("Pool synced successfully", "pool", req.Name, "cidr", cidr)
	return ctrl.Result{}, nil
}

// updateLoadBalancerIPPool updates a CiliumLoadBalancerIPPool with the new CIDR.
func (r *PoolSyncReconciler) updateLoadBalancerIPPool(ctx context.Context, pool *unstructured.Unstructured, cidr string) error {
	// CiliumLoadBalancerIPPool spec.blocks is a list of CIDR blocks
	// Format: spec.blocks[].cidr
	blocks := []interface{}{
		map[string]interface{}{
			"cidr": cidr,
		},
	}

	if err := unstructured.SetNestedField(pool.Object, blocks, "spec", "blocks"); err != nil {
		return fmt.Errorf("failed to set spec.blocks: %w", err)
	}

	// Update last-sync annotation
	r.setLastSyncAnnotation(pool)

	return r.Update(ctx, pool)
}

// updateCIDRGroup updates a CiliumCIDRGroup with the new CIDR.
func (r *PoolSyncReconciler) updateCIDRGroup(ctx context.Context, pool *unstructured.Unstructured, cidr string) error {
	// CiliumCIDRGroup spec.externalCIDRs is a list of CIDR strings
	externalCIDRs := []interface{}{cidr}

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
