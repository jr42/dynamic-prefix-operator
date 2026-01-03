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

When your prefix changes from `2001:db8:1234::/56` to `2001:db8:5678::/56`, **everything breaks**:

- **LoadBalancer IPs** become unreachable (Cilium LB-IPAM pools are static)
- **DNS records** point to stale addresses
- **Firewall rules** reference invalid CIDRs
- **Network policies** stop matching traffic
- **BGP announcements** advertise dead routes

The "solution" many resort to? **NAT66** — taking the beautiful end-to-end transparency of IPv6 and bolting the same ugly NAT architecture that made IPv4 a nightmare. Your packets get rewritten, your logs become meaningless, and you've solved nothing.

### Why This Matters for Kubernetes

Kubernetes on bare-metal or at home/SOHO is increasingly popular:
- Talos Linux makes cluster management trivial
- Cilium provides powerful networking without cloud dependencies
- ArgoCD enables GitOps for home infrastructure

But all of this assumes **stable IP addressing**. Cloud providers give you static IPs. Your home ISP gives you a prefix that changes every time the wind blows.

## The Solution

**Dynamic Prefix Operator** bridges this gap by:

1. **Receiving prefix delegations** via DHCPv6-PD — acting as a DHCPv6 client that receives prefixes from your upstream router
2. **Monitoring prefix changes** through Router Advertisement (RA) observation as a fallback
3. **Creating and updating Kubernetes resources** automatically when prefixes change
4. **Coordinating DNS updates** through proper integration with external-dns
5. **Managing graceful transitions** to minimize service disruption

## Architecture

```
                         Upstream Router / ISP
                                  │
                                  │ DHCPv6-PD (delegates prefix)
                                  ▼
┌─────────────────────────────────────────────────────────────────────┐
│                     Dynamic Prefix Operator                         │
│                                                                     │
│  ┌─────────────────────┐      ┌─────────────────────────────────┐  │
│  │   Prefix Receiver   │      │        Pool Generator           │  │
│  │                     │      │                                 │  │
│  │  • DHCPv6-PD Client │      │  Creates/updates pools that     │  │
│  │  • RA Monitor       │─────▶│  reference DynamicPrefix:       │  │
│  │  • Lease Manager    │      │                                 │  │
│  │                     │      │  • CiliumLoadBalancerIPPool    │  │
│  └─────────────────────┘      │  • CiliumCIDRGroup             │  │
│           │                   │  • Future: Calico, MetalLB     │  │
│           ▼                   └─────────────────────────────────┘  │
│  ┌─────────────────────┐                     │                      │
│  │  DynamicPrefix CR   │                     │                      │
│  │                     │                     ▼                      │
│  │  • Current prefix   │      ┌─────────────────────────────────┐  │
│  │  • Allocated subnets│      │  Pools with annotation:         │  │
│  │  • Lease state      │      │  dynamic-prefix.io/name: xxx    │  │
│  └─────────────────────┘      └─────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

## Key Features

### DHCPv6 Prefix Delegation Client

The operator acts as a DHCPv6-PD client, receiving delegated prefixes from your upstream router:
- Receives prefix delegation from upstream (router/ISP)
- Manages lease lifecycle (renew, rebind)
- Handles prefix changes gracefully
- Uses the well-maintained [insomniacslk/dhcp](https://github.com/insomniacslk/dhcp) library

### Router Advertisement Fallback

For networks without DHCPv6-PD or as supplementary detection:
- Monitors RAs using the [mdlayher/ndp](https://github.com/mdlayher/ndp) library
- Detects prefix information from router advertisements
- Validates prefix consistency across sources

### Simple Binding Model (1Password-style)

Inspired by the [1Password Operator](https://github.com/1Password/onepassword-operator), pools reference the DynamicPrefix via annotations — no separate binding resource needed:

```yaml
apiVersion: cilium.io/v2alpha1
kind: CiliumLoadBalancerIPPool
metadata:
  name: ipv6-pool
  annotations:
    # This pool is managed by the operator
    dynamic-prefix.io/name: home-ipv6
    dynamic-prefix.io/subnet: loadbalancers
spec:
  # CIDR is automatically populated and updated by the operator
  blocks: []  # Managed by operator
```

The operator watches for pools with the `dynamic-prefix.io/name` annotation and keeps them in sync with the referenced DynamicPrefix.

### Extensible Pool Types

The operator can create and manage different pool types:
- `CiliumLoadBalancerIPPool` — for Cilium LB-IPAM
- `CiliumCIDRGroup` — for network policies
- Future: Calico IPPool, MetalLB IPAddressPool

### Graceful Transition Management

Minimizes disruption during prefix changes:
- Maintains both old and new prefixes during transition
- Configurable drain periods before removing old prefix
- Coordinates with external-dns for DNS migration
- Emits events and metrics for observability

## Custom Resource Definition

### DynamicPrefix

The single CRD representing a managed IPv6 prefix:

```yaml
apiVersion: dynamic-prefix.io/v1alpha1
kind: DynamicPrefix
metadata:
  name: home-ipv6
