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

func TestCalculateAddressRange(t *testing.T) {
	tests := []struct {
		name        string
		basePrefix  string
		config      AddressRangeConfig
		wantStart   string
		wantEnd     string
		wantErr     bool
		errContains string
	}{
		{
			name:       "simple range in /64",
			basePrefix: "2001:db8:abcd:1::/64",
			config: AddressRangeConfig{
				Name:  "lb-pool",
				Start: "::f000:0:0:0",
				End:   "::ffff:ffff:ffff:ffff",
			},
			wantStart: "2001:db8:abcd:1:f000::",
			wantEnd:   "2001:db8:abcd:1:ffff:ffff:ffff:ffff",
			wantErr:   false,
		},
		{
			name:       "small range at end of /64",
			basePrefix: "2001:db8:abcd:1::/64",
			config: AddressRangeConfig{
				Name:  "small",
				Start: "::ffff:ff00:0:0",
				End:   "::ffff:ffff:ffff:ffff",
			},
			wantStart: "2001:db8:abcd:1:ffff:ff00::",
			wantEnd:   "2001:db8:abcd:1:ffff:ffff:ffff:ffff",
			wantErr:   false,
		},
		{
			name:       "range with specific addresses",
			basePrefix: "2001:db8::/32",
			config: AddressRangeConfig{
				Name:  "specific",
				Start: "::1:0:0:0:1",
				End:   "::1:0:0:0:100",
			},
			wantStart: "2001:db8:0:1::1",
			wantEnd:   "2001:db8:0:1::100",
			wantErr:   false,
		},
		{
			name:       "start greater than end",
			basePrefix: "2001:db8:abcd:1::/64",
			config: AddressRangeConfig{
				Name:  "invalid",
				Start: "::ffff:0:0:0",
				End:   "::f000:0:0:0",
			},
			wantErr:     true,
			errContains: "greater than end",
		},
		{
			name:       "invalid start suffix",
			basePrefix: "2001:db8:abcd:1::/64",
			config: AddressRangeConfig{
				Name:  "invalid",
				Start: "not-an-address",
				End:   "::ffff::",
			},
			wantErr:     true,
			errContains: "invalid start offset",
		},
		{
			name:       "invalid end suffix",
			basePrefix: "2001:db8:abcd:1::/64",
			config: AddressRangeConfig{
				Name:  "invalid",
				Start: "::1",
				End:   "not-an-address",
			},
			wantErr:     true,
			errContains: "invalid end offset",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			basePrefix := netip.MustParsePrefix(tt.basePrefix)
			result, err := CalculateAddressRange(basePrefix, tt.config)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.errContains)
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if result.Start.String() != tt.wantStart {
				t.Errorf("Start = %s, want %s", result.Start, tt.wantStart)
			}
			if result.End.String() != tt.wantEnd {
				t.Errorf("End = %s, want %s", result.End, tt.wantEnd)
			}
			if result.Name != tt.config.Name {
				t.Errorf("Name = %s, want %s", result.Name, tt.config.Name)
			}
		})
	}
}

func TestCalculateAddressRanges(t *testing.T) {
	basePrefix := netip.MustParsePrefix("2001:db8:abcd:1::/64")
	configs := []AddressRangeConfig{
		{
			Name:  "pool1",
			Start: "::f000:0:0:0",
			End:   "::f0ff:ffff:ffff:ffff",
		},
		{
			Name:  "pool2",
			Start: "::f100:0:0:0",
			End:   "::f1ff:ffff:ffff:ffff",
		},
	}

	results, err := CalculateAddressRanges(basePrefix, configs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Name != "pool1" {
		t.Errorf("first result name = %s, want pool1", results[0].Name)
	}
	if results[1].Name != "pool2" {
		t.Errorf("second result name = %s, want pool2", results[1].Name)
	}
}

func TestCalculateAddressRanges_IPv4Error(t *testing.T) {
	basePrefix := netip.MustParsePrefix("192.168.1.0/24")
	configs := []AddressRangeConfig{
		{Name: "test", Start: "::1", End: "::ff"},
	}

	_, err := CalculateAddressRanges(basePrefix, configs)
	if err == nil {
		t.Error("expected error for IPv4 prefix")
	}
}

func TestRangeToCIDR(t *testing.T) {
	tests := []struct {
		name     string
		start    string
		end      string
		wantCIDR string
	}{
		{
			name:     "aligned /120",
			start:    "2001:db8::f000",
			end:      "2001:db8::f0ff",
			wantCIDR: "2001:db8::f000/120",
		},
		{
			name:     "aligned /64",
			start:    "2001:db8:abcd:1::",
			end:      "2001:db8:abcd:1:ffff:ffff:ffff:ffff",
			wantCIDR: "2001:db8:abcd:1::/64",
		},
		{
			name:     "unaligned range",
			start:    "2001:db8::1",
			end:      "2001:db8::10",
			wantCIDR: "2001:db8::/123", // Smallest containing CIDR (covers 0-31)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := netip.MustParseAddr(tt.start)
			end := netip.MustParseAddr(tt.end)
			result := RangeToCIDR(start, end)

			if result.String() != tt.wantCIDR {
				t.Errorf("RangeToCIDR(%s, %s) = %s, want %s", tt.start, tt.end, result, tt.wantCIDR)
			}
		})
	}
}

func TestAddressCount(t *testing.T) {
	tests := []struct {
		name  string
		start string
		end   string
		want  uint64
	}{
		{
			name:  "single address",
			start: "2001:db8::1",
			end:   "2001:db8::1",
			want:  1,
		},
		{
			name:  "256 addresses",
			start: "2001:db8::0",
			end:   "2001:db8::ff",
			want:  256,
		},
		{
			name:  "4096 addresses",
			start: "2001:db8::f000",
			end:   "2001:db8::ffff",
			want:  4096,
		},
		{
			name:  "range too large",
			start: "2001:db8::",
			end:   "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff",
			want:  0, // Too large to represent
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := netip.MustParseAddr(tt.start)
			end := netip.MustParseAddr(tt.end)
			result := AddressCount(start, end)

			if result != tt.want {
				t.Errorf("AddressCount(%s, %s) = %d, want %d", tt.start, tt.end, result, tt.want)
			}
		})
	}
}

func TestParseOffsetSuffix(t *testing.T) {
	tests := []struct {
		name       string
		basePrefix string
		suffix     string
		want       string
		wantErr    bool
	}{
		{
			name:       "simple suffix in /64",
			basePrefix: "2001:db8:abcd:1::/64",
			suffix:     "::1",
			want:       "2001:db8:abcd:1::1",
		},
		{
			name:       "high suffix in /64",
			basePrefix: "2001:db8:abcd:1::/64",
			suffix:     "::f000:0:0:0",
			want:       "2001:db8:abcd:1:f000::",
		},
		{
			name:       "max suffix in /64",
			basePrefix: "2001:db8:abcd:1::/64",
			suffix:     "::ffff:ffff:ffff:ffff",
			want:       "2001:db8:abcd:1:ffff:ffff:ffff:ffff",
		},
		{
			name:       "suffix in /48",
			basePrefix: "2001:db8:abcd::/48",
			suffix:     "::ff:1:2:3:4",
			want:       "2001:db8:abcd:ff:1:2:3:4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			basePrefix := netip.MustParsePrefix(tt.basePrefix)
			result, err := parseOffsetSuffix(basePrefix, tt.suffix)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if result.String() != tt.want {
				t.Errorf("parseOffsetSuffix(%s, %s) = %s, want %s", tt.basePrefix, tt.suffix, result, tt.want)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
