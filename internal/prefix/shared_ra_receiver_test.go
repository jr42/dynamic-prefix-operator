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

	dynamicprefixiov1alpha1 "github.com/jr42/dynamic-prefix-operator/api/v1alpha1"
)

func TestDefaultReceiverFactory_SharesRAReceiverByInterface(t *testing.T) {
	ctx := context.Background()
	underlying := newInstrumentedReceiver(SourceRouterAdvertisement)
	created := 0

	factory := &DefaultReceiverFactory{
		newRAReceiver: func(iface string) Receiver {
			created++
			return underlying
		},
	}

	spec := dynamicprefixiov1alpha1.AcquisitionSpec{
		RouterAdvertisement: &dynamicprefixiov1alpha1.RouterAdvertisementSpec{
			Interface: "eth0",
			Enabled:   true,
		},
	}

	receiverA, err := factory.CreateReceiver(spec)
	if err != nil {
		t.Fatalf("CreateReceiver() A error = %v", err)
	}
	receiverB, err := factory.CreateReceiver(spec)
	if err != nil {
		t.Fatalf("CreateReceiver() B error = %v", err)
	}

	if created != 1 {
		t.Fatalf("created %d underlying RA receivers, want 1", created)
	}
	if receiverA == receiverB {
		t.Fatal("expected distinct subscriber receivers")
	}

	if err := receiverA.Start(ctx); err != nil {
		t.Fatalf("receiverA.Start() error = %v", err)
	}
	if err := receiverB.Start(ctx); err != nil {
		t.Fatalf("receiverB.Start() error = %v", err)
	}
	if underlying.startCount != 1 {
		t.Fatalf("underlying start count = %d, want 1", underlying.startCount)
	}

	prefix := &Prefix{
		Network:           netip.MustParsePrefix("2001:db8::/48"),
		ValidLifetime:     time.Hour,
		PreferredLifetime: time.Hour,
		Source:            SourceRouterAdvertisement,
		ReceivedAt:        time.Now(),
	}
	underlying.setPrefix(prefix)
	underlying.emit(Event{Type: EventTypeAcquired, Prefix: prefix})

	expectEvent(t, receiverA.Events(), EventTypeAcquired, prefix.Network)
	expectEvent(t, receiverB.Events(), EventTypeAcquired, prefix.Network)

	if receiverA.CurrentPrefix().Network != prefix.Network {
		t.Fatalf("receiverA.CurrentPrefix() = %v, want %v", receiverA.CurrentPrefix().Network, prefix.Network)
	}
	if receiverB.CurrentPrefix().Network != prefix.Network {
		t.Fatalf("receiverB.CurrentPrefix() = %v, want %v", receiverB.CurrentPrefix().Network, prefix.Network)
	}

	if err := receiverA.Stop(); err != nil {
		t.Fatalf("receiverA.Stop() error = %v", err)
	}
	if underlying.stopCount != 0 {
		t.Fatalf("underlying stopped while a subscriber remained: %d", underlying.stopCount)
	}

	if err := receiverB.Stop(); err != nil {
		t.Fatalf("receiverB.Stop() error = %v", err)
	}
	if underlying.stopCount != 1 {
		t.Fatalf("underlying stop count = %d, want 1", underlying.stopCount)
	}
}

func TestDefaultReceiverFactory_CreatesSeparateRAReceiversForDifferentInterfaces(t *testing.T) {
	createdByInterface := map[string]int{}
	factory := &DefaultReceiverFactory{
		newRAReceiver: func(iface string) Receiver {
			createdByInterface[iface]++
			return newInstrumentedReceiver(SourceRouterAdvertisement)
		},
	}

	for _, iface := range []string{"eth0", "eth1", "eth0"} {
		_, err := factory.CreateReceiver(dynamicprefixiov1alpha1.AcquisitionSpec{
			RouterAdvertisement: &dynamicprefixiov1alpha1.RouterAdvertisementSpec{
				Interface: iface,
				Enabled:   true,
			},
		})
		if err != nil {
			t.Fatalf("CreateReceiver(%s) error = %v", iface, err)
		}
	}

	if createdByInterface["eth0"] != 1 {
		t.Fatalf("eth0 created %d receivers, want 1", createdByInterface["eth0"])
	}
	if createdByInterface["eth1"] != 1 {
		t.Fatalf("eth1 created %d receivers, want 1", createdByInterface["eth1"])
	}
}

func expectEvent(t *testing.T, events <-chan Event, eventType EventType, network netip.Prefix) {
	t.Helper()

	select {
	case event := <-events:
		if event.Type != eventType {
			t.Fatalf("event.Type = %s, want %s", event.Type, eventType)
		}
		if event.Prefix == nil || event.Prefix.Network != network {
			t.Fatalf("event.Prefix = %v, want %v", event.Prefix, network)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s event", eventType)
	}
}

type instrumentedReceiver struct {
	source        Source
	events        chan Event
	currentPrefix *Prefix
	startCount    int
	stopCount     int
	started       bool
}

func newInstrumentedReceiver(source Source) *instrumentedReceiver {
	return &instrumentedReceiver{
		source: source,
		events: make(chan Event, 10),
	}
}

func (r *instrumentedReceiver) Start(ctx context.Context) error {
	r.startCount++
	r.started = true
	return nil
}

func (r *instrumentedReceiver) Stop() error {
	r.stopCount++
	r.started = false
	return nil
}

func (r *instrumentedReceiver) Events() <-chan Event {
	return r.events
}

func (r *instrumentedReceiver) CurrentPrefix() *Prefix {
	return r.currentPrefix
}

func (r *instrumentedReceiver) Source() Source {
	return r.source
}

func (r *instrumentedReceiver) setPrefix(prefix *Prefix) {
	r.currentPrefix = prefix
}

func (r *instrumentedReceiver) emit(event Event) {
	r.events <- event
}
