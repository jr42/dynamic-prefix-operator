# Dynamic Prefix Operator

A Kubernetes operator that manages dynamic IPv6 prefix delegation for bare-metal and home/SOHO Kubernetes clusters.

## The Problem

**You can host services from as many IPv6 addresses as you want — until you can't.**

IPv6 promises virtually unlimited addresses. With a /48 or /56 prefix, you could theoretically assign unique global addresses to every service, pod, and device in your infrastructure. No more NAT, no more port conflicts, just direct end-to-end connectivity.

Then reality hits.

### The Dynamic Prefix Problem

Many residential and SOHO ISPs assign IPv6 prefixes dynamically. These prefixes change:
- Daily or weekly for "privacy" reasons
- After router reboots
- After DHCPv6 lease expiration
- Randomly, because ISPs gonna ISP

When your prefix changes from `2001:db8:1234::/64` to `2001:db8:5678::/64`, **everything breaks**:

- **LoadBalancer IPs** become unreachable (Cilium LB-IPAM pools are static)
- **DNS records** point to stale addresses
- **Firewall rules** reference invalid CIDRs
- **Network policies** stop matching traffic

The "solution" many resort to? **NAT66** — taking the beautiful end-to-end transparency of IPv6 and bolting the same ugly NAT architecture that made IPv4 a nightmare.

### Why This Matters for Kubernetes

Kubernetes on bare-metal or at home/SOHO is increasingly popular:
- Talos Linux makes cluster management trivial
- Cilium provides powerful networking without cloud dependencies
- ArgoCD enables GitOps for home infrastructure

But all of this assumes **stable IP addressing**. Cloud providers give you static IPs. Your home ISP gives you a prefix that changes every time the wind blows.

## The Solution

**Dynamic Prefix Operator** bridges this gap by:

1. **Monitoring prefix changes** via Router Advertisement observation
2. **Calculating address ranges** from the received prefix automatically
3. **Updating Cilium resources** (LoadBalancerIPPool, CIDRGroup) when prefixes change
4. **Managing graceful transitions** to minimize service disruption

## Quick Start

### 1. Install the operator

```bash
# Using Helm
helm install dynamic-prefix-operator oci://ghcr.io/jr42/dynamic-prefix-operator/helm/dynamic-prefix-operator

# Or using kubectl
kubectl apply -f https://github.com/jr42/dynamic-prefix-operator/releases/latest/download/install.yaml
```

### 2. Create a DynamicPrefix with Address Ranges

The recommended approach for home/SOHO: reserve a portion of your /64 that your router won't hand out via DHCPv6/SLAAC.

```yaml
apiVersion: dynamic-prefix.io/v1alpha1
kind: DynamicPrefix
metadata:
  name: home-ipv6
spec:
  acquisition:
    routerAdvertisement:
      interface: eth0
      enabled: true

  # Reserve ::f000:0:0:0 through ::ffff:ffff:ffff:ffff for Kubernetes services
  # Configure your router to NOT assign addresses in this range via SLAAC/DHCPv6
  addressRanges:
    - name: loadbalancers
      start: "::f000:0:0:0"
      end: "::ffff:ffff:ffff:ffff"
```

### 3. Create a Cilium pool that references it

```yaml
apiVersion: cilium.io/v2alpha1
kind: CiliumLoadBalancerIPPool
metadata:
  name: ipv6-lb-pool
  annotations:
    dynamic-prefix.io/name: home-ipv6
    dynamic-prefix.io/address-range: loadbalancers
spec:
  blocks: []  # Operator manages this
```

### 4. Watch the operator populate the pool

```bash
kubectl get ciliumloadbalancerippool ipv6-lb-pool -o yaml
# spec.blocks now contains the actual address range from your prefix:
# - start: "2001:db8:1234:0:f000::"
#   stop: "2001:db8:1234:0:ffff:ffff:ffff:ffff"
```

When your prefix changes, the operator automatically updates all annotated pools.

## Architecture

