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
	"net/netip"
	"testing"
)

func TestCalculateSubnet(t *testing.T) {
	tests := []struct {
		name       string
		basePrefix string
		config     SubnetConfig
		wantCIDR   string
		wantErr    bool
	}{
		{
			name:       "first /64 from /48",
			basePrefix: "2001:db8::/48",
			config: SubnetConfig{
				Name:         "default",
				Offset:       0,
				PrefixLength: 64,
			},
			wantCIDR: "2001:db8::/64",
			wantErr:  false,
		},
		{
			name:       "second /64 from /48",
			basePrefix: "2001:db8::/48",
			config: SubnetConfig{
				Name:         "second",
				Offset:       1,
				PrefixLength: 64,
			},
			wantCIDR: "2001:db8:0:1::/64",
			wantErr:  false,
		},
		{
			name:       "256th /64 from /48",
			basePrefix: "2001:db8::/48",
			config: SubnetConfig{
				Name:         "services",
				Offset:       256,
				PrefixLength: 64,
			},
			wantCIDR: "2001:db8:0:100::/64",
			wantErr:  false,
		},
		{
			name:       "first /120 from /60",
			basePrefix: "2001:db8:1234:ab00::/60",
			config: SubnetConfig{
				Name:         "loadbalancers",
				Offset:       0,
				PrefixLength: 120,
			},
			wantCIDR: "2001:db8:1234:ab00::/120",
			wantErr:  false,
		},
		{
			name:       "17th /120 from /60",
			basePrefix: "2001:db8:1234:ab00::/60",
			config: SubnetConfig{
				Name:         "loadbalancers",
				Offset:       16, // 16 * 256 = 0x1000
				PrefixLength: 120,
			},
			wantCIDR: "2001:db8:1234:ab00::1000/120",
			wantErr:  false,
		},
		{
			name:       "second /112 from /56",
			basePrefix: "2001:db8:abcd:ef00::/56",
			config: SubnetConfig{
				Name:         "services",
				Offset:       1, // 1 * 65536 = 0x10000
				PrefixLength: 112,
			},
			wantCIDR: "2001:db8:abcd:ef00::1:0/112",
			wantErr:  false,
		},
		{
			name:       "error: subnet shorter than base",
			basePrefix: "2001:db8::/64",
			config: SubnetConfig{
				Name:         "invalid",
				Offset:       0,
				PrefixLength: 48,
			},
			wantErr: true,
		},
		{
			name:       "error: prefix length > 128",
			basePrefix: "2001:db8::/64",
			config: SubnetConfig{
				Name:         "invalid",
				Offset:       0,
				PrefixLength: 129,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			basePrefix := netip.MustParsePrefix(tt.basePrefix)

			subnet, err := CalculateSubnet(basePrefix, tt.config)

			if tt.wantErr {
				if err == nil {
					t.Errorf("CalculateSubnet() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("CalculateSubnet() unexpected error: %v", err)
				return
			}

			if subnet.Name != tt.config.Name {
				t.Errorf("subnet.Name = %q, want %q", subnet.Name, tt.config.Name)
			}

			if subnet.CIDR.String() != tt.wantCIDR {
				t.Errorf("subnet.CIDR = %q, want %q", subnet.CIDR.String(), tt.wantCIDR)
			}
		})
	}
}

func TestCalculateSubnets(t *testing.T) {
	basePrefix := netip.MustParsePrefix("2001:db8::/48")

	configs := []SubnetConfig{
		{
			Name:         "services",
			Offset:       0,
			PrefixLength: 64,
		},
		{
			Name:         "pods",
			Offset:       1,
			PrefixLength: 64,
		},
		{
			Name:         "loadbalancers",
			Offset:       256,
			PrefixLength: 64,
		},
	}

	subnets, err := CalculateSubnets(basePrefix, configs)
	if err != nil {
		t.Fatalf("CalculateSubnets() error: %v", err)
	}

	if len(subnets) != 3 {
		t.Fatalf("got %d subnets, want 3", len(subnets))
	}

	// Check first subnet
	if subnets[0].Name != "services" {
		t.Errorf("subnets[0].Name = %q, want %q", subnets[0].Name, "services")
	}
	if subnets[0].CIDR.String() != "2001:db8::/64" {
		t.Errorf("subnets[0].CIDR = %q, want %q", subnets[0].CIDR.String(), "2001:db8::/64")
	}

	// Check second subnet
	if subnets[1].Name != "pods" {
		t.Errorf("subnets[1].Name = %q, want %q", subnets[1].Name, "pods")
	}
	if subnets[1].CIDR.String() != "2001:db8:0:1::/64" {
		t.Errorf("subnets[1].CIDR = %q, want %q", subnets[1].CIDR.String(), "2001:db8:0:1::/64")
	}

	// Check third subnet
	if subnets[2].Name != "loadbalancers" {
		t.Errorf("subnets[2].Name = %q, want %q", subnets[2].Name, "loadbalancers")
	}
	if subnets[2].CIDR.String() != "2001:db8:0:100::/64" {
		t.Errorf("subnets[2].CIDR = %q, want %q", subnets[2].CIDR.String(), "2001:db8:0:100::/64")
	}
}

func TestCalculateSubnets_IPv4Error(t *testing.T) {
	basePrefix := netip.MustParsePrefix("192.168.1.0/24")

	configs := []SubnetConfig{
		{
			Name:         "test",
			Offset:       0,
			PrefixLength: 28,
		},
	}

	_, err := CalculateSubnets(basePrefix, configs)
	if err == nil {
		t.Error("CalculateSubnets() with IPv4 should return error")
	}
}

func TestValidateSubnetFitsInPrefix(t *testing.T) {
	tests := []struct {
		name       string
		basePrefix string
		config     SubnetConfig
		wantErr    bool
	}{
		{
			name:       "first subnet fits",
			basePrefix: "2001:db8::/48",
			config: SubnetConfig{
				Name:         "test",
				Offset:       0,
				PrefixLength: 64,
			},
			wantErr: false,
		},
		{
			name:       "last valid /64 in /48 fits",
			basePrefix: "2001:db8::/48",
			config: SubnetConfig{
				Name:         "test",
				Offset:       65535, // Last /64 in a /48 (2^16 - 1)
				PrefixLength: 64,
			},
			wantErr: false,
		},
		{
			name:       "subnet outside base prefix",
			basePrefix: "2001:db8::/48",
			config: SubnetConfig{
				Name:         "test",
				Offset:       65536, // One past the last valid /64
				PrefixLength: 64,
			},
			wantErr: true,
		},
		{
			name:       "very large offset overflows",
			basePrefix: "2001:db8::/60",
			config: SubnetConfig{
				Name:         "test",
				Offset:       0x7FFFFFFFFFFFFFFF,
				PrefixLength: 64,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			basePrefix := netip.MustParsePrefix(tt.basePrefix)
			err := ValidateSubnetFitsInPrefix(basePrefix, tt.config)

			if tt.wantErr && err == nil {
				t.Error("ValidateSubnetFitsInPrefix() expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateSubnetFitsInPrefix() unexpected error: %v", err)
			}
		})
	}
}

func TestParsePrefix(t *testing.T) {
	tests := []struct {
		name    string
		cidr    string
		want    string
		wantErr bool
	}{
		{
			name:    "valid IPv6 already normalized",
			cidr:    "2001:db8::/32",
			want:    "2001:db8::/32",
			wantErr: false,
		},
		{
			name:    "valid IPv6 normalized to network address",
			cidr:    "2001:db8:1234:5678::1/64",
			want:    "2001:db8:1234:5678::/64",
			wantErr: false,
		},
		{
			name:    "IPv6 with host bits gets masked",
			cidr:    "2001:db8::ffff/48",
			want:    "2001:db8::/48",
			wantErr: false,
		},
		{
			name:    "invalid CIDR",
			cidr:    "not-a-cidr",
			wantErr: true,
		},
		{
			name:    "missing prefix length",
			cidr:    "2001:db8::",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePrefix(tt.cidr)

			if tt.wantErr {
				if err == nil {
					t.Error("ParsePrefix() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("ParsePrefix() unexpected error: %v", err)
				return
			}

			if got.String() != tt.want {
				t.Errorf("ParsePrefix() = %q, want %q", got.String(), tt.want)
			}
		})
	}
}
