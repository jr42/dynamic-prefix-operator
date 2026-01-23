# Architecture Overview

## System Context

```
                              Upstream Router / ISP
                                       │
                                       │ Router Advertisements (prefix info)
                                       │
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
│   │  │  │ RA Monitor      │         │ DHCPv6-PD Client│                │   │ │
│   │  │  │ (Primary)       │         │ (Future)        │                │   │ │
│   │  │  │                 │         │                 │                │   │ │
│   │  │  │ • Parse RAs     │         │ • SOLICIT/REPLY │                │   │ │
│   │  │  │ • Extract PIOs  │         │ • Lease renewal │                │   │ │
│   │  │  └────────┬────────┘         └────────┬────────┘                │   │ │
│   │  │           └──────────┬───────────────┘                          │   │ │
│   │  │                      ▼                                          │   │ │
│   │  │           ┌──────────────────────┐                              │   │ │
│   │  │           │    Prefix Store      │                              │   │ │
│   │  │           │  • Current prefix    │                              │   │ │
│   │  │           │  • Address ranges    │                              │   │ │
│   │  │           └──────────────────────┘                              │   │ │
│   │  └─────────────────────────────────────────────────────────────────┘   │ │
│   │                            │                                            │ │
│   │                            ▼                                            │ │
│   │  ┌─────────────────────────────────────────────────────────────────┐   │ │
│   │  │                      Controllers                                 │   │ │
│   │  │                                                                  │   │ │
│   │  │  DynamicPrefix Controller        Pool Sync Controller            │   │ │
│   │  │  • Manages prefix lifecycle      • Watches annotated pools       │   │ │
│   │  │  • Calculates address ranges     • Updates CIDRs on change       │   │ │
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
│   │     address-range: lb  │    │     address-range: lb  │                   │
│   │                        │    │                        │                   │
│   │ spec.blocks:           │    │ spec.externalCIDRs:    │                   │
│   │   - start: <addr>      │    │   [CIDR]               │                   │
│   │     stop: <addr>       │    │                        │                   │
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

#### Router Advertisement Monitor (Primary)

The operator monitors Router Advertisements using [mdlayher/ndp](https://github.com/mdlayher/ndp) to detect the current IPv6 prefix.

**What it monitors:**
- ICMPv6 Router Advertisements (Type 134)
- Prefix Information Options (PIO)
- Filters for Global Unicast Addresses (GUA) over ULA
- Tracks valid and preferred lifetimes

**Why RA monitoring:**
- Works when another service (Talos, systemd-networkd) handles DHCPv6-PD
- Passive observation doesn't conflict with existing prefix delegation
- Simpler than running a DHCPv6-PD client

#### DHCPv6-PD Client (Future)

For environments where the operator should act as the DHCPv6 Prefix Delegation client:

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

### 2. Controller Layer

#### DynamicPrefix Controller

**Responsibilities:**
- Manages DynamicPrefix resource lifecycle
- Starts/stops prefix receivers
- Calculates address ranges from received prefix
- Updates CR status

**Reconciliation Triggers:**
- DynamicPrefix create/update/delete
- Prefix change from receiver
- Lease expiry approaching (if DHCPv6-PD)

#### Pool Sync Controller

**Responsibilities:**
- Watches for pools with `dynamic-prefix.io/*` annotations
- Looks up referenced DynamicPrefix
- Updates pool spec from DynamicPrefix status
- Re-reconciles when DynamicPrefix changes

**Annotation-Based Binding:**

```yaml
# Pool references DynamicPrefix via annotation
apiVersion: cilium.io/v2alpha1
kind: CiliumLoadBalancerIPPool
metadata:
  annotations:
    dynamic-prefix.io/name: home-ipv6           # Which DynamicPrefix
    dynamic-prefix.io/address-range: loadbalancers  # Which address range
spec:
  blocks: []  # Operator manages this
```

This follows the [1Password Operator](https://github.com/1Password/onepassword-operator) pattern:
- Simpler than explicit binding CRDs
- Pools are self-documenting
- No orphaned resources

## Data Flow

### Prefix Reception Flow

```
1. Operator starts, reads DynamicPrefix CR
2. Creates RA monitor for specified interface
3. Monitors for Router Advertisements
4. Extracts prefix from PIOs (prefers GUA over ULA)
5. Updates DynamicPrefix status.currentPrefix
6. Calculates address ranges per spec
7. Updates status.addressRanges
```

### Pool Update Flow

```
1. Pool created with dynamic-prefix.io/name annotation
2. Pool sync controller detects annotation
3. Looks up referenced DynamicPrefix
4. Gets address range from status
5. Updates pool's spec.blocks with start/stop
6. Pool is now in sync
```

### Prefix Change Flow

```
1. RA monitor receives new prefix
2. DynamicPrefix controller updates status
3. Pool sync controller sees status change
4. Finds all pools referencing this DynamicPrefix
5. Updates each pool with new address range
6. external-dns sees new LB IPs, updates DNS
```

## Custom Resource Definition

### DynamicPrefix

**Purpose:** Represents a dynamically received IPv6 prefix

**Spec:**
- `acquisition`: How to receive the prefix (RA monitoring, DHCPv6-PD)
- `addressRanges`: Ranges within the /64 (Mode 1)
- `subnets`: How to subdivide the prefix (Mode 2)
- `transition`: Graceful transition settings

**Status:**
- `currentPrefix`: Currently active prefix
- `prefixSource`: How prefix was received
- `addressRanges`: Calculated full addresses
- `subnets`: Calculated subnet CIDRs
- `conditions`: Standard Kubernetes conditions

## Failure Modes and Recovery

### RA Not Received

**Detection:** No RA within expected interval

**Recovery:**
1. Keep using cached prefix if recently valid
2. Emit warning event
3. Set Degraded condition

### Prefix Change

**Detection:** New prefix differs from current

**Recovery:**
1. Update DynamicPrefix status immediately
2. Pool sync controller updates all referencing pools
3. DNS updates via external-dns
4. Keep prefix history for audit

### Operator Restart

**Recovery:**
1. Read all DynamicPrefix CRs
2. Re-establish prefix receivers
3. Reconcile all annotated pools

## Security Considerations

### Network Access

The operator requires raw socket access for ICMPv6:

```yaml
securityContext:
  runAsUser: 0  # Required for raw sockets
  capabilities:
    add:
      - NET_RAW
```

**Host network required** for interface binding (`hostNetwork: true`).

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
| `dynamic_prefix_pools_synced` | Gauge | Pools currently synced |

### Events

| Event | When |
|-------|------|
| `PrefixReceived` | New prefix obtained |
| `PrefixChanged` | Prefix changed |
| `PoolUpdated` | Pool spec updated |

## Extensibility

### Adding New Pool Types

1. Implement pool handler logic in `poolsync_controller.go`
2. Add GVK to watched resources
3. Document annotation usage

Example for MetalLB:

```go
case "metallb.io":
    pool.Spec.Addresses = []string{addressRange.Start + "-" + addressRange.End}
```

### Adding New Prefix Sources

1. Implement `Receiver` interface
2. Add configuration to DynamicPrefix spec
3. Register in receiver factory
