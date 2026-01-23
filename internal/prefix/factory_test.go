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
	"testing"

	dynamicprefixiov1alpha1 "github.com/jr42/dynamic-prefix-operator/api/v1alpha1"
)

func TestDefaultReceiverFactory_CreateReceiver(t *testing.T) {
	factory := NewReceiverFactory()

	tests := []struct {
		name           string
		spec           dynamicprefixiov1alpha1.AcquisitionSpec
		expectedType   string
		expectedSource Source
		wantErr        bool
	}{
		{
			name: "DHCPv6-PD only",
			spec: dynamicprefixiov1alpha1.AcquisitionSpec{
				DHCPv6PD: &dynamicprefixiov1alpha1.DHCPv6PDSpec{
					Interface: "eth0",
				},
			},
			expectedType:   "*prefix.DHCPv6PDReceiver",
			expectedSource: SourceDHCPv6PD,
			wantErr:        false,
		},
		{
			name: "RA only",
			spec: dynamicprefixiov1alpha1.AcquisitionSpec{
				RouterAdvertisement: &dynamicprefixiov1alpha1.RouterAdvertisementSpec{
					Interface: "eth0",
					Enabled:   true,
				},
			},
			expectedType:   "*prefix.RAReceiver",
			expectedSource: SourceRouterAdvertisement,
			wantErr:        false,
		},
		{
			name: "Both DHCPv6-PD and RA",
			spec: dynamicprefixiov1alpha1.AcquisitionSpec{
				DHCPv6PD: &dynamicprefixiov1alpha1.DHCPv6PDSpec{
					Interface: "eth0",
				},
				RouterAdvertisement: &dynamicprefixiov1alpha1.RouterAdvertisementSpec{
					Interface: "eth0",
					Enabled:   true,
				},
			},
			expectedType:   "*prefix.CompositeReceiver",
			expectedSource: SourceDHCPv6PD, // Primary is DHCPv6-PD
			wantErr:        false,
		},
		{
			name: "RA disabled, only DHCPv6-PD",
			spec: dynamicprefixiov1alpha1.AcquisitionSpec{
				DHCPv6PD: &dynamicprefixiov1alpha1.DHCPv6PDSpec{
					Interface: "eth0",
				},
				RouterAdvertisement: &dynamicprefixiov1alpha1.RouterAdvertisementSpec{
					Interface: "eth0",
					Enabled:   false,
				},
			},
			expectedType:   "*prefix.DHCPv6PDReceiver",
			expectedSource: SourceDHCPv6PD,
			wantErr:        false,
		},
		{
			name:    "No acquisition method",
			spec:    dynamicprefixiov1alpha1.AcquisitionSpec{},
			wantErr: true,
		},
		{
			name: "DHCPv6-PD without interface",
			spec: dynamicprefixiov1alpha1.AcquisitionSpec{
				DHCPv6PD: &dynamicprefixiov1alpha1.DHCPv6PDSpec{
					Interface: "",
				},
			},
			wantErr: true,
		},
		{
			name: "RA without interface",
			spec: dynamicprefixiov1alpha1.AcquisitionSpec{
				RouterAdvertisement: &dynamicprefixiov1alpha1.RouterAdvertisementSpec{
					Interface: "",
					Enabled:   true,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receiver, err := factory.CreateReceiver(tt.spec)

			if (err != nil) != tt.wantErr {
				t.Errorf("CreateReceiver() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				return
			}

			if receiver == nil {
				t.Error("Expected receiver to be non-nil")
				return
			}

			// Check source type
			if receiver.Source() != tt.expectedSource {
				t.Errorf("receiver.Source() = %v, want %v", receiver.Source(), tt.expectedSource)
			}
		})
	}
}

func TestDefaultReceiverFactory_DHCPv6PDPrefixLength(t *testing.T) {
	factory := NewReceiverFactory()

	tests := []struct {
		name           string
		prefixLength   *int
		expectedLength int
	}{
		{
			name:           "Nil prefix length uses default",
			prefixLength:   nil,
			expectedLength: 56,
		},
		{
			name:           "Custom prefix length /48",
			prefixLength:   intPtr(48),
			expectedLength: 48,
		},
		{
			name:           "Custom prefix length /60",
			prefixLength:   intPtr(60),
			expectedLength: 60,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := dynamicprefixiov1alpha1.AcquisitionSpec{
				DHCPv6PD: &dynamicprefixiov1alpha1.DHCPv6PDSpec{
					Interface:             "eth0",
					RequestedPrefixLength: tt.prefixLength,
				},
			}

			receiver, err := factory.CreateReceiver(spec)
			if err != nil {
				t.Fatalf("CreateReceiver() error = %v", err)
			}

			dhcp, ok := receiver.(*DHCPv6PDReceiver)
			if !ok {
				t.Fatal("Expected DHCPv6PDReceiver")
			}

			if dhcp.requestedPrefixLength != tt.expectedLength {
				t.Errorf("requestedPrefixLength = %d, want %d", dhcp.requestedPrefixLength, tt.expectedLength)
			}
		})
	}
}

func intPtr(i int) *int {
	return &i
}
