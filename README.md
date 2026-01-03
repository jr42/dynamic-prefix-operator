# Dynamic Prefix Operator

A Kubernetes operator that manages dynamic IPv6 prefix delegation for bare-metal and home/SOHO Kubernetes clusters.

## The Problem

**You can host services from as many IPv6 addresses as you want — until you can't.**

IPv6 promises virtually unlimited addresses. With a /48 or /56 prefix, you could theoretically assign unique global addresses to every service, pod, and device in your infrastructure. No more NAT, no more port conflicts, just direct end-to-end connectivity.

Then reality hits.

### The Dynamic Prefix Problem

Many ISPs, particularly residential and SOHO providers like **Deutsche Telekom**, assign IPv6 prefixes dynamically. These prefixes change:
- Daily or weekly for "privacy" reasons
- After router reboots
- After DHCP lease expiration
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

1. **Actively acquiring prefixes** via DHCPv6 Prefix Delegation (DHCPv6-PD) — acting as a proper DHCPv6 client rather than passively observing SLAAC
2. **Monitoring prefix changes** through Router Advertisement (RA) observation as a fallback
3. **Updating Kubernetes resources** automatically when prefixes change:
   - Cilium `CiliumLoadBalancerIPPool` for LB-IPAM
   - Cilium `CiliumCIDRGroup` for network policies
   - Extensible to other CNIs (Calico, MetalLB, etc.)
4. **Coordinating DNS updates** through proper integration with external-dns
5. **Managing graceful transitions** to minimize service disruption

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                     Dynamic Prefix Operator                         │
│                                                                     │
│  ┌─────────────────────┐      ┌─────────────────────────────────┐  │
│  │   Prefix Acquirer   │      │        Pool Reconciler          │  │
│  │                     │      │                                 │  │
│  │  • DHCPv6-PD Client │      │  • CiliumLoadBalancerIPPool    │  │
│  │  • RA Monitor       │─────▶│  • CiliumCIDRGroup             │  │
│  │  • Lease Manager    │      │  • Future: Calico IPPool       │  │
│  │                     │      │  • Future: MetalLB AddressPool │  │
│  └─────────────────────┘      └─────────────────────────────────┘  │
│           │                                │                        │
│           ▼                                ▼                        │
│  ┌─────────────────────┐      ┌─────────────────────────────────┐  │
│  │  Prefix Store (CRD) │      │     Transition Controller       │  │
│  │                     │      │                                 │  │
│  │  • Current prefix   │      │  • Graceful prefix migration   │  │
│  │  • Prefix history   │      │  • Drain period management     │  │
│  │  • Lease state      │      │  • Service IP coordination     │  │
│  └─────────────────────┘      └─────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
                                      │
                                      ▼
                    ┌─────────────────────────────────────┐
                    │         Kubernetes Resources         │
                    │                                     │
                    │  CiliumLoadBalancerIPPool           │
                    │  CiliumCIDRGroup                    │
                    │  Service annotations                │
                    │         ↓                           │
                    │  external-dns → Route53/CloudFlare  │
                    └─────────────────────────────────────┘
```

## Key Features

### DHCPv6 Prefix Delegation Client
Unlike passive SLAAC monitoring, the operator actively participates in DHCPv6-PD:
- Requests prefix delegation from upstream router/ISP
- Manages lease lifecycle (renew, rebind)
- Handles rapid prefix changes gracefully
- Uses the well-maintained [insomniacslk/dhcp](https://github.com/insomniacslk/dhcp) library

### Router Advertisement Fallback
For networks without DHCPv6-PD or as supplementary detection:
- Monitors RAs using the [mdlayher/ndp](https://github.com/mdlayher/ndp) library
- Detects prefix information from router advertisements
- Validates prefix consistency across sources

### Cilium Integration (Primary Target)
Native support for Cilium networking:
- Updates `CiliumLoadBalancerIPPool` CIDR blocks for LB-IPAM
- Updates `CiliumCIDRGroup` for network policy matching
- Respects Cilium's allocation semantics

### Extensible Backend Architecture
Designed for future CNI support:
- Plugin interface for different IP pool backends
- Calico IPPool support (planned)
- MetalLB AddressPool support (planned)
- Custom backend plugins via CRD

### Graceful Transition Management
Minimizes disruption during prefix changes:
- Maintains both old and new prefixes during transition
- Configurable drain periods before removing old prefix
- Coordinates with external-dns for DNS migration
- Emits events and metrics for observability

## Custom Resource Definitions

### DynamicPrefix
The primary CRD representing a managed IPv6 prefix:

```yaml
apiVersion: network.reith.cloud/v1alpha1
kind: DynamicPrefix
metadata:
  name: home-ipv6
spec:
  # Prefix acquisition method
  acquisition:
    # Primary: DHCPv6-PD
    dhcpv6:
      interface: enp1s0.222
      requestedPrefixLength: 60  # Request a /60 from the /56
      duid: "auto"  # Or explicit DUID
    # Fallback: RA monitoring
    routerAdvertisement:
      interface: enp1s0.222
      enabled: true

  # Subnet allocation from acquired prefix
  subnets:
    - name: loadbalancers
      offset: 0x1000        # Offset within the prefix
      prefixLength: 120     # /120 = 256 addresses

    - name: services
      offset: 0x2000
      prefixLength: 112     # /112 = 65536 addresses

  # Transition settings
  transition:
    drainPeriodMinutes: 60  # Keep old prefix for 1 hour
    maxPrefixHistory: 2     # Retain last 2 prefixes