spec:
  # How to receive the prefix
  acquisition:
    # Primary: Receive via DHCPv6 Prefix Delegation
    dhcpv6pd:
      # Interface where we receive the delegated prefix
      interface: eth0
      # Hint for requested prefix length (optional)
      requestedPrefixLength: 60

    # Fallback: Monitor Router Advertisements
    routerAdvertisement:
      interface: eth0
      enabled: true

  # Subnet allocation from the received prefix
  subnets:
    - name: loadbalancers
      offset: 0x1000        # Offset within the prefix
      prefixLength: 120     # /120 = 256 addresses

    - name: dmz
      offset: 0x2000
      prefixLength: 112

  # Transition settings
  transition:
    drainPeriodMinutes: 60  # Keep old prefix active during transition
    maxPrefixHistory: 2

  # What pool types to create for referencing pools
  poolTypes:
    ciliumLoadBalancerIPPool: true
    ciliumCIDRGroup: true

status:
  currentPrefix: "2001:db8:1234::/60"
  prefixSource: "dhcpv6-pd"
  leaseExpiresAt: "2025-01-04T10:00:00Z"

  subnets:
    - name: loadbalancers
      cidr: "2001:db8:1234::1000/120"
    - name: dmz
      cidr: "2001:db8:1234::2000/112"

  conditions:
    - type: PrefixAcquired
      status: "True"
    - type: PoolsSynced
      status: "True"
```

## How It Works

1. **Create a DynamicPrefix** — defines where to receive the prefix and how to slice it into subnets

2. **Create pools with annotations** — pools reference the DynamicPrefix by name:

```yaml
apiVersion: cilium.io/v2alpha1
kind: CiliumLoadBalancerIPPool
metadata:
  name: external-services
  annotations:
    dynamic-prefix.io/name: home-ipv6      # Reference to DynamicPrefix
    dynamic-prefix.io/subnet: loadbalancers # Which subnet to use
spec:
  blocks: []  # Operator manages this
```

3. **Operator keeps pools in sync** — when the prefix changes, the operator updates all referencing pools automatically

4. **DNS updates automatically** — external-dns sees the new LoadBalancer IPs and updates DNS records

## Installation

```bash
# Using Helm (recommended)
helm repo add dynamic-prefix-operator https://charts.example.com
helm install dynamic-prefix-operator dynamic-prefix-operator/dynamic-prefix-operator

# Using Kustomize
kubectl apply -k https://github.com/jr42/dynamic-prefix-operator//config/default

# Using kubectl
kubectl apply -f https://github.com/jr42/dynamic-prefix-operator/releases/latest/download/install.yaml
```

## Quick Start

1. Install the operator (see above)

2. Create a DynamicPrefix:
```yaml
apiVersion: dynamic-prefix.io/v1alpha1
kind: DynamicPrefix
metadata:
  name: home-ipv6
spec:
  acquisition:
    dhcpv6pd:
      interface: eth0
  subnets:
    - name: loadbalancers
      offset: 0x1000
      prefixLength: 120
```

3. Create a pool that references it:
```yaml
apiVersion: cilium.io/v2alpha1
kind: CiliumLoadBalancerIPPool
metadata:
  name: ipv6-lb-pool
  annotations:
    dynamic-prefix.io/name: home-ipv6
    dynamic-prefix.io/subnet: loadbalancers
spec:
  blocks: []
```

4. Watch the operator populate the pool:
```bash
kubectl get ciliumloadbalancerippool ipv6-lb-pool -o yaml
# spec.blocks now contains the actual CIDR from your prefix
```

## Comparison with Alternatives

| Feature | Dynamic Prefix Operator | k6u | Manual Script |
|---------|------------------------|-----|---------------|
| DHCPv6-PD Client | Yes | No (SLAAC only) | No |
| Lease Management | Yes | No | No |
| Cilium LB-IPAM | Yes | No (CIDRGroup only) | Yes |
| Cilium CIDRGroup | Yes | Yes | No |
| Graceful Transitions | Yes | No | No |
| Multiple Pool Types | Yes | No | No |
| Simple Annotation Binding | Yes | No | N/A |
| Kubernetes Native | Yes | Yes | No |
| Metrics/Observability | Yes | No | No |

## Roadmap

- [x] Core operator framework (kubebuilder)
- [x] DHCPv6-PD client integration
- [x] Router Advertisement monitoring
- [x] Cilium LB-IPAM integration
- [x] Cilium CIDRGroup integration
- [ ] Calico IPPool backend
- [ ] MetalLB IPAddressPool backend
- [ ] Multi-cluster support
- [ ] Web UI for prefix visualization

## Contributing

Contributions are welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Apache License 2.0. See [LICENSE](LICENSE) for details.

## Acknowledgments

- [insomniacslk/dhcp](https://github.com/insomniacslk/dhcp) — DHCPv6 library
- [mdlayher/ndp](https://github.com/mdlayher/ndp) — NDP/RA library
- [1Password Operator](https://github.com/1Password/onepassword-operator) — Inspiration for annotation-based binding
- [k6u](https://github.com/0xC0ncord/k6u) — Inspiration for CiliumCIDRGroup updates
- [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) — Kubernetes controller framework
