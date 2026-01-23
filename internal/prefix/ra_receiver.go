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
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/mdlayher/ndp"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// RAReceiver monitors Router Advertisements to passively detect IPv6 prefix changes.
// This is useful when another service (like Talos or systemd-networkd) is handling
// DHCPv6-PD and we just need to observe the prefix being used.
type RAReceiver struct {
	mu            sync.RWMutex
	iface         string
	conn          *ndp.Conn
	currentPrefix *Prefix
	events        chan Event
	stopCh        chan struct{}
	started       bool
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewRAReceiver creates a new Router Advertisement receiver for the given interface.
func NewRAReceiver(iface string) *RAReceiver {
	return &RAReceiver{
		iface:  iface,
		events: make(chan Event, 10),
		stopCh: make(chan struct{}),
	}
}

// Start begins listening for Router Advertisements on the configured interface.
func (r *RAReceiver) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return nil
	}

	log := logf.FromContext(ctx).WithName("ra-receiver")
	log.Info("Looking up interface", "name", r.iface)

	ifi, err := net.InterfaceByName(r.iface)
	if err != nil {
		return fmt.Errorf("failed to get interface %s: %w", r.iface, err)
	}

	log.Info("Found interface",
		"name", ifi.Name,
		"index", ifi.Index,
		"hwAddr", ifi.HardwareAddr.String(),
		"mtu", ifi.MTU,
		"flags", ifi.Flags.String())

	// Create NDP connection for listening to Router Advertisements
	conn, addr, err := ndp.Listen(ifi, ndp.LinkLocal)
	if err != nil {
		return fmt.Errorf("failed to create NDP listener on %s: %w", r.iface, err)
	}

	log.Info("NDP listener started", "interface", r.iface, "localAddr", addr.String())

	r.conn = conn
	r.ctx, r.cancel = context.WithCancel(ctx)
	r.started = true

	go r.receiveLoop()

	return nil
}

// Stop stops listening for Router Advertisements.
func (r *RAReceiver) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return nil
	}

	r.started = false
	if r.cancel != nil {
		r.cancel()
	}
	close(r.stopCh)

	if r.conn != nil {
		return r.conn.Close()
	}

	return nil
}

// Events returns the channel of prefix events.
func (r *RAReceiver) Events() <-chan Event {
	return r.events
}

// CurrentPrefix returns the currently observed prefix, if any.
func (r *RAReceiver) CurrentPrefix() *Prefix {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.currentPrefix
}

// Source returns SourceRouterAdvertisement.
func (r *RAReceiver) Source() Source {
	return SourceRouterAdvertisement
}

// receiveLoop continuously reads Router Advertisements from the interface.
func (r *RAReceiver) receiveLoop() {
	log := logf.Log.WithName("ra-receiver")
	log.Info("Receive loop started", "interface", r.iface)

	iterationCount := 0
	for {
		select {
		case <-r.stopCh:
			log.Info("Receive loop stopping (stopCh)")
			return
		case <-r.ctx.Done():
			log.Info("Receive loop stopping (ctx done)")
			return
		default:
		}

		// Set read deadline to allow periodic checking of stop signal
		if err := r.conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			log.Error(err, "Failed to set read deadline")
			r.sendError(fmt.Errorf("failed to set read deadline: %w", err))
			continue
		}

		msg, _, from, err := r.conn.ReadFrom()
		if err != nil {
			// Timeout is expected, just continue
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				iterationCount++
				// Log every 30 seconds to show we're still alive
				if iterationCount%30 == 0 {
					log.V(1).Info("Waiting for Router Advertisements", "interface", r.iface, "iterations", iterationCount)
				}
				continue
			}
			log.Error(err, "Failed to read NDP message")
			r.sendError(fmt.Errorf("failed to read NDP message: %w", err))
			continue
		}

		log.V(1).Info("Received NDP message", "type", fmt.Sprintf("%T", msg), "from", from)

		ra, ok := msg.(*ndp.RouterAdvertisement)
		if !ok {
			// Not a Router Advertisement, ignore
			log.V(2).Info("Ignoring non-RA message", "type", fmt.Sprintf("%T", msg))
			continue
		}

		log.Info("Received Router Advertisement", "from", from, "optionCount", len(ra.Options))
		r.handleRouterAdvertisement(ra)
	}
}

