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

package integration

import (
	"net/netip"
	"testing"
	"time"

	"github.com/jr42/dynamic-prefix-operator/internal/prefix"
)

// TestISPSimulation_PrefixDelegation tests the full ISP prefix delegation flow
func TestISPSimulation_PrefixDelegation(t *testing.T) {
	// Create a mock ISP that provides a /48 prefix
	initialPrefix := netip.MustParsePrefix("2001:db8:1234::/48")
	leaseTime := time.Hour
	isp := prefix.NewMockISP(initialPrefix, leaseTime)

	// Customer requests a /60 prefix (common for residential)
	requestedLength := 60
	delegatedPrefix, lease, err := isp.DelegatePrefix(requestedLength)
	if err != nil {
		t.Fatalf("Failed to delegate prefix: %v", err)
	}

	t.Logf("Received delegated prefix: %s with lease %v", delegatedPrefix, lease)

	if delegatedPrefix != initialPrefix {
		t.Errorf("Expected prefix %s, got %s", initialPrefix, delegatedPrefix)
	}

	if lease != leaseTime {
		t.Errorf("Expected lease %v, got %v", leaseTime, lease)
	}
}

// TestISPSimulation_PrefixChange tests the ISP changing the customer's prefix
func TestISPSimulation_PrefixChange(t *testing.T) {
	// Initial prefix
	prefix1 := netip.MustParsePrefix("2001:db8:1::/48")
	isp := prefix.NewMockISP(prefix1, time.Hour)

	// Get initial prefix
	delegated1, _, _ := isp.DelegatePrefix(60)
	t.Logf("Initial prefix: %s", delegated1)

	// ISP changes the prefix (simulating a reconnect or ISP-side change)
	prefix2 := netip.MustParsePrefix("2001:db8:2::/48")
	isp.ChangePrefix(prefix2)

	// Get new prefix
	delegated2, _, _ := isp.DelegatePrefix(60)
	t.Logf("New prefix after change: %s", delegated2)

	if delegated2 != prefix2 {
		t.Errorf("Expected new prefix %s, got %s", prefix2, delegated2)
	}

	if delegated1 == delegated2 {
		t.Error("Prefix should have changed")
	}
}

// TestISPSimulation_DynamicPrefixScenario tests a realistic ISP behavior scenario
func TestISPSimulation_DynamicPrefixScenario(t *testing.T) {
	// Simulate a typical ISP that gives customers dynamic prefixes
	// ISP has a pool of /48 prefixes to delegate

	// Customer connects, gets first prefix
	prefix1 := netip.MustParsePrefix("2001:db8:abcd::/48")
	leaseTime := 2 * time.Hour
	isp := prefix.NewMockISP(prefix1, leaseTime)

	// Create a receiver to simulate the customer's DHCPv6-PD client
	receiver := prefix.NewMockReceiver(prefix.SourceDHCPv6PD)

	// Customer receives the prefix
	delegated, lease, err := isp.DelegatePrefix(60)
	if err != nil {
		t.Fatalf("Failed to delegate prefix: %v", err)
	}

	// Feed the prefix to the receiver (simulating DHCPv6-PD response)
	receiver.SimulatePrefix(delegated, lease)

	// Verify the receiver has the prefix
	current := receiver.CurrentPrefix()
	if current == nil {
		t.Fatal("Receiver should have a prefix")
	}

	if current.Network != delegated {
		t.Errorf("Receiver prefix %s doesn't match delegated %s", current.Network, delegated)
	}

	t.Logf("Customer received prefix: %s (lease: %v)", current.Network, current.ValidLifetime)

	// Simulate ISP-initiated prefix change (e.g., after router reboot)
	prefix2 := netip.MustParsePrefix("2001:db8:ef01::/48")
	isp.ChangePrefix(prefix2)

	// Customer re-requests prefix (e.g., lease renewal)
	delegated2, lease2, err := isp.DelegatePrefix(60)
	if err != nil {
		t.Fatalf("Failed to get new prefix: %v", err)
	}

	receiver.SimulatePrefix(delegated2, lease2)

	// Drain events
	<-receiver.Events() // acquired
	<-receiver.Events() // changed

	// Verify the receiver has the new prefix
	current = receiver.CurrentPrefix()
	if current.Network != delegated2 {
		t.Errorf("Expected new prefix %s, got %s", delegated2, current.Network)
	}

	t.Logf("Customer received new prefix: %s", current.Network)
}

