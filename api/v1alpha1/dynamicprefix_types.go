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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DynamicPrefixSpec defines the desired state of DynamicPrefix
type DynamicPrefixSpec struct {
	// Acquisition defines how to receive the IPv6 prefix
	// +required
	Acquisition AcquisitionSpec `json:"acquisition"`

	// AddressRanges defines address ranges within the received prefix.
	// Use this for Mode 1 (recommended): reserve a range within your /64 that
	// your router's DHCPv6/SLAAC won't hand out. No BGP required.
	// +optional
	AddressRanges []AddressRangeSpec `json:"addressRanges,omitempty"`

	// Subnets defines how to subdivide the received prefix into smaller subnets.
	// Use this for Mode 2 (advanced): carve out dedicated /64s from a larger
	// prefix. Requires BGP to announce the subnets to your router.
	// +optional
	Subnets []SubnetSpec `json:"subnets,omitempty"`

	// Transition defines graceful transition settings when prefix changes
	// +optional
	Transition *TransitionSpec `json:"transition,omitempty"`
}

// AcquisitionSpec defines how to acquire/receive the IPv6 prefix
type AcquisitionSpec struct {
	// DHCPv6PD configures DHCPv6 Prefix Delegation to receive prefix from upstream router
	// +optional
	DHCPv6PD *DHCPv6PDSpec `json:"dhcpv6pd,omitempty"`

	// RouterAdvertisement configures Router Advertisement monitoring as fallback
	// +optional
	RouterAdvertisement *RouterAdvertisementSpec `json:"routerAdvertisement,omitempty"`
}

// DHCPv6PDSpec configures the DHCPv6 Prefix Delegation client
type DHCPv6PDSpec struct {
	// Interface is the network interface to receive the delegated prefix on
	// +required
	// +kubebuilder:validation:MinLength=1
	Interface string `json:"interface"`

	// RequestedPrefixLength hints the desired prefix length to request
	// +optional
	// +kubebuilder:validation:Minimum=48
	// +kubebuilder:validation:Maximum=64
	RequestedPrefixLength *int `json:"requestedPrefixLength,omitempty"`
}

