# Architecture Overview

## System Context

```
                              Upstream Router / ISP
                                       │
                                       │ DHCPv6-PD (delegates prefix)
                                       │ or Router Advertisements
                                       ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                              Kubernetes Node                                  │
│                                                                              │
│   ┌────────────────────────────────────────────────────────────────────────┐ │
│   │                       Dynamic Prefix Operator                           │ │
│   │                                                                         │ │
│   │  ┌─────────────────────────────────────────────────────────────────┐   │ │
│   │  │                     Prefix Receiver                              │   │ │
│   │  │                                                                  │   │ │
│   │  │  ┌─────────────────┐         ┌─────────────────┐                │   │ │
│   │  │  │ DHCPv6-PD Client│         │ RA Monitor      │                │   │ │
│   │  │  │                 │         │                 │                │   │ │
│   │  │  │ • SOLICIT/REPLY │         │ • Parse RAs     │                │   │ │
│   │  │  │ • Lease renewal │         │ • Extract PIOs  │                │   │ │
│   │  │  └────────┬────────┘         └────────┬────────┘                │   │ │
│   │  │           └──────────┬───────────────┘                          │   │ │
│   │  │                      ▼                                          │   │ │
│   │  │           ┌──────────────────────┐                              │   │ │
│   │  │           │    Prefix Store      │                              │   │ │
│   │  │           │  • Current prefix    │                              │   │ │
│   │  │           │  • Lease state       │                              │   │ │
│   │  │           └──────────────────────┘                              │   │ │
│   │  └─────────────────────────────────────────────────────────────────┘   │ │
│   │                            │                                            │ │
│   │                            ▼                                            │ │
│   │  ┌─────────────────────────────────────────────────────────────────┐   │ │
│   │  │                      Controllers                                 │   │ │
│   │  │                                                                  │   │ │
│   │  │  DynamicPrefix Controller        Pool Controller                 │   │ │
│   │  │  • Manages prefix lifecycle      • Watches annotated pools       │   │ │
│   │  │  • Calculates subnets            • Updates CIDRs on change       │   │ │
│   │  │  • Updates CR status             • Handles multiple pool types   │   │ │
│   │  │         │                                │                       │   │ │
│   │  │         ▼                                ▼                       │   │ │
│   │  │  ┌────────────────┐        ┌─────────────────────────────────┐  │   │ │
│   │  │  │ DynamicPrefix  │        │ Pools with annotations:         │  │   │ │
│   │  │  │ CR (status)    │◄───────│ dynamic-prefix.io/name: xxx     │  │   │ │
│   │  │  └────────────────┘        └─────────────────────────────────┘  │   │ │
│   │  └─────────────────────────────────────────────────────────────────┘   │ │
│   └────────────────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                         Kubernetes Resources                                  │
│                                                                              │
│   ┌────────────────────────┐    ┌────────────────────────┐                   │
│   │ CiliumLoadBalancer     │    │ CiliumCIDRGroup        │                   │
│   │ IPPool                 │    │                        │                   │
│   │                        │    │ • Network policies     │                   │
│   │ annotations:           │    │ annotations:           │                   │
│   │   dynamic-prefix.io/   │    │   dynamic-prefix.io/   │                   │
│   │     name: home-ipv6    │    │     name: home-ipv6    │                   │
│   │                        │    │                        │                   │
│   │ spec.blocks: [CIDR]    │    │ spec.externalCIDRs:    │                   │
│   │ (managed by operator)  │    │   [CIDR]               │                   │
│   └───────────┬────────────┘    └────────────────────────┘                   │
│               │                                                              │
│               ▼                                                              │
│   ┌────────────────────────┐                                                 │
│   │ LoadBalancer Service   │                                                 │
│   │                        │                                                 │
│   │ • Gets IPv6 from pool  │                                                 │
│   └───────────┬────────────┘                                                 │
│               │                                                              │
│               ▼                                                              │
│   ┌────────────────────────┐                                                 │
│   │ external-dns           │─────────▶ DNS Provider                          │
│   │                        │           (AAAA record update)                  │
│   │ • Watches LB services  │                                                 │
│   │ • Updates DNS          │                                                 │
│   └────────────────────────┘                                                 │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

## Component Details

### 1. Prefix Receiver Layer

#### DHCPv6-PD Client

The operator acts as a DHCPv6 Prefix Delegation client, receiving prefixes from the upstream router.

**Protocol Flow:**
```
Operator                        Upstream Router
   │                                   │
   │──── SOLICIT (IA_PD) ─────────────▶│
   │                                   │
   │◀─── ADVERTISE (IA_PD) ────────────│
   │                                   │
   │──── REQUEST (IA_PD) ──────────────▶│
   │                                   │
   │◀─── REPLY (IA_PD, prefix) ────────│
   │                                   │
   │    ... lease active ...           │
   │                                   │
   │──── RENEW (before T1) ────────────▶│
   │                                   │
   │◀─── REPLY (renewed lease) ────────│
   │                                   │