// TestISPSimulation_SubnetCalculation tests subnet calculation from delegated prefix
func TestISPSimulation_SubnetCalculation(t *testing.T) {
	// ISP delegates a /48 prefix
	delegatedPrefix := netip.MustParsePrefix("2001:db8:cafe::/48")
	isp := prefix.NewMockISP(delegatedPrefix, time.Hour)

	delegated, _, err := isp.DelegatePrefix(48)
	if err != nil {
		t.Fatalf("Failed to delegate: %v", err)
	}

	// Customer wants to create multiple /64 subnets from the /48
	subnetConfigs := []prefix.SubnetConfig{
		{Name: "services", Offset: 0, PrefixLength: 64},
		{Name: "pods", Offset: 1, PrefixLength: 64},
		{Name: "loadbalancers", Offset: 256, PrefixLength: 64},
	}

	subnets, err := prefix.CalculateSubnets(delegated, subnetConfigs)
	if err != nil {
		t.Fatalf("Failed to calculate subnets: %v", err)
	}

	expected := map[string]string{
		"services":      "2001:db8:cafe::/64",
		"pods":          "2001:db8:cafe:1::/64",
		"loadbalancers": "2001:db8:cafe:100::/64",
	}

	for _, subnet := range subnets {
		expectedCIDR, ok := expected[subnet.Name]
		if !ok {
			t.Errorf("Unexpected subnet %s", subnet.Name)
			continue
		}
		if subnet.CIDR.String() != expectedCIDR {
			t.Errorf("Subnet %s: expected %s, got %s", subnet.Name, expectedCIDR, subnet.CIDR)
		}
		t.Logf("Subnet %s: %s", subnet.Name, subnet.CIDR)
	}
}

// TestISPSimulation_PrefixChangeWithSubnets tests how subnets change when prefix changes
func TestISPSimulation_PrefixChangeWithSubnets(t *testing.T) {
	// Initial prefix
	prefix1 := netip.MustParsePrefix("2001:db8:1::/48")
	isp := prefix.NewMockISP(prefix1, time.Hour)

	subnetConfigs := []prefix.SubnetConfig{
		{Name: "services", Offset: 0, PrefixLength: 64},
		{Name: "pods", Offset: 1, PrefixLength: 64},
	}

	// Calculate subnets with first prefix
	delegated1, _, _ := isp.DelegatePrefix(48)
	subnets1, err := prefix.CalculateSubnets(delegated1, subnetConfigs)
	if err != nil {
		t.Fatalf("Failed to calculate subnets: %v", err)
	}

	t.Log("Subnets with first prefix:")
	for _, s := range subnets1 {
		t.Logf("  %s: %s", s.Name, s.CIDR)
	}

	// ISP changes the prefix
	prefix2 := netip.MustParsePrefix("2001:db8:9::/48")
	isp.ChangePrefix(prefix2)

	// Calculate subnets with new prefix
	delegated2, _, _ := isp.DelegatePrefix(48)
	subnets2, err := prefix.CalculateSubnets(delegated2, subnetConfigs)
	if err != nil {
		t.Fatalf("Failed to calculate subnets: %v", err)
	}

	t.Log("Subnets with second prefix:")
	for _, s := range subnets2 {
		t.Logf("  %s: %s", s.Name, s.CIDR)
	}

	// Verify subnets changed
	if subnets1[0].CIDR == subnets2[0].CIDR {
		t.Error("Subnets should have changed when prefix changed")
	}

	// But the structure should be the same (same offset pattern)
	if subnets2[0].CIDR.String() != "2001:db8:9::/64" {
		t.Errorf("Expected services subnet 2001:db8:9::/64, got %s", subnets2[0].CIDR)
	}
	if subnets2[1].CIDR.String() != "2001:db8:9:1::/64" {
		t.Errorf("Expected pods subnet 2001:db8:9:1::/64, got %s", subnets2[1].CIDR)
	}
}

// TestISPSimulation_ReceiverEvents tests the event flow from prefix changes
func TestISPSimulation_ReceiverEvents(t *testing.T) {
	receiver := prefix.NewMockReceiver(prefix.SourceDHCPv6PD)

	prefix1 := netip.MustParsePrefix("2001:db8:1::/48")
	prefix2 := netip.MustParsePrefix("2001:db8:2::/48")

	// First prefix - should emit Acquired event
	receiver.SimulatePrefix(prefix1, time.Hour)

	select {
	case event := <-receiver.Events():
		if event.Type != prefix.EventTypeAcquired {
			t.Errorf("Expected Acquired event, got %s", event.Type)
		}
		t.Logf("Event: %s - %s", event.Type, event.Prefix.Network)
	case <-time.After(time.Second):
		t.Fatal("Timed out waiting for Acquired event")
	}

	// Same prefix with new lease - should emit Renewed event
	receiver.SimulatePrefix(prefix1, 2*time.Hour)

	select {
	case event := <-receiver.Events():
		if event.Type != prefix.EventTypeRenewed {
			t.Errorf("Expected Renewed event, got %s", event.Type)
		}
		t.Logf("Event: %s - %s", event.Type, event.Prefix.Network)
	case <-time.After(time.Second):
		t.Fatal("Timed out waiting for Renewed event")
	}

	// Different prefix - should emit Changed event
	receiver.SimulatePrefix(prefix2, time.Hour)

	select {
	case event := <-receiver.Events():
		if event.Type != prefix.EventTypeChanged {
			t.Errorf("Expected Changed event, got %s", event.Type)
		}
		t.Logf("Event: %s - %s", event.Type, event.Prefix.Network)
	case <-time.After(time.Second):
		t.Fatal("Timed out waiting for Changed event")
	}

	// Prefix expires
	receiver.SimulatePrefixExpiry()

	select {
	case event := <-receiver.Events():
		if event.Type != prefix.EventTypeExpired {
			t.Errorf("Expected Expired event, got %s", event.Type)
		}
		t.Logf("Event: %s", event.Type)
	case <-time.After(time.Second):
		t.Fatal("Timed out waiting for Expired event")
	}
}