// RouterAdvertisementSpec configures Router Advertisement monitoring
type RouterAdvertisementSpec struct {
	// Interface is the network interface to monitor for Router Advertisements
	// +optional
	Interface string `json:"interface,omitempty"`

	// Enabled controls whether RA monitoring is active
	// +optional
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`
}

// AddressRangeSpec defines an address range within the received prefix.
// This is used for Mode 1 where you reserve a portion of your /64 that
// the router won't hand out via DHCPv6/SLAAC.
type AddressRangeSpec struct {
	// Name identifies this address range (used in annotations to reference it)
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// Start is the start of the range, specified as a suffix to the prefix.
	// For example, "::f000:0:0:0" means start at prefix + 0xf000:0:0:0.
	// +required
	Start string `json:"start"`

	// End is the end of the range (inclusive), specified as a suffix.
	// For example, "::ffff:ffff:ffff:ffff" means end at prefix + 0xffff:ffff:ffff:ffff.
	// +required
	End string `json:"end"`
}

// SubnetSpec defines a subnet to be carved out of the received prefix.
// This is used for Mode 2 (advanced) where you claim a dedicated /64 from
// a larger prefix and announce it via BGP.
type SubnetSpec struct {
	// Name identifies this subnet (used in annotations to reference it)
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// Offset is the address offset within the received prefix (in host units)
	// +optional
	// +kubebuilder:default=0
	Offset int64 `json:"offset,omitempty"`

	// PrefixLength is the prefix length of the subnet (e.g., 120 for a /120)
	// +required
	// +kubebuilder:validation:Minimum=48
	// +kubebuilder:validation:Maximum=128
	PrefixLength int `json:"prefixLength"`
}

// TransitionMode defines the transition behavior mode
type TransitionMode string

const (
	// TransitionModeSimple keeps multiple blocks in pool; Services keep old IPs until block removed
	TransitionModeSimple TransitionMode = "simple"

	// TransitionModeHA keeps both old and new IPs on Service, with DNS pointing to new IP only
	TransitionModeHA TransitionMode = "ha"
)

// TransitionSpec defines settings for graceful prefix transitions
type TransitionSpec struct {
	// Mode specifies the transition behavior.
	// "simple" (default): Keep multiple blocks in pool, Services keep old IPs until block removed.
	// "ha": Keep both old and new IPs on Service, DNS points to new IP only via external-dns annotation.
	// +optional
	// +kubebuilder:validation:Enum=simple;ha
	// +kubebuilder:default=simple
	Mode TransitionMode `json:"mode,omitempty"`

	// MaxPrefixHistory is the maximum number of previous prefixes to retain in pool blocks.
	// When a new prefix is received, historical prefixes beyond this limit are dropped.
	// +optional
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	MaxPrefixHistory int `json:"maxPrefixHistory,omitempty"`
}

// DynamicPrefixStatus defines the observed state of DynamicPrefix
type DynamicPrefixStatus struct {
	// CurrentPrefix is the currently active IPv6 prefix in CIDR notation
	// +optional
	CurrentPrefix string `json:"currentPrefix,omitempty"`

	// PrefixSource indicates how the prefix was obtained
	// +optional
	PrefixSource PrefixSource `json:"prefixSource,omitempty"`

	// LeaseExpiresAt indicates when the DHCPv6 lease expires
	// +optional
	LeaseExpiresAt *metav1.Time `json:"leaseExpiresAt,omitempty"`

	// AddressRanges contains the calculated address ranges
	// +optional
	AddressRanges []AddressRangeStatus `json:"addressRanges,omitempty"`

	// Subnets contains the calculated subnet CIDRs
	// +optional
	Subnets []SubnetStatus `json:"subnets,omitempty"`

	// History contains previous prefixes
	// +optional
	History []PrefixHistoryEntry `json:"history,omitempty"`

	// Conditions represent the current state of the DynamicPrefix
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// PrefixSource indicates how a prefix was obtained
// +kubebuilder:validation:Enum=dhcpv6-pd;router-advertisement;static;unknown
type PrefixSource string

const (
	PrefixSourceDHCPv6PD            PrefixSource = "dhcpv6-pd"
	PrefixSourceRouterAdvertisement PrefixSource = "router-advertisement"
	PrefixSourceStatic              PrefixSource = "static"
	PrefixSourceUnknown             PrefixSource = "unknown"
)

// AddressRangeStatus represents the current state of an address range
type AddressRangeStatus struct {
	// Name is the address range identifier
	Name string `json:"name"`

	// Start is the first address in the range (full address)
	Start string `json:"start"`

	// End is the last address in the range (full address)
	End string `json:"end"`

	// CIDR is an approximate CIDR representation for compatibility.
	// For Cilium pools, use Start/End for precise range definition.
	// This may be a larger range if the start/end don't align to CIDR boundaries.
	CIDR string `json:"cidr,omitempty"`
}

// SubnetStatus represents the current state of a subnet
type SubnetStatus struct {
	// Name is the subnet identifier
	Name string `json:"name"`

	// CIDR is the calculated subnet in CIDR notation
	CIDR string `json:"cidr"`
}

// PrefixHistoryEntry represents a historical prefix
type PrefixHistoryEntry struct {
	// Prefix is the historical prefix in CIDR notation
	Prefix string `json:"prefix"`

	// AcquiredAt is when this prefix was first acquired
	AcquiredAt metav1.Time `json:"acquiredAt"`

	// DeprecatedAt is when this prefix was replaced by a new one
	// +optional
	DeprecatedAt *metav1.Time `json:"deprecatedAt,omitempty"`

	// State indicates the current state of this historical prefix
	// +optional
	State PrefixState `json:"state,omitempty"`
}

// PrefixState indicates the state of a prefix
// +kubebuilder:validation:Enum=active;draining;expired
type PrefixState string

const (
	PrefixStateActive   PrefixState = "active"
	PrefixStateDraining PrefixState = "draining"
	PrefixStateExpired  PrefixState = "expired"
)

// Condition types for DynamicPrefix
const (
	// ConditionTypePrefixAcquired indicates whether a prefix has been acquired
	ConditionTypePrefixAcquired = "PrefixAcquired"

	// ConditionTypePoolsSynced indicates whether all referencing pools are synced
	ConditionTypePoolsSynced = "PoolsSynced"

	// ConditionTypeDegraded indicates the resource is in a degraded state
	ConditionTypeDegraded = "Degraded"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=dp;dprefix
// +kubebuilder:printcolumn:name="Prefix",type=string,JSONPath=`.status.currentPrefix`
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.status.prefixSource`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DynamicPrefix is the Schema for the dynamicprefixes API.
// It represents a dynamically acquired IPv6 prefix that can be subdivided
// into subnets and used to populate Cilium IP pools and other resources.
type DynamicPrefix struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of DynamicPrefix
	// +required
	Spec DynamicPrefixSpec `json:"spec"`

	// Status defines the observed state of DynamicPrefix
	// +optional
	Status DynamicPrefixStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DynamicPrefixList contains a list of DynamicPrefix
type DynamicPrefixList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DynamicPrefix `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DynamicPrefix{}, &DynamicPrefixList{})
}
