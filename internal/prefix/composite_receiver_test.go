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
	"context"
	"net/netip"
	"testing"
	"time"
)

func TestCompositeReceiver_Source(t *testing.T) {
	primary := NewMockReceiver(SourceDHCPv6PD)
	fallback := NewMockReceiver(SourceRouterAdvertisement)
	composite := NewCompositeReceiver(primary, fallback)

	// Initially should return primary source
	if composite.Source() != SourceDHCPv6PD {
		t.Errorf("Source() = %v, want %v", composite.Source(), SourceDHCPv6PD)
	}
}

func TestCompositeReceiver_CurrentPrefix(t *testing.T) {
	primary := NewMockReceiver(SourceDHCPv6PD)
	fallback := NewMockReceiver(SourceRouterAdvertisement)
	composite := NewCompositeReceiver(primary, fallback)

	// Initially no prefix
	if composite.CurrentPrefix() != nil {
		t.Error("Expected nil prefix initially")
	}

	// Simulate primary getting a prefix
	primaryPrefix := netip.MustParsePrefix("2001:db8:1::/48")
	primary.SimulatePrefix(primaryPrefix, time.Hour)

	if composite.CurrentPrefix() == nil {
		t.Error("Expected non-nil prefix after primary acquisition")
	}

	if composite.CurrentPrefix().Network != primaryPrefix {
		t.Errorf("CurrentPrefix().Network = %v, want %v", composite.CurrentPrefix().Network, primaryPrefix)
	}

	// Simulate fallback getting a different prefix
	fallbackPrefix := netip.MustParsePrefix("2001:db8:2::/48")
	fallback.SimulatePrefix(fallbackPrefix, time.Hour)

	// Should still prefer primary
	if composite.CurrentPrefix().Network != primaryPrefix {
		t.Errorf("CurrentPrefix().Network = %v, want %v (should prefer primary)", composite.CurrentPrefix().Network, primaryPrefix)
	}

	// Clear primary prefix
	primary.SimulatePrefixExpiry()

	// Should now return fallback
	if composite.CurrentPrefix() == nil {
		t.Error("Expected non-nil prefix from fallback")
	}

	if composite.CurrentPrefix().Network != fallbackPrefix {
		t.Errorf("CurrentPrefix().Network = %v, want %v", composite.CurrentPrefix().Network, fallbackPrefix)
	}
}

func TestCompositeReceiver_StartStop(t *testing.T) {
	primary := NewMockReceiver(SourceDHCPv6PD)
	fallback := NewMockReceiver(SourceRouterAdvertisement)
	composite := NewCompositeReceiver(primary, fallback)

	ctx := context.Background()

	// Start should start both receivers
	if err := composite.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if !primary.IsStarted() {
		t.Error("Primary should be started")
	}

	if !fallback.IsStarted() {
		t.Error("Fallback should be started")
	}

	// Stop should stop both receivers
	if err := composite.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	if primary.IsStarted() {
		t.Error("Primary should be stopped")
	}

	if fallback.IsStarted() {
		t.Error("Fallback should be stopped")
	}
}

func TestCompositeReceiver_IsUsingFallback(t *testing.T) {
	primary := NewMockReceiver(SourceDHCPv6PD)
	fallback := NewMockReceiver(SourceRouterAdvertisement)
	composite := NewCompositeReceiver(primary, fallback)

	// Initially using primary
	if composite.IsUsingFallback() {
		t.Error("Should not be using fallback initially")
	}
}

func TestCompositeReceiver_EventChannel(t *testing.T) {
	primary := NewMockReceiver(SourceDHCPv6PD)
	fallback := NewMockReceiver(SourceRouterAdvertisement)
	composite := NewCompositeReceiver(primary, fallback)

	events := composite.Events()
	if events == nil {
		t.Error("Events channel should not be nil")
	}

	if cap(events) != 10 {
		t.Errorf("Events channel capacity = %d, want 10", cap(events))
	}
}
