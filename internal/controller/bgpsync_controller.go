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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dynamicprefixiov1alpha1 "github.com/jr42/dynamic-prefix-operator/api/v1alpha1"
)

var (
	// CiliumBGPAdvertisementGVK is the GroupVersionKind for CiliumBGPAdvertisement.
	CiliumBGPAdvertisementGVK = schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2alpha1",
		Kind:    "CiliumBGPAdvertisement",
	}
)

const (
	// LabelManagedBy identifies resources managed by this operator.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	// LabelManagedByValue is the value for the managed-by label.
	LabelManagedByValue = "dynamic-prefix-operator"
	// LabelDynamicPrefixName references the DynamicPrefix CR name.
	LabelDynamicPrefixName = "dynamic-prefix.io/name"
	// LabelSubnetName references the subnet name within the DynamicPrefix.
	LabelSubnetName = "dynamic-prefix.io/subnet"
)

// BGPSyncReconciler reconciles DynamicPrefix resources and manages CiliumBGPAdvertisement
// resources for subnets with BGP advertisement enabled.
type BGPSyncReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=cilium.io,resources=ciliumbgpadvertisements,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cilium.io,resources=ciliumloadbalancerippools,verbs=get;list;watch

// Reconcile handles BGP advertisement synchronization for DynamicPrefix resources.
func (r *BGPSyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the DynamicPrefix
	var dp dynamicprefixiov1alpha1.DynamicPrefix
	if err := r.Get(ctx, req.NamespacedName, &dp); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("Reconciling BGP advertisements", "dynamicPrefix", dp.Name)

	// Collect subnets that need BGP advertisements
	subnetsWithBGP := r.getSubnetsWithBGP(&dp)

	// Track which advertisements we expect to exist
	expectedAdvertisements := make(map[string]bool)

	// Create or update advertisements for each subnet with BGP enabled
	for _, subnet := range subnetsWithBGP {
		advName := r.advertisementName(dp.Name, subnet.Name)
		expectedAdvertisements[advName] = true

		if err := r.reconcileAdvertisement(ctx, &dp, &subnet); err != nil {
			log.Error(err, "Failed to reconcile BGP advertisement", "subnet", subnet.Name)
			// Continue with other subnets
		}
	}

	// Delete orphaned advertisements (subnets that no longer have BGP enabled)
	if err := r.deleteOrphanedAdvertisements(ctx, &dp, expectedAdvertisements); err != nil {
		log.Error(err, "Failed to delete orphaned advertisements")
	}

	// Update DynamicPrefix status with advertisement names
	if err := r.updateStatus(ctx, &dp, subnetsWithBGP); err != nil {
		log.Error(err, "Failed to update DynamicPrefix status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// getSubnetsWithBGP returns subnets that have BGP advertisement enabled.
func (r *BGPSyncReconciler) getSubnetsWithBGP(dp *dynamicprefixiov1alpha1.DynamicPrefix) []dynamicprefixiov1alpha1.SubnetSpec {
	var result []dynamicprefixiov1alpha1.SubnetSpec
	for _, subnet := range dp.Spec.Subnets {
		if subnet.BGP != nil && subnet.BGP.Advertise {
			result = append(result, subnet)
		}
	}
	return result
}

// advertisementName generates the name for a CiliumBGPAdvertisement resource.
func (r *BGPSyncReconciler) advertisementName(dpName, subnetName string) string {
	return fmt.Sprintf("dp-%s-%s", dpName, subnetName)
}

// reconcileAdvertisement creates or updates a CiliumBGPAdvertisement for a subnet.
func (r *BGPSyncReconciler) reconcileAdvertisement(
	ctx context.Context,
	dp *dynamicprefixiov1alpha1.DynamicPrefix,
	subnet *dynamicprefixiov1alpha1.SubnetSpec,
) error {
	log := logf.FromContext(ctx)
	advName := r.advertisementName(dp.Name, subnet.Name)

	// Get the corresponding CiliumLoadBalancerIPPool to read its serviceSelector
	poolSelector, err := r.getPoolServiceSelector(ctx, dp.Name, subnet.Name)
	if err != nil {
		log.V(1).Info("Failed to get pool service selector, using empty selector", "error", err.Error())
		poolSelector = nil
	}

	// Build the CiliumBGPAdvertisement spec
	advSpec := r.buildAdvertisementSpec(subnet, poolSelector)

	// Create or update the advertisement
	adv := &unstructured.Unstructured{}
	adv.SetGroupVersionKind(CiliumBGPAdvertisementGVK)
	adv.SetName(advName)

	// Check if it exists
	err = r.Get(ctx, types.NamespacedName{Name: advName}, adv)
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to get CiliumBGPAdvertisement: %w", err)
	}

	if err != nil {
		// Create new advertisement
		adv = &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "cilium.io/v2alpha1",
				"kind":       "CiliumBGPAdvertisement",
				"metadata": map[string]interface{}{
					"name": advName,
					"labels": map[string]interface{}{
						LabelManagedBy:         LabelManagedByValue,
						LabelDynamicPrefixName: dp.Name,
						LabelSubnetName:        subnet.Name,
					},
				},
				"spec": advSpec,
			},
		}

		// Set owner reference for garbage collection
		if err := controllerutil.SetControllerReference(dp, adv, r.Scheme); err != nil {
			return fmt.Errorf("failed to set owner reference: %w", err)
		}

		if err := r.Create(ctx, adv); err != nil {
			return fmt.Errorf("failed to create CiliumBGPAdvertisement: %w", err)
		}
		log.Info("Created CiliumBGPAdvertisement", "name", advName, "subnet", subnet.Name)
	} else {
		// Update existing advertisement
		adv.Object["spec"] = advSpec

		// Ensure labels are set
		labels := adv.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[LabelManagedBy] = LabelManagedByValue
		labels[LabelDynamicPrefixName] = dp.Name
		labels[LabelSubnetName] = subnet.Name
		adv.SetLabels(labels)

		if err := r.Update(ctx, adv); err != nil {
			return fmt.Errorf("failed to update CiliumBGPAdvertisement: %w", err)
		}
		log.V(1).Info("Updated CiliumBGPAdvertisement", "name", advName, "subnet", subnet.Name)
	}

	return nil
}

