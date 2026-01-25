# Design: BGP Subnet Advertisement for Mode 2

## Status

**Approved** - Ready for implementation

## Summary

This document describes how the Dynamic Prefix Operator advertises carved-out subnets via BGP in subnet mode (Mode 2). The operator manages `CiliumBGPAdvertisement` resources to enable Cilium's BGP Control Plane to announce LoadBalancer Service IPs from dynamically calculated subnets.

## Problem Statement

In subnet mode, the operator carves dedicated subnets (e.g., `/64`) from a larger ISP-delegated prefix (e.g., `/56`). These subnets are used to populate `CiliumLoadBalancerIPPool` resources. However, the upstream router has no route to these subnets—traffic cannot reach the Kubernetes cluster.

```
ISP → Router (receives /56 via DHCPv6-PD) → K8s node
                                              │
                          Operator calculates: 2001:db8:1234:ff::/64
                          Updates CiliumLoadBalancerIPPool
                                              │
                          ❌ Router has no route to this /64
```

## Solution Overview

Leverage Cilium's BGP Control Plane to advertise LoadBalancer Service IPs back to the router. The operator:

1. Creates `CiliumBGPAdvertisement` resources for subnets with `bgp.advertise: true`
2. Uses Service-based advertisement (individual IPs, not whole subnets)
3. Tags advertisements with configurable BGP communities for router-side filtering

```
Router ← BGP: "2001:db8:1234:ff::1/128 via node-1" ← Cilium BGP
                                                        ↑
                              CiliumBGPAdvertisement (operator-managed)
                                                        ↑
                              CiliumLoadBalancerIPPool (operator-managed)
                                                        ↑
                              LoadBalancer Service (user-created)
```

## Design Decisions

### Scope of Operator Responsibility

| Resource | Managed By | Rationale |
|----------|-----------|-----------|
| `CiliumBGPClusterConfig` | User | Peering is security-sensitive; user controls BGP sessions |
| `CiliumBGPPeerConfig` | User | Authentication and peer settings are site-specific |
| `CiliumBGPAdvertisement` | Operator | What to advertise matches operator's domain |
| `CiliumLoadBalancerIPPool` | Operator | Already implemented |

### Advertisement Strategy: Per-IP vs Aggregation

**Decision: Per-IP (/128) advertisement by default**

- Only IPs assigned to actual Services are announced
- Router receives minimal, precise routing information
- Aggregation is not supported (simplicity, trust minimization)

### Service Selection

**Decision: Use pool's serviceSelector**

The `CiliumLoadBalancerIPPool` has a `serviceSelector` field that determines which Services can get IPs from the pool. The operator uses this same selector in the `CiliumBGPAdvertisement` to ensure only Services using the pool are advertised.

