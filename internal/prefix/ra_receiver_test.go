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

func TestIsGlobalUnicast(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		expected bool
	}{
		{
			name:     "GUA 2001:db8::1",
			addr:     "2001:db8::1",
			expected: true,
		},
		{
			name:     "GUA 2620:fe::fe",
			addr:     "2620:fe::fe",
			expected: true,
		},
		{
			name:     "GUA 2000::1",
			addr:     "2000::1",
			expected: true,
		},
		{
			name:     "GUA 3fff:ffff::1 (edge of range)",
			addr:     "3fff:ffff::1",
			expected: true,
		},
		{
			name:     "ULA fd00::1",
			addr:     "fd00::1",
			expected: false,
		},
		{
			name:     "ULA fc00::1",
			addr:     "fc00::1",
			expected: false,
		},
		{
			name:     "Link-local fe80::1",
			addr:     "fe80::1",
			expected: false,
		},
		{
			name:     "Loopback ::1",
			addr:     "::1",
			expected: false,
		},
		{
			name:     "Multicast ff02::1",
			addr:     "ff02::1",
			expected: false,
		},
		{
			name:     "IPv4 mapped",
			addr:     "::ffff:192.0.2.1",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := netip.MustParseAddr(tt.addr)
			result := isGlobalUnicast(addr)
			if result != tt.expected {
				t.Errorf("isGlobalUnicast(%s) = %v, want %v", tt.addr, result, tt.expected)
			}
		})
	}
}

func TestIsULA(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		expected bool
	}{
		{
			name:     "ULA fd00::1",
			addr:     "fd00::1",
			expected: true,
		},
		{
			name:     "ULA fc00::1",
			addr:     "fc00::1",
			expected: true,
		},
		{
			name:     "ULA fdab:cdef:1234::1",
			addr:     "fdab:cdef:1234::1",
			expected: true,
		},
		{
			name:     "GUA 2001:db8::1",
			addr:     "2001:db8::1",
			expected: false,
		},
		{
			name:     "Link-local fe80::1",
			addr:     "fe80::1",
			expected: false,
		},
		{
			name:     "Not ULA fb00::1",
			addr:     "fb00::1",
			expected: false,
		},
		{
			name:     "Not ULA fe00::1",
			addr:     "fe00::1",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := netip.MustParseAddr(tt.addr)
			result := isULA(addr)
			if result != tt.expected {
				t.Errorf("isULA(%s) = %v, want %v", tt.addr, result, tt.expected)
			}
		})
	}
}

func TestIsLinkLocal(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		expected bool
	}{
		{
			name:     "Link-local fe80::1",
			addr:     "fe80::1",
			expected: true,
		},
		{
			name:     "Link-local fe80::abcd:1234",
			addr:     "fe80::abcd:1234",
			expected: true,
		},
		{
			name:     "GUA 2001:db8::1",
			addr:     "2001:db8::1",
			expected: false,
		},
		{
			name:     "ULA fd00::1",
			addr:     "fd00::1",
			expected: false,
		},
		{
			name:     "Not link-local fec0::1",
			addr:     "fec0::1",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := netip.MustParseAddr(tt.addr)
			result := isLinkLocal(addr)
			if result != tt.expected {
				t.Errorf("isLinkLocal(%s) = %v, want %v", tt.addr, result, tt.expected)
			}
		})
	}
}

func TestRAReceiverSource(t *testing.T) {
	r := NewRAReceiver("eth0")
	if r.Source() != SourceRouterAdvertisement {
		t.Errorf("Source() = %v, want %v", r.Source(), SourceRouterAdvertisement)
	}
}

func TestRAReceiverInitialState(t *testing.T) {
	r := NewRAReceiver("eth0")

	if r.CurrentPrefix() != nil {
		t.Error("Expected CurrentPrefix() to be nil initially")
	}

	// Events channel should be available
	if r.Events() == nil {
		t.Error("Expected Events() channel to be non-nil")
	}
}

func TestRAReceiverEventChannel(t *testing.T) {
	r := NewRAReceiver("eth0")

	// Verify the event channel is buffered
	events := r.Events()
	if cap(events) != 10 {
		t.Errorf("Events channel capacity = %d, want 10", cap(events))
	}
}