// getPoolServiceSelector finds the CiliumLoadBalancerIPPool for this subnet and returns its serviceSelector.
func (r *BGPSyncReconciler) getPoolServiceSelector(
	ctx context.Context,
	dpName, subnetName string,
) (map[string]interface{}, error) {
	// List all CiliumLoadBalancerIPPools with matching annotations
	poolList := &unstructured.UnstructuredList{}
	poolList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2alpha1",
		Kind:    "CiliumLoadBalancerIPPoolList",
	})

	if err := r.List(ctx, poolList); err != nil {
		return nil, fmt.Errorf("failed to list CiliumLoadBalancerIPPools: %w", err)
	}

	for _, pool := range poolList.Items {
		annotations := pool.GetAnnotations()
		if annotations == nil {
			continue
		}
		if annotations[AnnotationName] != dpName {
			continue
		}
		if annotations[AnnotationSubnet] != subnetName {
			continue
		}

		// Found the matching pool, extract serviceSelector
		selector, found, err := unstructured.NestedMap(pool.Object, "spec", "serviceSelector")
		if err != nil || !found {
			return nil, nil // No selector defined
		}
		return selector, nil
	}

	return nil, fmt.Errorf("no CiliumLoadBalancerIPPool found for subnet %s", subnetName)
}

// buildAdvertisementSpec builds the spec for a CiliumBGPAdvertisement.
func (r *BGPSyncReconciler) buildAdvertisementSpec(
	subnet *dynamicprefixiov1alpha1.SubnetSpec,
	poolServiceSelector map[string]interface{},
) map[string]interface{} {
	// Build the advertisement entry
	advertisement := map[string]interface{}{
		"advertisementType": "Service",
		"service": map[string]interface{}{
			"addresses": []interface{}{"LoadBalancerIP"},
		},
	}

	// Add service selector if available from the pool
	if poolServiceSelector != nil && len(poolServiceSelector) > 0 {
		advertisement["selector"] = poolServiceSelector
	}

	// Add BGP community if specified
	if subnet.BGP != nil && subnet.BGP.Community != "" {
		advertisement["attributes"] = map[string]interface{}{
			"communities": map[string]interface{}{
				"standard": []interface{}{subnet.BGP.Community},
			},
		}
	}

	return map[string]interface{}{
		"advertisements": []interface{}{advertisement},
	}
}

// deleteOrphanedAdvertisements removes CiliumBGPAdvertisement resources that are no longer needed.
func (r *BGPSyncReconciler) deleteOrphanedAdvertisements(
	ctx context.Context,
	dp *dynamicprefixiov1alpha1.DynamicPrefix,
	expectedAdvertisements map[string]bool,
) error {
	log := logf.FromContext(ctx)

	// List all advertisements managed by this operator for this DynamicPrefix
	advList := &unstructured.UnstructuredList{}
	advList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "cilium.io",
		Version: "v2alpha1",
		Kind:    "CiliumBGPAdvertisementList",
	})

	if err := r.List(ctx, advList, client.MatchingLabels{
		LabelManagedBy:         LabelManagedByValue,
		LabelDynamicPrefixName: dp.Name,
	}); err != nil {
		return fmt.Errorf("failed to list CiliumBGPAdvertisements: %w", err)
	}

	for _, adv := range advList.Items {
		if !expectedAdvertisements[adv.GetName()] {
			if err := r.Delete(ctx, &adv); err != nil {
				log.Error(err, "Failed to delete orphaned CiliumBGPAdvertisement", "name", adv.GetName())
				continue
			}
			log.Info("Deleted orphaned CiliumBGPAdvertisement", "name", adv.GetName())
		}
	}

	return nil
}