// handleRouterAdvertisement processes a received Router Advertisement.
func (r *RAReceiver) handleRouterAdvertisement(ra *ndp.RouterAdvertisement) {
	log := logf.Log.WithName("ra-receiver")
	var bestPrefix *ndp.PrefixInformation

	// Look through all options for Prefix Information
	for _, opt := range ra.Options {
		pi, ok := opt.(*ndp.PrefixInformation)
		if !ok {
			continue
		}

		log.Info("Found prefix option",
			"prefix", pi.Prefix,
			"prefixLength", pi.PrefixLength,
			"onLink", pi.OnLink,
			"autonomous", pi.AutonomousAddressConfiguration,
			"validLifetime", pi.ValidLifetime,
			"preferredLifetime", pi.PreferredLifetime)

		// Skip if not on-link
		// Note: We don't require autonomous=true because that only controls SLAAC.
		// Many ISPs (e.g., Deutsche Telekom) advertise prefixes with autonomous=false
		// when using stateful DHCPv6 for address assignment. The prefix is still valid.
		if !pi.OnLink {
			log.V(1).Info("Skipping prefix: not on-link", "prefix", pi.Prefix)
			continue
		}

		// Skip zero valid lifetime (deprecated prefix)
		if pi.ValidLifetime == 0 {
			log.V(1).Info("Skipping prefix: zero valid lifetime", "prefix", pi.Prefix)
			continue
		}

		// The Prefix field is already netip.Addr in mdlayher/ndp v1.1.0
		addr := pi.Prefix

		// Prefer Global Unicast Addresses over ULA and Link-Local
		if isGlobalUnicast(addr) {
			log.V(1).Info("Prefix is Global Unicast", "prefix", pi.Prefix)
			if bestPrefix == nil || !isGlobalUnicast(bestPrefix.Prefix) {
				bestPrefix = pi
			}
		} else if isULA(addr) {
			log.V(1).Info("Prefix is ULA", "prefix", pi.Prefix)
			if bestPrefix == nil {
				bestPrefix = pi
			}
		} else {
			log.V(1).Info("Prefix is neither GUA nor ULA, skipping", "prefix", pi.Prefix)
		}
	}

	if bestPrefix == nil {
		log.Info("No suitable prefix found in Router Advertisement")
		return
	}

	prefix := netip.PrefixFrom(bestPrefix.Prefix, int(bestPrefix.PrefixLength))
	log.Info("Selected prefix", "prefix", prefix, "validLifetime", bestPrefix.ValidLifetime)

	r.updatePrefix(prefix, bestPrefix.ValidLifetime, bestPrefix.PreferredLifetime)
}

// updatePrefix updates the current prefix and sends an event if changed.
func (r *RAReceiver) updatePrefix(prefix netip.Prefix, validLifetime, preferredLifetime time.Duration) {
	log := logf.Log.WithName("ra-receiver")
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	newPrefix := &Prefix{
		Network:           prefix,
		ValidLifetime:     validLifetime,
		PreferredLifetime: preferredLifetime,
		Source:            SourceRouterAdvertisement,
		ReceivedAt:        now,
	}

	var eventType EventType
	if r.currentPrefix == nil {
		eventType = EventTypeAcquired
	} else if r.currentPrefix.Network != prefix {
		eventType = EventTypeChanged
	} else {
		eventType = EventTypeRenewed
	}

	log.Info("Updating prefix",
		"prefix", prefix,
		"eventType", eventType,
		"previousPrefix", r.currentPrefix)

	r.currentPrefix = newPrefix

	// Send event (non-blocking to avoid deadlock)
	select {
	case r.events <- Event{Type: eventType, Prefix: newPrefix}:
		log.Info("Event sent successfully", "eventType", eventType)
	default:
		log.Info("Event channel full, event dropped", "eventType", eventType)
	}
}

// sendError sends a failed event.
func (r *RAReceiver) sendError(err error) {
	select {
	case r.events <- Event{Type: EventTypeFailed, Error: err}:
	default:
		// Channel full, event dropped
	}
}

// isGlobalUnicast returns true if the address is a Global Unicast Address (2000::/3).
func isGlobalUnicast(addr netip.Addr) bool {
	if !addr.Is6() {
		return false
	}
	bytes := addr.As16()
	// GUA: first 3 bits are 001 (0x20-0x3f in first byte)
	return (bytes[0] & 0xE0) == 0x20
}

// isULA returns true if the address is a Unique Local Address (fc00::/7).
func isULA(addr netip.Addr) bool {
	if !addr.Is6() {
		return false
	}
	bytes := addr.As16()
	// ULA: first 7 bits are 1111110 (0xfc or 0xfd in first byte)
	return (bytes[0] & 0xFE) == 0xFC
}

// isLinkLocal returns true if the address is a Link-Local Address (fe80::/10).
func isLinkLocal(addr netip.Addr) bool {
	return addr.IsLinkLocalUnicast()
}
