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

package prefix

import (
	"fmt"

	dynamicprefixiov1alpha1 "github.com/jr42/dynamic-prefix-operator/api/v1alpha1"
)

// ReceiverFactory creates Receiver instances based on AcquisitionSpec.
type ReceiverFactory interface {
	// CreateReceiver creates a Receiver based on the given acquisition spec.
	CreateReceiver(spec dynamicprefixiov1alpha1.AcquisitionSpec) (Receiver, error)
}

// DefaultReceiverFactory is the default implementation of ReceiverFactory.
type DefaultReceiverFactory struct{}

// NewReceiverFactory creates a new DefaultReceiverFactory.
func NewReceiverFactory() *DefaultReceiverFactory {
	return &DefaultReceiverFactory{}
}

// CreateReceiver creates a Receiver based on the AcquisitionSpec.
// Decision logic:
// 1. If only DHCPv6PD configured → DHCPv6PDReceiver
// 2. If only RouterAdvertisement configured → RAReceiver
// 3. If both configured → CompositeReceiver (DHCPv6-PD primary, RA fallback)
func (f *DefaultReceiverFactory) CreateReceiver(spec dynamicprefixiov1alpha1.AcquisitionSpec) (Receiver, error) {
	hasDHCPv6 := spec.DHCPv6PD != nil
	hasRA := spec.RouterAdvertisement != nil && spec.RouterAdvertisement.Enabled

	switch {
	case hasDHCPv6 && hasRA:
		// Both configured - use composite receiver
		return f.createCompositeReceiver(spec)
	case hasDHCPv6:
		// Only DHCPv6-PD configured
		return f.createDHCPv6PDReceiver(spec.DHCPv6PD)
	case hasRA:
		// Only RA configured
		return f.createRAReceiver(spec.RouterAdvertisement)
	default:
		return nil, fmt.Errorf("no acquisition method configured")
	}
}

// createDHCPv6PDReceiver creates a DHCPv6-PD receiver from the spec.
func (f *DefaultReceiverFactory) createDHCPv6PDReceiver(spec *dynamicprefixiov1alpha1.DHCPv6PDSpec) (*DHCPv6PDReceiver, error) {
	if spec.Interface == "" {
		return nil, fmt.Errorf("DHCPv6-PD interface is required")
	}

	prefixLength := 56 // Default
	if spec.RequestedPrefixLength != nil {
		prefixLength = *spec.RequestedPrefixLength
	}

	return NewDHCPv6PDReceiver(spec.Interface, prefixLength), nil
}

// createRAReceiver creates a Router Advertisement receiver from the spec.
func (f *DefaultReceiverFactory) createRAReceiver(spec *dynamicprefixiov1alpha1.RouterAdvertisementSpec) (*RAReceiver, error) {
	if spec.Interface == "" {
		return nil, fmt.Errorf("router advertisement interface is required")
	}

	return NewRAReceiver(spec.Interface), nil
}

// createCompositeReceiver creates a composite receiver with DHCPv6-PD as primary and RA as fallback.
func (f *DefaultReceiverFactory) createCompositeReceiver(spec dynamicprefixiov1alpha1.AcquisitionSpec) (*CompositeReceiver, error) {
	primary, err := f.createDHCPv6PDReceiver(spec.DHCPv6PD)
	if err != nil {
		return nil, fmt.Errorf("failed to create primary DHCPv6-PD receiver: %w", err)
	}

	fallback, err := f.createRAReceiver(spec.RouterAdvertisement)
	if err != nil {
		return nil, fmt.Errorf("failed to create fallback RA receiver: %w", err)
	}

	return NewCompositeReceiver(primary, fallback), nil
}
