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
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	dynamicprefixiov1alpha1 "github.com/jr42/dynamic-prefix-operator/api/v1alpha1"
	"github.com/jr42/dynamic-prefix-operator/internal/prefix"
)

const (
	finalizerName = "dynamic-prefix.io/finalizer"
)

// ReceiverFactory creates prefix receivers for DynamicPrefix resources
type ReceiverFactory interface {
	// CreateReceiver creates a new receiver based on the acquisition spec
	CreateReceiver(spec dynamicprefixiov1alpha1.AcquisitionSpec) (prefix.Receiver, error)
}

// DynamicPrefixReconciler reconciles a DynamicPrefix object
type DynamicPrefixReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	ReceiverFactory ReceiverFactory

	// receiversMu protects the receivers map
	receiversMu sync.RWMutex
	// receivers maps DynamicPrefix name to its active receiver
	receivers map[string]prefix.Receiver
}

// NewDynamicPrefixReconciler creates a new reconciler with default configuration
func NewDynamicPrefixReconciler(c client.Client, scheme *runtime.Scheme) *DynamicPrefixReconciler {
	return &DynamicPrefixReconciler{
		Client:    c,
		Scheme:    scheme,
		receivers: make(map[string]prefix.Receiver),
	}
}

// +kubebuilder:rbac:groups=dynamic-prefix.io,resources=dynamicprefixes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dynamic-prefix.io,resources=dynamicprefixes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dynamic-prefix.io,resources=dynamicprefixes/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *DynamicPrefixReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the DynamicPrefix instance
	var dp dynamicprefixiov1alpha1.DynamicPrefix
	if err := r.Get(ctx, req.NamespacedName, &dp); err != nil {
		// Resource deleted - clean up receiver if any
		r.cleanupReceiver(req.Name)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if !dp.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&dp, finalizerName) {
			log.Info("DynamicPrefix being deleted, cleaning up receiver")
			r.cleanupReceiver(dp.Name)

			controllerutil.RemoveFinalizer(&dp, finalizerName)
			if err := r.Update(ctx, &dp); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&dp, finalizerName) {
		controllerutil.AddFinalizer(&dp, finalizerName)
		if err := r.Update(ctx, &dp); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Get or create the receiver for this DynamicPrefix
	receiver, err := r.getOrCreateReceiver(ctx, &dp)
	if err != nil {
		log.Error(err, "Failed to create receiver")
		r.setCondition(&dp, dynamicprefixiov1alpha1.ConditionTypePrefixAcquired, metav1.ConditionFalse,
			"ReceiverCreationFailed", err.Error())
		if statusErr := r.Status().Update(ctx, &dp); statusErr != nil {
			log.Error(statusErr, "Failed to update status")
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Get current prefix from receiver
	currentPrefix := receiver.CurrentPrefix()
	if currentPrefix == nil {
		log.Info("No prefix acquired yet")
		r.setCondition(&dp, dynamicprefixiov1alpha1.ConditionTypePrefixAcquired, metav1.ConditionFalse,
			"WaitingForPrefix", "Waiting to receive prefix from upstream")
		if err := r.Status().Update(ctx, &dp); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Update status with current prefix
	prefixChanged := dp.Status.CurrentPrefix != currentPrefix.Network.String()
	if prefixChanged {
		log.Info("Prefix changed", "oldPrefix", dp.Status.CurrentPrefix, "newPrefix", currentPrefix.Network.String())
		r.handlePrefixChange(ctx, &dp, currentPrefix)
	}

	dp.Status.CurrentPrefix = currentPrefix.Network.String()
	dp.Status.PrefixSource = sourceToPrefixSource(receiver.Source())

	// Calculate lease expiration
	if currentPrefix.ValidLifetime > 0 {
		expiresAt := metav1.NewTime(currentPrefix.ReceivedAt.Add(currentPrefix.ValidLifetime))
		dp.Status.LeaseExpiresAt = &expiresAt
	}

	// Calculate subnets
	subnets, err := r.calculateSubnets(currentPrefix.Network, dp.Spec.Subnets)
	if err != nil {
		log.Error(err, "Failed to calculate subnets")
		r.setCondition(&dp, dynamicprefixiov1alpha1.ConditionTypeDegraded, metav1.ConditionTrue,
			"SubnetCalculationFailed", err.Error())
	} else {
		dp.Status.Subnets = subnets
		r.setCondition(&dp, dynamicprefixiov1alpha1.ConditionTypeDegraded, metav1.ConditionFalse,
			"Healthy", "DynamicPrefix is operating normally")
	}

	// Set prefix acquired condition
	r.setCondition(&dp, dynamicprefixiov1alpha1.ConditionTypePrefixAcquired, metav1.ConditionTrue,
		"PrefixAcquired", fmt.Sprintf("Prefix %s acquired via %s", currentPrefix.Network, receiver.Source()))

	// Update status
	if err := r.Status().Update(ctx, &dp); err != nil {
		return ctrl.Result{}, err
	}

	// Requeue to handle lease renewal
	requeueAfter := r.calculateRequeueTime(currentPrefix)
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// getOrCreateReceiver returns an existing receiver or creates a new one
func (r *DynamicPrefixReconciler) getOrCreateReceiver(ctx context.Context, dp *dynamicprefixiov1alpha1.DynamicPrefix) (prefix.Receiver, error) {
	r.receiversMu.RLock()
	receiver, exists := r.receivers[dp.Name]
	r.receiversMu.RUnlock()

	if exists {
		return receiver, nil
	}

	// Create new receiver
	r.receiversMu.Lock()
	defer r.receiversMu.Unlock()

	// Double-check after acquiring write lock
	if receiver, exists = r.receivers[dp.Name]; exists {
		return receiver, nil
	}

	if r.ReceiverFactory == nil {
		// Use mock receiver for testing
		receiver = prefix.NewMockReceiver(prefix.SourceDHCPv6PD)
	} else {
		var err error
		receiver, err = r.ReceiverFactory.CreateReceiver(dp.Spec.Acquisition)
		if err != nil {
			return nil, fmt.Errorf("failed to create receiver: %w", err)
		}
	}

	// Start the receiver
	if err := receiver.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start receiver: %w", err)
	}

	r.receivers[dp.Name] = receiver
	return receiver, nil
}

// cleanupReceiver stops and removes a receiver
func (r *DynamicPrefixReconciler) cleanupReceiver(name string) {
	r.receiversMu.Lock()
	defer r.receiversMu.Unlock()

	receiver, exists := r.receivers[name]
	if !exists {
		return
	}

	if err := receiver.Stop(); err != nil {
		logf.Log.Error(err, "Failed to stop receiver", "name", name)
	}
	delete(r.receivers, name)
}

// calculateSubnets calculates subnet CIDRs from the base prefix
func (r *DynamicPrefixReconciler) calculateSubnets(basePrefix netip.Prefix, specs []dynamicprefixiov1alpha1.SubnetSpec) ([]dynamicprefixiov1alpha1.SubnetStatus, error) {
	if len(specs) == 0 {
		return nil, nil
	}

	configs := make([]prefix.SubnetConfig, len(specs))
	for i, spec := range specs {
		configs[i] = prefix.SubnetConfig{
			Name:         spec.Name,
			Offset:       spec.Offset,
			PrefixLength: spec.PrefixLength,
		}
	}

	subnets, err := prefix.CalculateSubnets(basePrefix, configs)
	if err != nil {
		return nil, err
	}

	result := make([]dynamicprefixiov1alpha1.SubnetStatus, len(subnets))
	for i, s := range subnets {
		result[i] = dynamicprefixiov1alpha1.SubnetStatus{
			Name: s.Name,
			CIDR: s.CIDR.String(),
		}
	}

	return result, nil
}

// handlePrefixChange handles graceful prefix transitions
func (r *DynamicPrefixReconciler) handlePrefixChange(ctx context.Context, dp *dynamicprefixiov1alpha1.DynamicPrefix, newPrefix *prefix.Prefix) {
	log := logf.FromContext(ctx)
	now := metav1.Now()

	// Add old prefix to history if it exists
	if dp.Status.CurrentPrefix != "" {
		oldEntry := dynamicprefixiov1alpha1.PrefixHistoryEntry{
			Prefix:       dp.Status.CurrentPrefix,
			AcquiredAt:   dp.CreationTimestamp,
			DeprecatedAt: &now,
			State:        dynamicprefixiov1alpha1.PrefixStateDraining,
		}

		// Find and update existing entry or add new one
		dp.Status.History = append(dp.Status.History, oldEntry)

		// Limit history size
		maxHistory := 2
		if dp.Spec.Transition != nil && dp.Spec.Transition.MaxPrefixHistory > 0 {
			maxHistory = dp.Spec.Transition.MaxPrefixHistory
		}
		if len(dp.Status.History) > maxHistory {
			dp.Status.History = dp.Status.History[len(dp.Status.History)-maxHistory:]
		}

		log.Info("Added prefix to history", "prefix", dp.Status.CurrentPrefix, "state", dynamicprefixiov1alpha1.PrefixStateDraining)
	}
}

// setCondition sets a condition on the DynamicPrefix status
func (r *DynamicPrefixReconciler) setCondition(dp *dynamicprefixiov1alpha1.DynamicPrefix, condType string, status metav1.ConditionStatus, reason, message string) {
	condition := metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: dp.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}
	meta.SetStatusCondition(&dp.Status.Conditions, condition)
}

// calculateRequeueTime determines when to requeue based on lease
func (r *DynamicPrefixReconciler) calculateRequeueTime(p *prefix.Prefix) time.Duration {
	if p.ValidLifetime == 0 {
		// No lease expiration, requeue periodically
		return 5 * time.Minute
	}

	// Requeue at 80% of remaining lease time, but not less than 1 minute
	remaining := time.Until(p.ReceivedAt.Add(p.ValidLifetime))
	requeue := time.Duration(float64(remaining) * 0.8)
	if requeue < time.Minute {
		requeue = time.Minute
	}
	if requeue > 5*time.Minute {
		requeue = 5 * time.Minute
	}
	return requeue
}

// sourceToPrefixSource converts prefix.Source to v1alpha1.PrefixSource
func sourceToPrefixSource(s prefix.Source) dynamicprefixiov1alpha1.PrefixSource {
	switch s {
	case prefix.SourceDHCPv6PD:
		return dynamicprefixiov1alpha1.PrefixSourceDHCPv6PD
	case prefix.SourceRouterAdvertisement:
		return dynamicprefixiov1alpha1.PrefixSourceRouterAdvertisement
	case prefix.SourceStatic:
		return dynamicprefixiov1alpha1.PrefixSourceStatic
	default:
		return dynamicprefixiov1alpha1.PrefixSourceUnknown
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *DynamicPrefixReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dynamicprefixiov1alpha1.DynamicPrefix{}).
		Named("dynamicprefix").
		Complete(r)
}