status:
  # Current state
  currentPrefix: "2001:db8:1234::/60"
  prefixSource: "dhcpv6-pd"
  leaseExpiresAt: "2025-01-04T10:00:00Z"

  # Allocated subnets
  subnets:
    - name: loadbalancers
      cidr: "2001:db8:1234::1000/120"
    - name: services
      cidr: "2001:db8:1234::2000/112"

  # Prefix history
  history:
    - prefix: "2001:db8:old::/60"
      acquiredAt: "2025-01-01T10:00:00Z"
      deprecatedAt: "2025-01-03T10:00:00Z"
      state: "draining"

  conditions:
    - type: PrefixAcquired
      status: "True"
      lastTransitionTime: "2025-01-03T10:00:00Z"
    - type: PoolsSynced
      status: "True"
      lastTransitionTime: "2025-01-03T10:05:00Z"
```

### DynamicPrefixBinding
Binds a DynamicPrefix to a specific backend pool:

```yaml
apiVersion: network.reith.cloud/v1alpha1
kind: DynamicPrefixBinding
metadata:
  name: cilium-lb-pool
spec:
  # Reference to the DynamicPrefix
  prefixRef:
    name: home-ipv6
    subnet: loadbalancers  # Which subnet to use

  # Target pool to update
  target:
    kind: CiliumLoadBalancerIPPool
    name: ipv6-vlan222-pool
    namespace: kube-system

  # How to update the target
  updateStrategy:
    type: Replace  # Or "Append" for multi-source pools

status:
  boundPrefix: "2001:db8:1234::1000/120"
  targetSynced: true
  lastSyncTime: "2025-01-03T10:05:00Z"
```

## Installation

```bash
# Using Helm (recommended)
helm repo add dynamic-prefix-operator https://charts.reith.cloud
helm install dynamic-prefix-operator dynamic-prefix-operator/dynamic-prefix-operator

# Using Kustomize
kubectl apply -k https://github.com/jr42/dynamic-prefix-operator//config/default

# Using kubectl
kubectl apply -f https://github.com/jr42/dynamic-prefix-operator/releases/latest/download/install.yaml
```

## Quick Start

1. Install the operator (see above)

2. Create a DynamicPrefix for your network:
```yaml
apiVersion: network.reith.cloud/v1alpha1
kind: DynamicPrefix
metadata:
  name: home-ipv6
spec:
  acquisition:
    dhcpv6:
      interface: eth0
      requestedPrefixLength: 60
  subnets:
    - name: loadbalancers
      offset: 0x1000
      prefixLength: 120
```

3. Bind it to your Cilium LB-IPAM pool:
```yaml
apiVersion: network.reith.cloud/v1alpha1
kind: DynamicPrefixBinding
metadata:
  name: cilium-binding
spec:
  prefixRef:
    name: home-ipv6
    subnet: loadbalancers
  target:
    kind: CiliumLoadBalancerIPPool
    name: my-ipv6-pool
    namespace: kube-system
```

4. Watch the magic happen:
```bash
kubectl get dynamicprefix home-ipv6 -w
```

## Comparison with Alternatives

| Feature | Dynamic Prefix Operator | k6u | Manual Script |
|---------|------------------------|-----|---------------|
| DHCPv6-PD Client | ✅ | ❌ (SLAAC only) | ❌ |
| Lease Management | ✅ | ❌ | ❌ |
| Cilium LB-IPAM | ✅ | ❌ (CIDRGroup only) | ✅ |
| Cilium CIDRGroup | ✅ | ✅ | ❌ |
| Graceful Transitions | ✅ | ❌ | ❌ |
| Multiple Backends | ✅ | ❌ | ❌ |
| Kubernetes Native | ✅ | ✅ | ❌ |
| Metrics/Observability | ✅ | ❌ | ❌ |

## Roadmap

- [x] Core operator framework (kubebuilder)
- [x] DHCPv6-PD client integration
- [x] Router Advertisement monitoring
- [x] Cilium LB-IPAM integration
- [x] Cilium CIDRGroup integration
- [ ] Calico IPPool backend
- [ ] MetalLB AddressPool backend
- [ ] Multi-cluster support
- [ ] BGP prefix announcement integration
- [ ] Web UI for prefix visualization

## Contributing

Contributions are welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Apache License 2.0. See [LICENSE](LICENSE) for details.

## Acknowledgments

- [insomniacslk/dhcp](https://github.com/insomniacslk/dhcp) - DHCPv6 library
- [mdlayher/ndp](https://github.com/mdlayher/ndp) - NDP/RA library
- [k6u](https://github.com/0xC0ncord/k6u) - Inspiration for CiliumCIDRGroup updates
- [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) - Kubernetes controller framework