// updateStatus updates the DynamicPrefix status with BGP advertisement information.
func (r *BGPSyncReconciler) updateStatus(
	ctx context.Context,
	dp *dynamicprefixiov1alpha1.DynamicPrefix,
	subnetsWithBGP []dynamicprefixiov1alpha1.SubnetSpec,
) error {
	// Build a map of subnet name to advertisement name
	advNames := make(map[string]string)
	for _, subnet := range subnetsWithBGP {
		advNames[subnet.Name] = r.advertisementName(dp.Name, subnet.Name)
	}

	// Update subnet status with advertisement names
	statusChanged := false
	for i := range dp.Status.Subnets {
		advName, hasBGP := advNames[dp.Status.Subnets[i].Name]
		if hasBGP {
			if dp.Status.Subnets[i].BGPAdvertisement != advName {
				dp.Status.Subnets[i].BGPAdvertisement = advName
				statusChanged = true
			}
		} else {
			if dp.Status.Subnets[i].BGPAdvertisement != "" {
				dp.Status.Subnets[i].BGPAdvertisement = ""
				statusChanged = true
			}
		}
	}

	// Update BGPAdvertisementReady condition
	condition := r.buildBGPCondition(ctx, dp, subnetsWithBGP)
	existingCondition := r.findCondition(dp.Status.Conditions, dynamicprefixiov1alpha1.ConditionTypeBGPAdvertisementReady)
	if existingCondition == nil || existingCondition.Status != condition.Status || existingCondition.Message != condition.Message {
		r.setCondition(&dp.Status.Conditions, condition)
		statusChanged = true
	}

	if statusChanged {
		if err := r.Status().Update(ctx, dp); err != nil {
			return fmt.Errorf("failed to update status: %w", err)
		}
	}

	return nil
}

// buildBGPCondition builds the BGPAdvertisementReady condition.
func (r *BGPSyncReconciler) buildBGPCondition(
	ctx context.Context,
	dp *dynamicprefixiov1alpha1.DynamicPrefix,
	subnetsWithBGP []dynamicprefixiov1alpha1.SubnetSpec,
) metav1.Condition {
	if len(subnetsWithBGP) == 0 {
		return metav1.Condition{
			Type:               dynamicprefixiov1alpha1.ConditionTypeBGPAdvertisementReady,
			Status:             metav1.ConditionFalse,
			Reason:             "NoBGPSubnets",
			Message:            "No subnets have BGP advertisement enabled",
			LastTransitionTime: metav1.Now(),
		}
	}

	// Check if all expected advertisements exist
	allReady := true
	for _, subnet := range subnetsWithBGP {
		advName := r.advertisementName(dp.Name, subnet.Name)
		adv := &unstructured.Unstructured{}
		adv.SetGroupVersionKind(CiliumBGPAdvertisementGVK)
		if err := r.Get(ctx, types.NamespacedName{Name: advName}, adv); err != nil {
			allReady = false
			break
		}
	}

	if allReady {
		return metav1.Condition{
			Type:               dynamicprefixiov1alpha1.ConditionTypeBGPAdvertisementReady,
			Status:             metav1.ConditionTrue,
			Reason:             "AdvertisementsReady",
			Message:            fmt.Sprintf("%d BGP advertisement(s) configured", len(subnetsWithBGP)),
			LastTransitionTime: metav1.Now(),
		}
	}

	return metav1.Condition{
		Type:               dynamicprefixiov1alpha1.ConditionTypeBGPAdvertisementReady,
		Status:             metav1.ConditionFalse,
		Reason:             "AdvertisementsPending",
		Message:            "Some BGP advertisements are not yet ready",
		LastTransitionTime: metav1.Now(),
	}
}

// findCondition finds a condition by type.
func (r *BGPSyncReconciler) findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

// setCondition updates or adds a condition.
func (r *BGPSyncReconciler) setCondition(conditions *[]metav1.Condition, condition metav1.Condition) {
	for i := range *conditions {
		if (*conditions)[i].Type == condition.Type {
			(*conditions)[i] = condition
			return
		}
	}
	*conditions = append(*conditions, condition)
}

// SetupWithManager sets up the controller with the Manager.
func (r *BGPSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Watch CiliumBGPAdvertisement for owned resources
	bgpAdv := &unstructured.Unstructured{}
	bgpAdv.SetGroupVersionKind(CiliumBGPAdvertisementGVK)

	return ctrl.NewControllerManagedBy(mgr).
		Named("bgpsync").
		For(&dynamicprefixiov1alpha1.DynamicPrefix{}).
		Owns(bgpAdv).
		Watches(&unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "cilium.io/v2alpha1",
				"kind":       "CiliumLoadBalancerIPPool",
			},
		}, handler.EnqueueRequestsFromMapFunc(r.findDynamicPrefixForPool)).
		Complete(r)
}

// findDynamicPrefixForPool maps a CiliumLoadBalancerIPPool to its referenced DynamicPrefix.
func (r *BGPSyncReconciler) findDynamicPrefixForPool(ctx context.Context, obj client.Object) []reconcile.Request {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return nil
	}

	dpName, ok := annotations[AnnotationName]
	if !ok {
		return nil
	}

	// Only trigger if the pool uses subnet mode
	_, hasSubnet := annotations[AnnotationSubnet]
	if !hasSubnet {
		return nil
	}

	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: dpName}},
	}
}
