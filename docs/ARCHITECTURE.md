# Architecture Overview

## System Context

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                              ISP Network                                      │
│                                                                              │
│   DHCPv6 Server                    Router                                    │
│   (Prefix Delegation)              (Router Advertisements)                   │
│         │                                │                                   │
└─────────┼────────────────────────────────┼───────────────────────────────────┘
          │ DHCPv6-PD                      │ ICMPv6 RA
          │ (SOLICIT/ADVERTISE/           │ (Prefix Information)
          │  REQUEST/REPLY)                │
          ▼                                ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                         Kubernetes Node                                       │
│                                                                              │
│   ┌────────────────────────────────────────────────────────────────────────┐ │
│   │                    Dynamic Prefix Operator                              │ │
│   │                                                                         │ │
│   │  ┌─────────────────┐         ┌─────────────────┐                       │ │
│   │  │ DHCPv6-PD       │         │ RA Monitor      │                       │ │
│   │  │ Client          │         │                 │                       │ │
│   │  │                 │         │                 │                       │ │
│   │  │ • Acquire prefix│         │ • Parse RAs     │                       │ │
│   │  │ • Manage lease  │         │ • Extract PIOs  │                       │ │
│   │  │ • Handle renew  │         │ • Track validity│                       │ │
│   │  └────────┬────────┘         └────────┬────────┘                       │ │
│   │           │                           │                                 │ │
│   │           └───────────┬───────────────┘                                 │ │
│   │                       ▼                                                 │ │
│   │           ┌─────────────────────────┐                                   │ │
│   │           │    Prefix Store         │                                   │ │
│   │           │                         │                                   │ │
│   │           │ • Current prefix        │                                   │ │
│   │           │ • Prefix history        │                                   │ │
│   │           │ • Lease state           │                                   │ │
│   │           └───────────┬─────────────┘                                   │ │
│   │                       │                                                 │ │
│   │                       ▼                                                 │ │
│   │           ┌─────────────────────────────────────────────────────────┐   │ │
│   │           │              Controllers                                │   │ │
│   │           │                                                         │   │ │
│   │           │  DynamicPrefix          DynamicPrefixBinding            │   │ │
│   │           │  Controller             Controller                      │   │ │
│   │           │      │                       │                          │   │ │
│   │           │      │    Reconcile          │    Reconcile             │   │ │
│   │           │      ▼                       ▼                          │   │ │
│   │           │  ┌────────────────┐   ┌─────────────────────────────┐   │   │ │
│   │           │  │ DynamicPrefix  │   │ Backend Manager             │   │   │ │
│   │           │  │ CR (status)    │   │                             │   │   │ │
│   │           │  └────────────────┘   │ • Cilium LB-IPAM Backend   │   │   │ │
│   │           │                       │ • Cilium CIDRGroup Backend │   │   │ │
│   │           │                       │ • (Future: Calico, MetalLB)│   │   │ │
│   │           │                       └─────────────────────────────┘   │   │ │
│   │           └─────────────────────────────────────────────────────────┘   │ │
│   │                                        │                                │ │
│   └────────────────────────────────────────┼────────────────────────────────┘ │
│                                            │                                  │
└────────────────────────────────────────────┼──────────────────────────────────┘
                                             │ Update
                                             ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                         Kubernetes Resources                                  │
│                                                                              │
│   ┌────────────────────────┐    ┌────────────────────────┐                   │
│   │ CiliumLoadBalancer     │    │ CiliumCIDRGroup        │                   │
│   │ IPPool                 │    │                        │                   │
│   │                        │    │ • Network policies     │                   │
│   │ • LB-IPAM allocation   │    │ • Egress rules         │                   │
│   │ • Service IPs          │    │                        │                   │
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
│   │ external-dns           │─────────▶ Route53 / CloudFlare                  │
│   │                        │           (AAAA record update)                  │
│   │ • Watches LB services  │                                                 │
│   │ • Updates DNS          │                                                 │
│   └────────────────────────┘                                                 │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

## Component Details

### 1. Prefix Acquisition Layer

#### DHCPv6-PD Client