```
                         Upstream Router / ISP
                                  │
                                  │ Router Advertisement
                                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     Dynamic Prefix Operator                         │
│                                                                     │
│  ┌─────────────────────┐      ┌─────────────────────────────────┐  │
│  │   Prefix Receiver   │      │     Pool Sync Controller        │  │
│  │                     │      │                                 │  │
│  │  • RA Monitor       │─────▶│  Updates pools that reference   │  │
│  │  • Prefix Detection │      │  DynamicPrefix via annotations: │  │
│  │                     │      │                                 │  │
│  └─────────────────────┘      │  • CiliumLoadBalancerIPPool    │  │
│           │                   │  • CiliumCIDRGroup             │  │
│           ▼                   └─────────────────────────────────┘  │
│  ┌─────────────────────┐                     │                      │
│  │  DynamicPrefix CR   │                     │                      │
│  │                     │                     ▼                      │
│  │  • Current prefix   │      ┌─────────────────────────────────┐  │
│  │  • Address ranges   │      │  Pools with annotation:         │  │
│  │  • Lease state      │      │  dynamic-prefix.io/name: xxx    │  │
│  └─────────────────────┘      └─────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

## Address Range Mode (Recommended)

For most home/SOHO setups, you receive a /64 prefix from your ISP. The operator lets you **reserve a portion of that /64** for Kubernetes services.

**How it works:**
1. Configure your router to NOT hand out addresses in a specific range (e.g., `::f000:0:0:0` to `::ffff:ffff:ffff:ffff`)
2. Tell the operator about this reserved range
3. The operator monitors RAs for prefix changes and updates your Cilium pools with the full addresses

**Advantages:**
- Works with standard /64 allocations
- No BGP required
- Simple router configuration (just exclude a range from DHCPv6/SLAAC)

```yaml
spec:
  addressRanges:
    - name: loadbalancers
      start: "::f000:0:0:0"        # Lower bound suffix
      end: "::ffff:ffff:ffff:ffff"  # Upper bound suffix
```

## Subnet Mode (Advanced)

If you receive a larger prefix (e.g., /56 or /48), you can carve out dedicated /64 subnets. This mode requires BGP to announce the subnets to your router.

> **Note:** BGP integration is planned but not yet implemented. Use Address Range mode for now.

```yaml
spec:
  subnets:
    - name: loadbalancers
      offset: 0
      prefixLength: 64
```

## Graceful Prefix Transitions

When your ISP changes your prefix, the operator supports two transition modes to minimize service disruption:

### Simple Mode (Default)

Keeps multiple address blocks in pools during transitions. Services retain their old IPs until the historical blocks are removed.

```yaml
spec:
  transition:
    mode: simple           # Default
    maxPrefixHistory: 2    # Keep 2 previous prefixes in pool blocks
```

**How it works:**
1. Prefix changes from A → B
2. Pool now has blocks for both prefix A and B
3. Existing services keep their prefix-A IPs
4. New services get prefix-B IPs
5. After another prefix change (B → C), oldest block (A) is dropped

### HA Mode (High Availability)

For zero-downtime transitions, HA mode manages both LoadBalancer IPs and DNS targeting:

```yaml
spec:
  transition:
    mode: ha
    maxPrefixHistory: 2
```

**How it works:**
1. Prefix changes from A → B
2. Service gets **both** IPs via `lbipam.cilium.io/ips` annotation
3. DNS points to **new IP only** via `external-dns.alpha.kubernetes.io/target`
4. Old connections continue working (both IPs active on Service)
5. New clients connect to new IP via DNS

```yaml
# HA Mode result on Service:
annotations:
  lbipam.cilium.io/ips: "2001:db8:new::1,2001:db8:old::1"  # Both IPs active
  external-dns.alpha.kubernetes.io/target: "2001:db8:new::1"  # DNS → new only
