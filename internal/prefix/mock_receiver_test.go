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
	"errors"
	"net/netip"
	"testing"
	"time"
)

func TestMockReceiver_StartStop(t *testing.T) {
	receiver := NewMockReceiver(SourceDHCPv6PD)

	if receiver.IsStarted() {
		t.Error("receiver should not be started initially")
	}

	err := receiver.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	if !receiver.IsStarted() {
		t.Error("receiver should be started after Start()")
	}

	err = receiver.Stop()
	if err != nil {
		t.Fatalf("Stop() error: %v", err)
	}

	if receiver.IsStarted() {
		t.Error("receiver should not be started after Stop()")
	}
}

func TestMockReceiver_SimulatePrefix(t *testing.T) {
	receiver := NewMockReceiver(SourceDHCPv6PD)

	prefix := netip.MustParsePrefix("2001:db8::/60")

	// Initially no prefix
	if receiver.CurrentPrefix() != nil {
		t.Error("CurrentPrefix() should be nil initially")
	}

	// Simulate receiving a prefix
	receiver.SimulatePrefix(prefix, time.Hour)

	// Check current prefix
	current := receiver.CurrentPrefix()
	if current == nil {
		t.Fatal("CurrentPrefix() should not be nil after SimulatePrefix()")
	}

	if current.Network != prefix {
		t.Errorf("CurrentPrefix().Network = %s, want %s", current.Network, prefix)
	}

	if current.Source != SourceDHCPv6PD {
		t.Errorf("CurrentPrefix().Source = %s, want %s", current.Source, SourceDHCPv6PD)
	}

	// Check event was emitted
	select {
	case event := <-receiver.Events():
		if event.Type != EventTypeAcquired {
			t.Errorf("event.Type = %s, want %s", event.Type, EventTypeAcquired)
		}
		if event.Prefix.Network != prefix {
			t.Errorf("event.Prefix.Network = %s, want %s", event.Prefix.Network, prefix)
		}
	case <-time.After(time.Second):
		t.Error("expected event to be emitted")
	}
}

func TestMockReceiver_SimulatePrefixChange(t *testing.T) {
	receiver := NewMockReceiver(SourceDHCPv6PD)

	prefix1 := netip.MustParsePrefix("2001:db8:1::/60")
	prefix2 := netip.MustParsePrefix("2001:db8:2::/60")

	// First prefix
	receiver.SimulatePrefix(prefix1, time.Hour)
	<-receiver.Events() // drain acquired event

	// Change prefix
	receiver.SimulatePrefix(prefix2, time.Hour)

	// Check event type is changed
	select {
	case event := <-receiver.Events():
		if event.Type != EventTypeChanged {
			t.Errorf("event.Type = %s, want %s", event.Type, EventTypeChanged)
		}
		if event.Prefix.Network != prefix2 {
			t.Errorf("event.Prefix.Network = %s, want %s", event.Prefix.Network, prefix2)
		}
	case <-time.After(time.Second):
		t.Error("expected event to be emitted")
	}
}

func TestMockReceiver_SimulatePrefixRenewal(t *testing.T) {
	receiver := NewMockReceiver(SourceDHCPv6PD)

	prefix := netip.MustParsePrefix("2001:db8::/60")

	// First acquisition
	receiver.SimulatePrefix(prefix, time.Hour)
	<-receiver.Events() // drain acquired event

	// Renewal (same prefix)
	receiver.SimulatePrefix(prefix, 2*time.Hour)

	// Check event type is renewed
	select {
	case event := <-receiver.Events():
		if event.Type != EventTypeRenewed {
			t.Errorf("event.Type = %s, want %s", event.Type, EventTypeRenewed)
		}
	case <-time.After(time.Second):
		t.Error("expected event to be emitted")
	}
}

