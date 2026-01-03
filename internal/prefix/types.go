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
	"time"
)

// Source indicates how a prefix was obtained
type Source string

const (
	SourceDHCPv6PD            Source = "dhcpv6-pd"
	SourceRouterAdvertisement Source = "router-advertisement"
	SourceStatic              Source = "static"
	SourceUnknown             Source = "unknown"
)

// Prefix represents an acquired IPv6 prefix with metadata
type Prefix struct {
	// Network is the IPv6 prefix
	Network netip.Prefix

	// ValidLifetime is how long this prefix is valid
	ValidLifetime time.Duration

	// PreferredLifetime is how long this prefix is preferred
	PreferredLifetime time.Duration

	// Source indicates how this prefix was obtained
	Source Source

	// ReceivedAt is when this prefix was received
	ReceivedAt time.Time
}

// Event represents a prefix-related event
type Event struct {
	// Type indicates what happened
	Type EventType

	// Prefix is the prefix involved (may be nil for some events)
	Prefix *Prefix

	// Error contains any error (for failure events)
	Error error
}

// EventType indicates the type of prefix event
type EventType string

const (
	EventTypeAcquired EventType = "acquired"
	EventTypeRenewed  EventType = "renewed"
	EventTypeChanged  EventType = "changed"
	EventTypeExpired  EventType = "expired"
	EventTypeFailed   EventType = "failed"
)

// Receiver is the interface for prefix acquisition implementations
type Receiver interface {
	// Start begins receiving prefixes
	Start(ctx context.Context) error

	// Stop stops receiving prefixes
	Stop() error

	// Events returns a channel of prefix events
	Events() <-chan Event

	// CurrentPrefix returns the current prefix, if any
	CurrentPrefix() *Prefix

	// Source returns the type of this receiver
	Source() Source
}
