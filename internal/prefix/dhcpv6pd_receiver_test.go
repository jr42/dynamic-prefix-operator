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
)

func TestNewDHCPv6PDReceiver(t *testing.T) {
	tests := []struct {
		name                  string
		iface                 string
		requestedPrefixLength int
		expectedPrefixLength  int
	}{
		{
			name:                  "With explicit prefix length",
			iface:                 "eth0",
			requestedPrefixLength: 48,
			expectedPrefixLength:  48,
		},
		{
			name:                  "With default prefix length",
			iface:                 "eth1",
			requestedPrefixLength: 0,
			expectedPrefixLength:  56, // Default
		},
		{
			name:                  "Custom prefix length /60",
			iface:                 "enp0s3",
			requestedPrefixLength: 60,
			expectedPrefixLength:  60,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewDHCPv6PDReceiver(tt.iface, tt.requestedPrefixLength)

			if r.iface != tt.iface {
				t.Errorf("iface = %s, want %s", r.iface, tt.iface)
			}

			if r.requestedPrefixLength != tt.expectedPrefixLength {
				t.Errorf("requestedPrefixLength = %d, want %d", r.requestedPrefixLength, tt.expectedPrefixLength)
			}

			if r.events == nil {
				t.Error("events channel should not be nil")
			}

			if r.stopCh == nil {
				t.Error("stopCh should not be nil")
			}
		})
	}
}

func TestDHCPv6PDReceiverSource(t *testing.T) {
	r := NewDHCPv6PDReceiver("eth0", 56)
	if r.Source() != SourceDHCPv6PD {
		t.Errorf("Source() = %v, want %v", r.Source(), SourceDHCPv6PD)
	}
}

func TestDHCPv6PDReceiverInitialState(t *testing.T) {
	r := NewDHCPv6PDReceiver("eth0", 56)

	if r.CurrentPrefix() != nil {
		t.Error("Expected CurrentPrefix() to be nil initially")
	}

	if r.Events() == nil {
		t.Error("Expected Events() channel to be non-nil")
	}
}

func TestDHCPv6PDReceiverEventChannel(t *testing.T) {
	r := NewDHCPv6PDReceiver("eth0", 56)

	events := r.Events()
	if cap(events) != 10 {
		t.Errorf("Events channel capacity = %d, want 10", cap(events))
	}
}

func TestDHCPv6PDReceiverStopWithoutStart(t *testing.T) {
	r := NewDHCPv6PDReceiver("eth0", 56)

	// Stop should not panic when called without Start
	err := r.Stop()
	if err != nil {
		t.Errorf("Stop() returned error: %v", err)
	}
}