If no `serviceSelector` is set on the pool, the advertisement matches all Services (Cilium's default behavior).

### Trust Minimization

The operator enables router-side filtering through:

1. **Per-IP advertisement**: Only /128 routes, never broader prefixes
2. **BGP communities**: Configurable community tag for filtering
3. **Documentation**: Router configuration examples

## API Changes

### SubnetSpec Extension

```go
// SubnetSpec defines a subnet to be carved out of the received prefix.
type SubnetSpec struct {
    // Name identifies this subnet
    Name string `json:"name"`

    // Offset is the address offset within the received prefix
    Offset int64 `json:"offset,omitempty"`

    // PrefixLength is the prefix length of the subnet
    PrefixLength int `json:"prefixLength"`

    // BGP configures BGP advertisement for this subnet
    // +optional
    BGP *SubnetBGPSpec `json:"bgp,omitempty"`
}

// SubnetBGPSpec configures BGP advertisement for a subnet.
type SubnetBGPSpec struct {
    // Advertise enables BGP advertisement of LoadBalancer IPs from this subnet.
    // When true, the operator creates a CiliumBGPAdvertisement resource.
    // Requires Cilium BGP Control Plane to be enabled and peering configured.
    // +optional
    Advertise bool `json:"advertise,omitempty"`

    // Community is the BGP community to attach to advertisements.
    // Format: "ASN:VALUE" (e.g., "65001:100").
    // The router should filter to only accept routes with this community.
    // +optional
    // +kubebuilder:validation:Pattern=`^\d+:\d+$`
    Community string `json:"community,omitempty"`
}
```

### SubnetStatus Extension

```go
// SubnetStatus represents the current state of a subnet
type SubnetStatus struct {
    // Name is the subnet identifier
    Name string `json:"name"`

    // CIDR is the calculated subnet in CIDR notation
    CIDR string `json:"cidr"`

    // BGPAdvertisement is the name of the managed CiliumBGPAdvertisement resource
    // +optional
    BGPAdvertisement string `json:"bgpAdvertisement,omitempty"`
}
```

### New Condition

```go
const (
    // ConditionTypeBGPAdvertisementReady indicates BGP advertisements are configured
    ConditionTypeBGPAdvertisementReady = "BGPAdvertisementReady"
)
```

## Example Configuration

### DynamicPrefix with BGP

```yaml
apiVersion: dynamic-prefix.io/v1alpha1
kind: DynamicPrefix
metadata:
  name: home-ipv6
spec:
  acquisition:
    dhcpv6pd:
      interface: eth0
      requestedPrefixLength: 56

  subnets:
    - name: loadbalancers
      offset: 255           # Use the 256th /64 (index 255)
      prefixLength: 64
      bgp:
        advertise: true
        community: "65001:42"

  transition:
    mode: simple
    maxPrefixHistory: 2
```

### Generated CiliumBGPAdvertisement

```yaml
apiVersion: cilium.io/v2
kind: CiliumBGPAdvertisement
metadata:
  name: dp-home-ipv6-loadbalancers
  labels:
    app.kubernetes.io/managed-by: dynamic-prefix-operator
    dynamic-prefix.io/name: home-ipv6
    dynamic-prefix.io/subnet: loadbalancers
  ownerReferences:
    - apiVersion: dynamic-prefix.io/v1alpha1
      kind: DynamicPrefix
      name: home-ipv6
      controller: true
spec:
  advertisements:
    - advertisementType: Service
      service:
        addresses:
          - LoadBalancerIP
      selector:
        matchLabels:
          # Copied from CiliumLoadBalancerIPPool.spec.serviceSelector
          # If pool has no selector, this matches all Services
      attributes:
        communities:
          standard:
            - "65001:42"
```

### User-Managed BGP Peering (Prerequisite)

```yaml
apiVersion: cilium.io/v2
kind: CiliumBGPClusterConfig
metadata:
  name: bgp-config
spec:
  nodeSelector:
    matchLabels:
      bgp: enabled
  bgpInstances:
    - name: default
      localASN: 65001
      peers:
        - name: router
          peerAddress: fe80::1
          peerASN: 65000
          peerConfigRef:
            name: router-peer
---
apiVersion: cilium.io/v2
kind: CiliumBGPPeerConfig
metadata:
  name: router-peer
spec:
  transport:
    peerPort: 179
  timers:
    holdTimeSeconds: 90
    keepAliveTimeSeconds: 30
  families:
    - afi: ipv6
      safi: unicast
      advertisements:
        matchLabels:
          dynamic-prefix.io/name: home-ipv6
```

## Controller Design

### BGPSyncReconciler

A new controller that watches `DynamicPrefix` resources and manages `CiliumBGPAdvertisement` resources.

```
Watches:
  - DynamicPrefix (primary)
  - CiliumBGPAdvertisement (owned resources)
  - CiliumLoadBalancerIPPool (to read serviceSelector)

Reconciliation:
  1. List subnets with bgp.advertise: true
  2. For each subnet:
     a. Find the corresponding CiliumLoadBalancerIPPool
     b. Read its serviceSelector
     c. Create/update CiliumBGPAdvertisement with:
        - Owner reference to DynamicPrefix
        - Labels for identification
        - Service selector from pool
        - Community from subnet.bgp.community
  3. Delete orphaned CiliumBGPAdvertisement resources
  4. Update DynamicPrefix status with advertisement names
  5. Set BGPAdvertisementReady condition
```

### Integration with Existing Controllers

The BGPSyncReconciler runs independently but coordinates through the DynamicPrefix status:

```
DynamicPrefixReconciler     PoolSyncReconciler     BGPSyncReconciler
        │                          │                       │
        │ Updates status           │ Syncs pools           │ Syncs advertisements
        │ with prefix              │ from status           │ from status
        ▼                          ▼                       ▼
   DynamicPrefix.Status ──────────────────────────────────────
        │
        ├── currentPrefix
        ├── subnets[].cidr
        └── subnets[].bgpAdvertisement
```

## Router Configuration

### Recommended Filter Configuration

```
# 1. Only accept /128 host routes (CRITICAL - static, prefix-independent)
ipv6 prefix-list K8S-HOSTS seq 10 permit ::/0 ge 128 le 128

# 2. Require community tag (RECOMMENDED)
ip community-list standard K8S-LB permit 65001:42

# 3. Combine in route-map
route-map FROM-K8S permit 10
  match ipv6 address prefix-list K8S-HOSTS
  match community K8S-LB
route-map FROM-K8S deny 20

# 4. Apply to peer
neighbor 2001:db8::node1 route-map FROM-K8S in

# 5. Limit total routes (SAFETY)
neighbor 2001:db8::node1 maximum-prefix 100 warning-only
```

### Platform-Specific Examples

#### MikroTik RouterOS 7

```
/routing bgp template
add name=k8s-peer as=65000 router-id=10.0.0.1

/routing filter rule
add chain=k8s-in rule="if (dst-len < 128) { reject }"
add chain=k8s-in rule="if (bgp-communities includes 65001:42) { accept }"
add chain=k8s-in rule="reject"

/routing bgp connection
add name=k8s-node1 remote.address=2001:db8::node1 remote.as=65001 \
    templates=k8s-peer input.filter=k8s-in
```

#### BIRD 2 (OpenWRT)

```
filter accept_k8s_lb {
  # Only accept /128 host routes
  if net.len != 128 then reject;

  # Require our community tag
  if (65001, 42) ~ bgp_community then accept;

  reject;
}

protocol bgp k8s_node1 {
  local as 65000;
  neighbor 2001:db8::node1 as 65001;

  ipv6 {
    import filter accept_k8s_lb;
    import limit 100;
  };
}
```

#### VyOS / EdgeOS

```
set policy community-list 100 rule 10 action permit
set policy community-list 100 rule 10 community '65001:42'

set policy prefix-list6 K8S-HOSTS rule 10 action permit
set policy prefix-list6 K8S-HOSTS rule 10 prefix '::/0'
set policy prefix-list6 K8S-HOSTS rule 10 ge 128
set policy prefix-list6 K8S-HOSTS rule 10 le 128

set policy route-map FROM-K8S rule 10 action permit
set policy route-map FROM-K8S rule 10 match community community-list 100
set policy route-map FROM-K8S rule 10 match ipv6 address prefix-list K8S-HOSTS

set protocols bgp neighbor 2001:db8::node1 address-family ipv6-unicast route-map import FROM-K8S
set protocols bgp neighbor 2001:db8::node1 address-family ipv6-unicast maximum-prefix 100
```

## Security Considerations

### Trust Model

| Layer | Controlled By | Trust Requirement |
|-------|--------------|-------------------|
| BGP session establishment | User (CiliumBGPClusterConfig) | User trusts nodes to peer |
| Route content | Operator (CiliumBGPAdvertisement) | Router trusts /128 + community |
| IP allocation | Cilium LB-IPAM | Pool constraints |
| Service creation | User | K8s RBAC |

### Attack Vectors Mitigated

| Attack | Mitigation |
|--------|-----------|
| Advertise default route | Blocked by /128 length filter |
| Advertise ISP prefix | Blocked by /128 length filter |
| Advertise arbitrary subnets | Blocked by /128 length filter |
| Route flooding | Limited by max-prefix |
| Spoofed advertisements | Blocked by community filter + BGP auth |

### Residual Risks

- **Malicious Service creation**: User with Service creation permissions can trigger route advertisement. Mitigated by K8s RBAC.
- **Community spoofing from nodes**: If a node is compromised, it could advertise routes with the community. Mitigated by BGP session authentication.

## Testing Strategy

### Unit Tests

- `SubnetBGPSpec` validation
- Advertisement name generation
- Selector merging logic

### Integration Tests

- Advertisement creation when `bgp.advertise: true`
- Advertisement deletion when subnet removed
- Advertisement update when community changes
- Owner reference cleanup

### E2E Tests

- Full flow with mock BGP peer
- Prefix change triggers advertisement update

## Implementation Plan

1. **CRD Extension**: Add `SubnetBGPSpec` to `SubnetSpec`
2. **Controller**: Implement `BGPSyncReconciler`
3. **Status**: Add `BGPAdvertisement` field to `SubnetStatus`
4. **RBAC**: Add permissions for `CiliumBGPAdvertisement`
5. **Documentation**: Router configuration guide
6. **Tests**: Unit and integration tests

## Future Considerations

Not in scope for initial implementation:

- Multiple BGP communities per subnet
- Large BGP communities (RFC 8092)
- Well-known communities (no-export, no-advertise)
- IPv4 support
- Multiple advertisement resources per subnet (different peers)