```

### Annotations for HA Mode Services

| Annotation | Description |
|------------|-------------|
| `dynamic-prefix.io/name` | Name of the DynamicPrefix CR (required) |
| `dynamic-prefix.io/service-address-range` | Which address range for IP calculation |
| `dynamic-prefix.io/service-subnet` | Which subnet for IP calculation |

## Supported Annotations

Add these annotations to Cilium resources to have them managed by the operator:

| Annotation | Description |
|------------|-------------|
| `dynamic-prefix.io/name` | Name of the DynamicPrefix CR to reference |
| `dynamic-prefix.io/address-range` | Name of the address range to use (Mode 1) |
| `dynamic-prefix.io/subnet` | Name of the subnet to use (Mode 2) |

## Supported Resources

- **CiliumLoadBalancerIPPool** — for Cilium LB-IPAM (`spec.blocks` with start/stop)
- **CiliumCIDRGroup** — for network policies (`spec.externalCIDRs`)

## Configuration Reference

### DynamicPrefix Spec

```yaml
apiVersion: dynamic-prefix.io/v1alpha1
kind: DynamicPrefix
metadata:
  name: home-ipv6
spec:
  # How to receive the prefix
  acquisition:
    routerAdvertisement:
      interface: eth0    # Interface to monitor for RAs
      enabled: true

  # Address ranges within the /64 (recommended for home/SOHO)
  addressRanges:
    - name: loadbalancers
      start: "::f000:0:0:0"
      end: "::ffff:ffff:ffff:ffff"

  # Transition settings
  transition:
    mode: simple            # "simple" (default) or "ha" for high availability
    maxPrefixHistory: 2     # Number of historical prefixes to retain in pool blocks
```

### Status

```yaml
status:
  currentPrefix: "2001:db8:1234::/64"
  prefixSource: "router-advertisement"

  addressRanges:
    - name: loadbalancers
      start: "2001:db8:1234:0:f000::"
      end: "2001:db8:1234:0:ffff:ffff:ffff:ffff"

  conditions:
    - type: PrefixAcquired
      status: "True"
    - type: PoolsSynced
      status: "True"
```

## Requirements

- Kubernetes 1.28+
- Cilium (for LB-IPAM pools)
- `hostNetwork: true` for the operator pod (to see Router Advertisements)
- `NET_RAW` capability (for raw ICMPv6 sockets)

## Prefix Change Behavior

When your ISP changes your prefix:

1. **Detection**: The RA receiver detects the new prefix within seconds
2. **Status Update**: DynamicPrefix status is updated with new prefix and calculated ranges
3. **Pool Sync**: All annotated Cilium pools are updated with both old and new blocks
4. **Service Sync** (HA mode): Services get both IPs, DNS points to new IP only
5. **DNS Update**: external-dns updates records based on Service IPs or target override

### Simple Mode (Default)
- Pools contain multiple blocks (current + historical prefixes)
- Existing Services keep their old IPs until pool blocks are pruned
- New Services get IPs from the current prefix block

### HA Mode
- Services are updated with **all** active IPs (old + new)
- DNS target annotation ensures new clients get the new IP
- Old connections continue working until they naturally close
- Zero-downtime for properly configured setups

**Recommendations**:
- Use short DNS TTLs (60-300s) so clients get new IPs quickly
- Use HA mode if you need zero-downtime during prefix transitions
- Ensure your applications handle reconnection gracefully
- Monitor the `PrefixAcquired` condition for alerting

## Roadmap

- [x] Core operator framework (kubebuilder)
- [x] Router Advertisement monitoring
- [x] Address range mode (within /64)
- [x] Cilium LB-IPAM integration
- [x] Cilium CIDRGroup integration
- [x] Graceful prefix transitions (simple mode)
- [x] HA mode with multi-IP Services and DNS targeting
- [ ] DHCPv6-PD client (act as PD client)
- [ ] BGP integration for subnet mode
- [ ] Calico IPPool backend
- [ ] MetalLB IPAddressPool backend

## Contributing

Contributions are welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Apache License 2.0. See [LICENSE](LICENSE) for details.

## Acknowledgments

- [mdlayher/ndp](https://github.com/mdlayher/ndp) — NDP/RA library
- [1Password Operator](https://github.com/1Password/onepassword-operator) — Inspiration for annotation-based binding
- [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) — Kubernetes controller framework