func TestMockReceiver_SimulatePrefixExpiry(t *testing.T) {
	receiver := NewMockReceiver(SourceDHCPv6PD)

	prefix := netip.MustParsePrefix("2001:db8::/60")

	// Acquire prefix
	receiver.SimulatePrefix(prefix, time.Hour)
	<-receiver.Events() // drain acquired event

	// Expire prefix
	receiver.SimulatePrefixExpiry()

	// Check current prefix is nil
	if receiver.CurrentPrefix() != nil {
		t.Error("CurrentPrefix() should be nil after expiry")
	}

	// Check event
	select {
	case event := <-receiver.Events():
		if event.Type != EventTypeExpired {
			t.Errorf("event.Type = %s, want %s", event.Type, EventTypeExpired)
		}
	case <-time.After(time.Second):
		t.Error("expected event to be emitted")
	}
}

func TestMockReceiver_SimulateError(t *testing.T) {
	receiver := NewMockReceiver(SourceDHCPv6PD)

	testErr := errors.New("test error")
	receiver.SimulateError(testErr)

	select {
	case event := <-receiver.Events():
		if event.Type != EventTypeFailed {
			t.Errorf("event.Type = %s, want %s", event.Type, EventTypeFailed)
		}
		if event.Error != testErr {
			t.Errorf("event.Error = %v, want %v", event.Error, testErr)
		}
	case <-time.After(time.Second):
		t.Error("expected event to be emitted")
	}
}

func TestMockReceiver_Source(t *testing.T) {
	tests := []struct {
		source Source
	}{
		{SourceDHCPv6PD},
		{SourceRouterAdvertisement},
		{SourceStatic},
	}

	for _, tt := range tests {
		t.Run(string(tt.source), func(t *testing.T) {
			receiver := NewMockReceiver(tt.source)
			if receiver.Source() != tt.source {
				t.Errorf("Source() = %s, want %s", receiver.Source(), tt.source)
			}
		})
	}
}

func TestMockISP_DelegatePrefix(t *testing.T) {
	initialPrefix := netip.MustParsePrefix("2001:db8:1234::/48")
	leaseTime := time.Hour

	isp := NewMockISP(initialPrefix, leaseTime)

	// Delegate prefix
	prefix, lease, err := isp.DelegatePrefix(60)
	if err != nil {
		t.Fatalf("DelegatePrefix() error: %v", err)
	}

	if prefix != initialPrefix {
		t.Errorf("DelegatePrefix() prefix = %s, want %s", prefix, initialPrefix)
	}

	if lease != leaseTime {
		t.Errorf("DelegatePrefix() lease = %v, want %v", lease, leaseTime)
	}
}

func TestMockISP_ChangePrefix(t *testing.T) {
	initialPrefix := netip.MustParsePrefix("2001:db8:1::/48")
	newPrefix := netip.MustParsePrefix("2001:db8:2::/48")

	isp := NewMockISP(initialPrefix, time.Hour)

	// Initial prefix
	if isp.GetCurrentPrefix() != initialPrefix {
		t.Errorf("GetCurrentPrefix() = %s, want %s", isp.GetCurrentPrefix(), initialPrefix)
	}

	// Change prefix
	isp.ChangePrefix(newPrefix)

	// Check new prefix
	if isp.GetCurrentPrefix() != newPrefix {
		t.Errorf("GetCurrentPrefix() = %s, want %s", isp.GetCurrentPrefix(), newPrefix)
	}
}

func TestMockISP_PrefixChangeFn(t *testing.T) {
	initialPrefix := netip.MustParsePrefix("2001:db8:1::/48")
	changedPrefix := netip.MustParsePrefix("2001:db8:99::/48")

	isp := NewMockISP(initialPrefix, time.Hour)

	called := false
	isp.SetPrefixChangeFn(func() netip.Prefix {
		if !called {
			called = true
			return changedPrefix
		}
		return isp.GetCurrentPrefix()
	})

	// First delegation should trigger the change function
	prefix, _, err := isp.DelegatePrefix(60)
	if err != nil {
		t.Fatalf("DelegatePrefix() error: %v", err)
	}

	if prefix != changedPrefix {
		t.Errorf("DelegatePrefix() prefix = %s, want %s", prefix, changedPrefix)
	}
}