The primary method for obtaining IPv6 prefixes. Uses the [insomniacslk/dhcp](https://github.com/insomniacslk/dhcp) library.

**Protocol Flow:**
```
Client                          Server
   │                               │
   │──── SOLICIT (IA_PD) ─────────▶│
   │                               │
   │◀─── ADVERTISE (IA_PD) ────────│
   │                               │
   │──── REQUEST (IA_PD) ──────────▶│
   │                               │
   │◀─── REPLY (IA_PD, prefix) ────│
   │                               │
   │    ... lease active ...       │
   │                               │
   │──── RENEW (before T1) ────────▶│
   │                               │
   │◀─── REPLY (renewed lease) ────│
   │                               │
```

**Key Features:**
- Configurable prefix length request (e.g., /60 from a /56)
- DUID persistence for stable identity
- T1/T2 timer management for renewal
- Rebind fallback if renewal fails

#### Router Advertisement Monitor

Fallback/supplementary prefix detection using [mdlayher/ndp](https://github.com/mdlayher/ndp).

**What it monitors:**
- ICMPv6 Router Advertisements (Type 134)
- Prefix Information Options (PIO)
- Valid and preferred lifetimes

**Use cases:**
- Networks without DHCPv6-PD
- Prefix validation (cross-check with DHCPv6-PD)
- Faster detection of prefix changes

### 2. Controller Layer

#### DynamicPrefix Controller

**Responsibilities:**
- Manages the lifecycle of DynamicPrefix resources
- Starts/stops prefix acquirers based on spec
- Calculates subnets from acquired prefix
- Maintains prefix history
- Handles graceful transitions

**Reconciliation Triggers:**
- DynamicPrefix create/update/delete
- Prefix change detected by acquirer
- Lease expiry approaching
- Periodic resync

#### DynamicPrefixBinding Controller

**Responsibilities:**
- Watches DynamicPrefix status changes
- Updates target resources (pools, groups) via backends
- Manages binding status
- Handles update strategies (Replace vs Append)

**Reconciliation Triggers:**
- DynamicPrefixBinding create/update/delete
- Referenced DynamicPrefix status change
- Target resource modification

### 3. Backend Layer

#### Backend Interface

```go
type Backend interface {
    Name() string
    Supports(target TargetRef) bool
    Update(ctx context.Context, target TargetRef, prefix netip.Prefix, strategy UpdateStrategy) error
    Remove(ctx context.Context, target TargetRef, prefix netip.Prefix) error
    Validate(ctx context.Context, target TargetRef) error
}
```

#### Cilium LB-IPAM Backend

Updates `CiliumLoadBalancerIPPool` resources:

```yaml
apiVersion: cilium.io/v2alpha1
kind: CiliumLoadBalancerIPPool
metadata:
  name: ipv6-pool
spec:
  blocks:
    - cidr: "2001:db8:1234::1000/120"  # Updated by operator
```

**Update Strategies:**
- `Replace`: Overwrites all blocks with new prefix
- `Append`: Adds new prefix, keeps existing (for graceful transitions)

#### Cilium CIDRGroup Backend

Updates `CiliumCIDRGroup` resources for network policies:

```yaml
apiVersion: cilium.io/v2
kind: CiliumCIDRGroup
metadata:
  name: home-prefix
spec:
  externalCIDRs:
    - "2001:db8:1234::/60"  # Updated by operator
```

### 4. Transition Layer

Manages graceful prefix transitions to minimize service disruption.

**Transition Flow:**

```
Time ──────────────────────────────────────────────────────────────▶

     Prefix A Active          Both Active              Prefix B Active
     ┌────────────────────────┬─────────────────────────┬─────────────────
     │                        │                         │
     │  Pool: [A]             │  Pool: [A, B]           │  Pool: [B]
     │  DNS: A                │  DNS: B                 │  DNS: B
     │                        │  (external-dns updates) │
     │                        │                         │
     └────────────────────────┴─────────────────────────┴─────────────────
                              │                         │
                        Prefix change              Drain complete
                        detected                   (configurable)
```

**Key Mechanisms:**
- Append new prefix before removing old
- Configurable drain period (default: 60 minutes)
- DNS TTL consideration
- Event emission for monitoring

## Data Flow

### Prefix Acquisition Flow

```
1. Operator starts
2. Reads DynamicPrefix CR
3. Creates DHCPv6-PD client for specified interface
4. Client sends SOLICIT with IA_PD
5. Receives ADVERTISE, sends REQUEST
6. Receives REPLY with delegated prefix
7. Updates DynamicPrefix status.currentPrefix
8. Calculates subnets per spec
9. Updates status.subnets
10. Triggers binding reconciliation
11. Schedules renewal based on T1
```

### Binding Update Flow

```
1. DynamicPrefix status changes
2. DynamicPrefixBinding controller triggered
3. Reads binding spec (target, strategy)
4. Gets appropriate backend for target kind
5. Calls backend.Update() with new prefix
6. Updates binding status
7. Emits BindingSynced event
```

### Graceful Transition Flow

```
1. New prefix detected (different from current)
2. Start transition:
   a. Add new prefix to all bindings (Append)
   b. Record old prefix in history
   c. Start drain timer
3. During drain:
   a. Both prefixes active in pools
   b. New IPs allocated from new prefix
   c. DNS updates to new prefix
4. Drain complete:
   a. Remove old prefix from bindings
   b. Update history state to "expired"
   c. Emit DrainCompleted event
```

## Custom Resource Definitions

### DynamicPrefix

**Purpose:** Represents a dynamically acquired IPv6 prefix

**Spec:**
- `acquisition`: How to obtain the prefix (DHCPv6-PD, RA)
- `subnets`: How to subdivide the prefix
- `transition`: Graceful transition settings

**Status:**
- `currentPrefix`: Currently active prefix
- `prefixSource`: How prefix was acquired
- `leaseExpiresAt`: When DHCP lease expires
- `subnets`: Calculated subnet CIDRs
- `history`: Previous prefixes with states
- `conditions`: Standard Kubernetes conditions

### DynamicPrefixBinding

**Purpose:** Binds a DynamicPrefix subnet to a target resource

**Spec:**
- `prefixRef`: Reference to DynamicPrefix and subnet
- `target`: Target resource (kind, name, namespace)
- `updateStrategy`: How to update target (Replace, Append)

**Status:**
- `boundPrefix`: Currently bound prefix
- `targetSynced`: Whether target is up to date
- `lastSyncTime`: Last successful sync
- `lastSyncError`: Last error if any

## Failure Modes and Recovery

### DHCPv6-PD Server Unavailable

**Detection:** SOLICIT timeout, no ADVERTISE received

**Recovery:**
1. Retry with exponential backoff
2. Fall back to RA monitoring if configured
3. Keep using cached prefix if valid lifetime remains
4. Emit PrefixAcquisitionFailed event

### Prefix Change During Active Connections

**Detection:** New prefix differs from current

**Recovery:**
1. Start graceful transition (don't immediately remove old)
2. Both prefixes active during drain period
3. New connections use new prefix
4. Old connections drain naturally
5. Remove old prefix after drain

### Backend Update Failure

**Detection:** Backend.Update() returns error

**Recovery:**
1. Retry with exponential backoff
2. Update binding status with error
3. Emit BindingSyncFailed event
4. Continue retrying on subsequent reconciliations

### Operator Restart

**Detection:** N/A (startup condition)

**Recovery:**
1. Read all DynamicPrefix CRs
2. Re-establish prefix acquirers
3. Reconcile all bindings
4. Resume any pending drain timers

## Security Considerations

### Network Access

The operator requires raw socket access for DHCPv6 and NDP:

```yaml
securityContext:
  capabilities:
    add:
      - NET_RAW
      - NET_BIND_SERVICE
```

**Host network required** for binding to physical interfaces.

### RBAC

Minimal permissions following principle of least privilege:

- Read/write own CRDs (DynamicPrefix, DynamicPrefixBinding)
- Update Cilium resources (pools, groups)
- Create events
- Leader election resources

### Secret Handling

Currently no secrets required. Future consideration:
- Encrypted DUID storage
- Backend credentials if needed

## Observability

### Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `dynamic_prefix_acquisitions_total` | Counter | Total prefix acquisitions by status |
| `dynamic_prefix_changes_total` | Counter | Prefix changes detected |
| `dynamic_prefix_lease_expiry_seconds` | Gauge | Seconds until lease expires |
| `dynamic_prefix_binding_sync_duration_seconds` | Histogram | Binding sync latency |
| `dynamic_prefix_drain_period_remaining_seconds` | Gauge | Drain time remaining |

### Events

| Event | When |
|-------|------|
| `PrefixAcquired` | New prefix obtained |
| `PrefixChanged` | Prefix changed |
| `PrefixRenewed` | Lease renewed |
| `PrefixExpired` | Lease expired without renewal |
| `BindingSynced` | Backend successfully updated |
| `BindingSyncFailed` | Backend update failed |
| `DrainStarted` | Transition drain started |
| `DrainCompleted` | Old prefix removed |

### Logging

Structured JSON logging via zap:

```json
{
  "level": "info",
  "ts": "2025-01-03T10:00:00Z",
  "logger": "dynamicprefix-controller",
  "msg": "prefix changed",
  "dynamicprefix": "home-ipv6",
  "old_prefix": "2001:db8:old::/60",
  "new_prefix": "2001:db8:new::/60",
  "source": "dhcpv6-pd"
}
```

## Extensibility

### Adding New Backends

1. Implement the `Backend` interface
2. Register in backend manager
3. Add target kind to CRD enum
4. Document configuration

Example for MetalLB:

```go
// internal/backend/metallb/addresspool.go
type AddressPoolBackend struct {
    client client.Client
}

func (b *AddressPoolBackend) Name() string {
    return "metallb-addresspool"
}

func (b *AddressPoolBackend) Supports(target TargetRef) bool {
    return target.Kind == "IPAddressPool"
}

func (b *AddressPoolBackend) Update(ctx context.Context, target TargetRef, prefix netip.Prefix, strategy UpdateStrategy) error {
    // Implementation
}
```

### Adding New Prefix Sources

1. Implement the `Acquirer` interface
2. Add configuration to DynamicPrefix spec
3. Register in acquirer manager
4. Document configuration

Example for static configuration:

```go
// internal/prefix/static/provider.go
type StaticProvider struct {
    prefix netip.Prefix
}

func (p *StaticProvider) Acquire(ctx context.Context) (*Prefix, error) {
    return &Prefix{
        Network:       p.prefix,
        ValidLifetime: time.Hour * 24 * 365, // 1 year
        Source:        PrefixSourceStatic,
        AcquiredAt:    time.Now(),
    }, nil
}
```
