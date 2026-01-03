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
	"sync"
	"time"
)

// MockReceiver is a test implementation of Receiver that allows manual control
type MockReceiver struct {
	mu            sync.RWMutex
	source        Source
	currentPrefix *Prefix
	events        chan Event
	started       bool
	stopCh        chan struct{}
}

// NewMockReceiver creates a new mock receiver
func NewMockReceiver(source Source) *MockReceiver {
	return &MockReceiver{
		source: source,
		events: make(chan Event, 10),
		stopCh: make(chan struct{}),
	}
}

// Start implements Receiver
func (m *MockReceiver) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	return nil
}

// Stop implements Receiver
func (m *MockReceiver) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = false
	close(m.stopCh)
	return nil
}

// Events implements Receiver
func (m *MockReceiver) Events() <-chan Event {
	return m.events
}

// CurrentPrefix implements Receiver
func (m *MockReceiver) CurrentPrefix() *Prefix {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentPrefix
}

// Source implements Receiver
func (m *MockReceiver) Source() Source {
	return m.source
}

// SimulatePrefix simulates receiving a new prefix (for testing)
func (m *MockReceiver) SimulatePrefix(prefix netip.Prefix, validLifetime time.Duration) {
	m.mu.Lock()
	oldPrefix := m.currentPrefix
	m.currentPrefix = &Prefix{
		Network:           prefix,
		ValidLifetime:     validLifetime,
		PreferredLifetime: validLifetime,
		Source:            m.source,
		ReceivedAt:        time.Now(),
	}
	m.mu.Unlock()

	eventType := EventTypeAcquired
	if oldPrefix != nil {
		if oldPrefix.Network != prefix {
			eventType = EventTypeChanged
		} else {
			eventType = EventTypeRenewed
		}
	}

	m.events <- Event{
		Type:   eventType,
		Prefix: m.currentPrefix,
	}
}

// SimulatePrefixExpiry simulates prefix expiration (for testing)
func (m *MockReceiver) SimulatePrefixExpiry() {
	m.mu.Lock()
	oldPrefix := m.currentPrefix
	m.currentPrefix = nil
	m.mu.Unlock()

	if oldPrefix != nil {
		m.events <- Event{
			Type:   EventTypeExpired,
			Prefix: oldPrefix,
		}
	}
}

// SimulateError simulates a receiver error (for testing)
func (m *MockReceiver) SimulateError(err error) {
	m.events <- Event{
		Type:  EventTypeFailed,
		Error: err,
	}
}

// IsStarted returns whether the receiver is started
func (m *MockReceiver) IsStarted() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.started
}

// MockISP simulates an ISP that assigns dynamic prefixes
type MockISP struct {
	mu              sync.RWMutex
	currentPrefix   netip.Prefix
	prefixLength    int
	leaseTime       time.Duration
	prefixChangesFn func() netip.Prefix
}

// NewMockISP creates a new mock ISP
func NewMockISP(initialPrefix netip.Prefix, leaseTime time.Duration) *MockISP {
	return &MockISP{
		currentPrefix: initialPrefix,
		prefixLength:  initialPrefix.Bits(),
		leaseTime:     leaseTime,
	}
}

// SetPrefixChangeFn sets a function to generate new prefixes (simulates ISP changing prefix)
func (isp *MockISP) SetPrefixChangeFn(fn func() netip.Prefix) {
	isp.mu.Lock()
	defer isp.mu.Unlock()
	isp.prefixChangesFn = fn
}

// GetCurrentPrefix returns the current prefix
func (isp *MockISP) GetCurrentPrefix() netip.Prefix {
	isp.mu.RLock()
	defer isp.mu.RUnlock()
	return isp.currentPrefix
}

// ChangePrefix simulates the ISP changing the prefix
func (isp *MockISP) ChangePrefix(newPrefix netip.Prefix) {
	isp.mu.Lock()
	defer isp.mu.Unlock()
	isp.currentPrefix = newPrefix
}

// GetLeaseTime returns the lease duration
func (isp *MockISP) GetLeaseTime() time.Duration {
	return isp.leaseTime
}

// DelegatePrefix simulates DHCPv6-PD prefix delegation
func (isp *MockISP) DelegatePrefix(requestedLength int) (netip.Prefix, time.Duration, error) {
	isp.mu.RLock()
	defer isp.mu.RUnlock()

	// If a prefix change function is set and returns a different prefix, use it
	if isp.prefixChangesFn != nil {
		newPrefix := isp.prefixChangesFn()
		if newPrefix.IsValid() && newPrefix != isp.currentPrefix {
			isp.mu.RUnlock()
			isp.mu.Lock()
			isp.currentPrefix = newPrefix
			isp.mu.Unlock()
			isp.mu.RLock()
		}
	}

	return isp.currentPrefix, isp.leaseTime, nil
}