```

**Key Features:**
- Receives delegated prefix from upstream router
- Configurable prefix length hint
- T1/T2 timer management for renewal
- Rebind fallback if renewal fails

#### Router Advertisement Monitor

Fallback/supplementary prefix detection using [mdlayher/ndp](https://github.com/mdlayher/ndp).

**What it monitors:**
- ICMPv6 Router Advertisements (Type 134)
- Prefix Information Options (PIO)
- Valid and preferred lifetimes

### 2. Controller Layer

#### DynamicPrefix Controller

**Responsibilities:**
- Manages DynamicPrefix resource lifecycle
- Starts/stops prefix receivers
- Calculates subnets from received prefix
- Updates CR status

**Reconciliation Triggers:**
- DynamicPrefix create/update/delete
- Prefix change from receiver
- Lease expiry approaching

#### Pool Controller

**Responsibilities:**
- Watches for pools with `dynamic-prefix.io/name` annotation
- Looks up referenced DynamicPrefix
- Updates pool CIDR from DynamicPrefix status
- Re-reconciles when DynamicPrefix changes

**Annotation-Based Binding:**

```yaml
# Pool references DynamicPrefix via annotation
apiVersion: cilium.io/v2alpha1
kind: CiliumLoadBalancerIPPool
metadata:
  annotations:
    dynamic-prefix.io/name: home-ipv6       # Which DynamicPrefix
    dynamic-prefix.io/subnet: loadbalancers # Which subnet
spec:
  blocks: []  # Operator manages this
