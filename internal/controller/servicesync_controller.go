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
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	// AnnotationCiliumIPs is the Cilium LB-IPAM annotation for requesting specific IPs.
	AnnotationCiliumIPs = "lbipam.cilium.io/ips"

	// AnnotationExternalDNSTarget is the external-dns annotation for overriding DNS target.
	AnnotationExternalDNSTarget = "external-dns.alpha.kubernetes.io/target"

	// AnnotationServiceAddressRange specifies which address range to use for Service IPs.
	// This is used when the DynamicPrefix uses address ranges (Mode 1).
	AnnotationServiceAddressRange = "dynamic-prefix.io/service-address-range"

	// AnnotationServiceSubnet specifies which subnet to use for Service IPs.
	// This is used when the DynamicPrefix uses subnets (Mode 2).
	AnnotationServiceSubnet = "dynamic-prefix.io/service-subnet"
)

// ServiceSyncReconciler reconciles LoadBalancer Services for HA mode prefix transitions.
// In HA mode, it manages both lbipam.cilium.io/ips and external-dns.alpha.kubernetes.io/target
// annotations to ensure graceful transitions when prefixes change.
type ServiceSyncReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;update;patch

// Reconcile handles Service synchronization for HA mode prefix transitions.
func (r *ServiceSyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the Service
	var svc corev1.Service
	if err := r.Get(ctx, req.NamespacedName, &svc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Skip non-LoadBalancer services
	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return ctrl.Result{}, nil
	}

	// Check for DynamicPrefix annotation
	annotations := svc.GetAnnotations()
	if annotations == nil {
		return ctrl.Result{}, nil
	}

	dpName, hasDP := annotations[AnnotationName]
	if !hasDP {
		return ctrl.Result{}, nil
	}

	// Fetch the referenced DynamicPrefix
	var dp dynamicprefixiov1alpha1.DynamicPrefix
	if err := r.Get(ctx, types.NamespacedName{Name: dpName}, &dp); err != nil {
		log.Error(err, "Failed to get DynamicPrefix", "name", dpName)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Check if HA mode is enabled
	if dp.Spec.Transition == nil || dp.Spec.Transition.Mode != dynamicprefixiov1alpha1.TransitionModeHA {
		// Not HA mode, skip Service management
		return ctrl.Result{}, nil
	}

	log.Info("Syncing Service for HA mode", "service", req.NamespacedName, "dynamicPrefix", dpName)

	// Get current assigned IP from Service status
	currentServiceIP := r.getCurrentServiceIP(&svc)
	if currentServiceIP == "" {
		// Service doesn't have an IP yet, let Cilium assign one
		log.V(1).Info("Service has no IP assigned yet, skipping")
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Calculate all IPs (current + historical) based on the Service's current IP
	allIPs, currentIP, err := r.calculateServiceIPs(ctx, &dp, &svc, currentServiceIP)
	if err != nil {
		log.Error(err, "Failed to calculate Service IPs")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Update Service annotations
	updated := false
	newAnnotations := make(map[string]string)
	for k, v := range annotations {
		newAnnotations[k] = v
	}

	// Set lbipam.cilium.io/ips with all IPs
	allIPsStr := strings.Join(allIPs, ",")
	if annotations[AnnotationCiliumIPs] != allIPsStr {
		newAnnotations[AnnotationCiliumIPs] = allIPsStr
		updated = true
	}

	// Set external-dns target to current IP only
	if annotations[AnnotationExternalDNSTarget] != currentIP {
		newAnnotations[AnnotationExternalDNSTarget] = currentIP
		updated = true
	}

	// Update last-sync annotation
	newAnnotations[AnnotationLastSync] = time.Now().UTC().Format(time.RFC3339)

	if updated {
		svc.SetAnnotations(newAnnotations)
		if err := r.Update(ctx, &svc); err != nil {
			log.Error(err, "Failed to update Service annotations")
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		log.Info("Service annotations updated", "service", req.NamespacedName, "allIPs", allIPsStr, "dnsTarget", currentIP)
	}

	return ctrl.Result{}, nil
}

// getCurrentServiceIP returns the current IPv6 IP from Service status.
func (r *ServiceSyncReconciler) getCurrentServiceIP(svc *corev1.Service) string {
	for _, ingress := range svc.Status.LoadBalancer.Ingress {
		if ingress.IP != "" {
			// Prefer IPv6
			addr, err := netip.ParseAddr(ingress.IP)
			if err == nil && addr.Is6() {
				return ingress.IP
			}
		}
	}
	// Fall back to any IP
	for _, ingress := range svc.Status.LoadBalancer.Ingress {
		if ingress.IP != "" {
			return ingress.IP
		}
	}
	return ""
}

// calculateServiceIPs calculates all IPs for a Service based on current prefix and history.
// Returns (allIPs, currentIP, error).
func (r *ServiceSyncReconciler) calculateServiceIPs(
	ctx context.Context,
	dp *dynamicprefixiov1alpha1.DynamicPrefix,
	svc *corev1.Service,
	currentServiceIP string,
) ([]string, string, error) {
	log := logf.FromContext(ctx)
	annotations := svc.GetAnnotations()

	// Get max history count
	maxHistory := 2 // Default
	if dp.Spec.Transition != nil && dp.Spec.Transition.MaxPrefixHistory > 0 {
		maxHistory = dp.Spec.Transition.MaxPrefixHistory
	}

	// Determine the IP offset within the prefix from the current Service IP
	// This allows us to calculate corresponding IPs in historical prefixes
	currentAddr, err := netip.ParseAddr(currentServiceIP)
	if err != nil {
		return nil, "", err
	}

	addressRangeName := annotations[AnnotationServiceAddressRange]
	subnetName := annotations[AnnotationServiceSubnet]
	// Also check the pool-level annotations for backward compatibility
	if addressRangeName == "" {
		addressRangeName = annotations[AnnotationAddressRange]
	}
	if subnetName == "" {
		subnetName = annotations[AnnotationSubnet]
	}

	var allIPs []string
	var currentPrefixIP string

	if addressRangeName != "" {
		// Mode 1: Address ranges
		currentPrefixIP, allIPs, err = r.calculateAddressRangeIPs(dp, currentAddr, addressRangeName, maxHistory)
		if err != nil {
			log.Error(err, "Failed to calculate address range IPs")
			// Fall back to current IP only
			return []string{currentServiceIP}, currentServiceIP, nil
		}
	} else if subnetName != "" {
		// Mode 2: Subnets
		currentPrefixIP, allIPs, err = r.calculateSubnetIPs(dp, currentAddr, subnetName, maxHistory)
		if err != nil {
			log.Error(err, "Failed to calculate subnet IPs")
			// Fall back to current IP only
			return []string{currentServiceIP}, currentServiceIP, nil
		}
	} else {
		// No specific range/subnet, use current IP
		return []string{currentServiceIP}, currentServiceIP, nil
	}

	return allIPs, currentPrefixIP, nil
}

// calculateAddressRangeIPs calculates IPs for address range mode.
func (r *ServiceSyncReconciler) calculateAddressRangeIPs(
	dp *dynamicprefixiov1alpha1.DynamicPrefix,
	currentAddr netip.Addr,
	addressRangeName string,
	maxHistory int,
) (string, []string, error) {
	// Find the address range spec
	var rangeSpec *dynamicprefixiov1alpha1.AddressRangeSpec
	for i := range dp.Spec.AddressRanges {
		if dp.Spec.AddressRanges[i].Name == addressRangeName {
			rangeSpec = &dp.Spec.AddressRanges[i]
			break
		}
	}
	if rangeSpec == nil {
		return "", nil, nil
	}

	// Calculate offset of current IP within its range
	currentPrefix, err := netip.ParsePrefix(dp.Status.CurrentPrefix)
	if err != nil {
		return "", nil, err
	}

	cfg := prefix.AddressRangeConfig{
		Name:  rangeSpec.Name,
		Start: rangeSpec.Start,
		End:   rangeSpec.End,
	}

	currentRange, err := prefix.CalculateAddressRange(currentPrefix, cfg)
	if err != nil {
		return "", nil, err
	}

	// Calculate offset from start of range
	offset := r.calculateIPOffset(currentRange.Start, currentAddr)

	var allIPs []string
	currentPrefixIP := currentAddr.String()

	// Add current prefix IP
	allIPs = append(allIPs, currentPrefixIP)

	// Calculate IPs for historical prefixes
	for i, histEntry := range dp.Status.History {
		if i >= maxHistory {
			break
		}

		histPrefix, err := netip.ParsePrefix(histEntry.Prefix)
		if err != nil {
			continue
		}

		histRange, err := prefix.CalculateAddressRange(histPrefix, cfg)
		if err != nil {
			continue
		}

		histIP := r.applyIPOffset(histRange.Start, offset)
		if histIP.IsValid() {
			allIPs = append(allIPs, histIP.String())
		}
	}

	return currentPrefixIP, allIPs, nil
}

// calculateSubnetIPs calculates IPs for subnet mode.
func (r *ServiceSyncReconciler) calculateSubnetIPs(
	dp *dynamicprefixiov1alpha1.DynamicPrefix,
	currentAddr netip.Addr,
	subnetName string,
	maxHistory int,
) (string, []string, error) {
	// Find the subnet spec
	var subnetSpec *dynamicprefixiov1alpha1.SubnetSpec
	for i := range dp.Spec.Subnets {
		if dp.Spec.Subnets[i].Name == subnetName {
			subnetSpec = &dp.Spec.Subnets[i]
			break
		}
	}
	if subnetSpec == nil {
		return "", nil, nil
	}

	// Calculate current subnet
	currentPrefix, err := netip.ParsePrefix(dp.Status.CurrentPrefix)
	if err != nil {
		return "", nil, err
	}

	cfg := prefix.SubnetConfig{
		Name:         subnetSpec.Name,
		Offset:       subnetSpec.Offset,
		PrefixLength: subnetSpec.PrefixLength,
	}

	currentSubnet, err := prefix.CalculateSubnet(currentPrefix, cfg)
	if err != nil {
		return "", nil, err
	}

	// Calculate offset from start of subnet
	offset := r.calculateIPOffset(currentSubnet.CIDR.Addr(), currentAddr)

	var allIPs []string
	currentPrefixIP := currentAddr.String()

	// Add current prefix IP
	allIPs = append(allIPs, currentPrefixIP)

	// Calculate IPs for historical prefixes
	for i, histEntry := range dp.Status.History {
		if i >= maxHistory {
			break
		}

		histPrefix, err := netip.ParsePrefix(histEntry.Prefix)
		if err != nil {
			continue
		}

		histSubnet, err := prefix.CalculateSubnet(histPrefix, cfg)
		if err != nil {
			continue
		}

		histIP := r.applyIPOffset(histSubnet.CIDR.Addr(), offset)
		if histIP.IsValid() {
			allIPs = append(allIPs, histIP.String())
		}
	}

	return currentPrefixIP, allIPs, nil
}

// calculateIPOffset calculates the offset between two IPv6 addresses.
func (r *ServiceSyncReconciler) calculateIPOffset(base, target netip.Addr) [16]byte {
	baseBytes := base.As16()
	targetBytes := target.As16()
	var offset [16]byte

	borrow := uint16(0)
	for i := 15; i >= 0; i-- {
		diff := int16(targetBytes[i]) - int16(baseBytes[i]) - int16(borrow)
		if diff < 0 {
			diff += 256
			borrow = 1
		} else {
			borrow = 0
		}
		offset[i] = byte(diff)
	}

	return offset
}

// applyIPOffset applies an offset to an IPv6 address.
func (r *ServiceSyncReconciler) applyIPOffset(base netip.Addr, offset [16]byte) netip.Addr {
	baseBytes := base.As16()
	var result [16]byte

	carry := uint16(0)
	for i := 15; i >= 0; i-- {
		sum := uint16(baseBytes[i]) + uint16(offset[i]) + carry
		result[i] = byte(sum & 0xFF)
		carry = sum >> 8
	}

	return netip.AddrFrom16(result)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create predicate for LoadBalancer Services with dynamic-prefix.io/name annotation
	hasAnnotation := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		svc, ok := obj.(*corev1.Service)
		if !ok {
			return false
		}
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			return false
		}
		annotations := svc.GetAnnotations()
		if annotations == nil {
			return false
		}
		_, ok = annotations[AnnotationName]
		return ok
	})

	return ctrl.NewControllerManagedBy(mgr).
		Named("servicesync").
		For(&corev1.Service{}, builder.WithPredicates(hasAnnotation)).
		Watches(&dynamicprefixiov1alpha1.DynamicPrefix{}, handler.EnqueueRequestsFromMapFunc(r.findReferencingServices)).
		Complete(r)
}

// findReferencingServices finds all Services that reference the given DynamicPrefix.
func (r *ServiceSyncReconciler) findReferencingServices(ctx context.Context, obj client.Object) []reconcile.Request {
	dp, ok := obj.(*dynamicprefixiov1alpha1.DynamicPrefix)
	if !ok {
		return nil
	}

	// Only process if HA mode is enabled
	if dp.Spec.Transition == nil || dp.Spec.Transition.Mode != dynamicprefixiov1alpha1.TransitionModeHA {
		return nil
	}

	log := logf.FromContext(ctx)
	var requests []reconcile.Request

	// List all Services
	var serviceList corev1.ServiceList
	if err := r.List(ctx, &serviceList); err != nil {
		log.V(1).Info("Failed to list Services", "error", err)
		return nil
	}

	for _, svc := range serviceList.Items {
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			continue
		}
		annotations := svc.GetAnnotations()
		if annotations == nil {
			continue
		}
		if annotations[AnnotationName] == dp.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      svc.Name,
					Namespace: svc.Namespace,
				},
			})
		}
	}

	if len(requests) > 0 {
		log.Info("DynamicPrefix changed, enqueuing referencing Services", "dynamicPrefix", dp.Name, "serviceCount", len(requests))
	}

	return requests
}