```

This follows the [1Password Operator](https://github.com/1Password/onepassword-operator) pattern:
- Simpler than explicit binding CRDs
- Pools are self-documenting
- No orphaned resources

### 3. Pool Handlers

Each pool type has a handler that knows how to update it:

```go
type PoolHandler interface {
    // Extract annotation values
    GetAnnotations(obj client.Object) (prefixName, subnetName string, ok bool)

    // Update the pool's CIDR field
    UpdateCIDR(ctx context.Context, obj client.Object, cidr netip.Prefix) error
}
```

**Cilium LB-IPAM Handler:**
```go
func (h *LBIPAMHandler) UpdateCIDR(ctx context.Context, obj client.Object, cidr netip.Prefix) error {
    pool := obj.(*ciliumv2alpha1.CiliumLoadBalancerIPPool)
    pool.Spec.Blocks = []CiliumLoadBalancerIPPoolIPBlock{
        {Cidr: IPv6CIDR(cidr.String())},
    }
    return nil
}
```

**Cilium CIDRGroup Handler:**
```go
func (h *CIDRGroupHandler) UpdateCIDR(ctx context.Context, obj client.Object, cidr netip.Prefix) error {
    group := obj.(*ciliumv2.CiliumCIDRGroup)
    group.Spec.ExternalCIDRs = []ExternalCIDR{ExternalCIDR(cidr.String())}
    return nil
}
```

## Data Flow

### Prefix Reception Flow

```
1. Operator starts, reads DynamicPrefix CR
2. Creates DHCPv6-PD client for specified interface
3. Client sends SOLICIT with IA_PD
4. Receives REPLY with delegated prefix
5. Updates DynamicPrefix status.currentPrefix
6. Calculates subnets per spec
7. Updates status.subnets
8. Schedules renewal based on T1
```

### Pool Update Flow

```
1. Pool created with dynamic-prefix.io/name annotation
2. Pool controller detects annotation
3. Looks up referenced DynamicPrefix
4. Gets subnet CIDR from status
5. Updates pool's CIDR field
6. Pool is now in sync
```

### Prefix Change Flow

```
1. DHCPv6-PD client receives new prefix
2. DynamicPrefix controller updates status
3. Pool controller sees status change
4. Finds all pools referencing this DynamicPrefix
5. Updates each pool with new CIDR
6. external-dns sees new LB IPs, updates DNS
```

## Custom Resource Definition

### DynamicPrefix

**Purpose:** Represents a dynamically received IPv6 prefix

**Spec:**
- `acquisition`: How to receive the prefix (DHCPv6-PD, RA)
- `subnets`: How to subdivide the prefix
- `transition`: Graceful transition settings

**Status:**
- `currentPrefix`: Currently active prefix
- `prefixSource`: How prefix was received
- `leaseExpiresAt`: When DHCPv6 lease expires
- `subnets`: Calculated subnet CIDRs
- `conditions`: Standard Kubernetes conditions

## Failure Modes and Recovery

### DHCPv6-PD Server Unavailable

**Detection:** SOLICIT timeout

**Recovery:**
1. Retry with exponential backoff
2. Fall back to RA monitoring if configured
3. Keep using cached prefix if valid lifetime remains
4. Emit PrefixAcquisitionFailed event

### Prefix Change

**Detection:** New prefix differs from current

**Recovery:**
1. Update DynamicPrefix status
2. Pool controller updates all referencing pools
3. DNS updates via external-dns
4. Optional: Graceful transition with drain period

### Operator Restart

**Recovery:**
1. Read all DynamicPrefix CRs
2. Re-establish prefix receivers
3. Reconcile all annotated pools
4. Resume lease renewals

## Security Considerations

### Network Access

The operator requires raw socket access:

```yaml
securityContext:
  capabilities:
    add:
      - NET_RAW
```

**Host network required** for interface binding.

### RBAC

Minimal permissions:
- Read/write DynamicPrefix CRs
- Update Cilium pools (only annotated ones)
- Create events
- Leader election

## Observability

### Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `dynamic_prefix_received_total` | Counter | Prefixes received |
| `dynamic_prefix_changes_total` | Counter | Prefix changes |
| `dynamic_prefix_lease_expiry_seconds` | Gauge | Seconds until expiry |
| `dynamic_prefix_pools_synced` | Gauge | Pools currently synced |

### Events

| Event | When |
|-------|------|
| `PrefixReceived` | New prefix obtained |
| `PrefixChanged` | Prefix changed |
| `PoolUpdated` | Pool CIDR updated |

## Extensibility

### Adding New Pool Types

1. Implement `PoolHandler` interface
2. Register in pool controller
3. Document annotation usage

Example for MetalLB:

```go
type MetalLBHandler struct{}

func (h *MetalLBHandler) UpdateCIDR(ctx context.Context, obj client.Object, cidr netip.Prefix) error {
    pool := obj.(*metallbv1beta1.IPAddressPool)
    pool.Spec.Addresses = []string{cidr.String()}
    return nil
}
```

### Adding New Prefix Sources

1. Implement `Receiver` interface
2. Add configuration to DynamicPrefix spec
3. Register in receiver manager
