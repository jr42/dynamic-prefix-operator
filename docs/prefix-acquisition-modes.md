# Prefix Acquisition Modes

This guide explains how the Dynamic Prefix Operator works with your network setup and helps you choose the right configuration for your environment.

## The Problem: Dynamic IPv6 Prefixes

Most home and small office internet connections receive a dynamic IPv6 prefix from the ISP. This prefix can change periodically (e.g., after router reboots or lease expiry). When running Kubernetes services that need stable IPv6 addresses (like LoadBalancers), you need a way to:

1. Detect when your prefix changes
2. Update your Kubernetes IP pools automatically
3. Ensure the addresses you use don't conflict with other devices

## How IPv6 Prefix Delegation Works

Understanding the typical home/SOHO network helps clarify the options:

```
ISP
 │
 │ Delegates /56 via DHCPv6-PD (e.g., 2001:db8:abcd::/56)
 ▼
Your Router (UniFi, OpenWRT, etc.)
 │
 │ Assigns /64 per VLAN via Router Advertisement
 │ (e.g., 2001:db8:abcd:01::/64 for VLAN 1)
 ▼
Your Network/VLAN
 │
 ├── Device A (gets 2001:db8:abcd:01::<random>/64 via SLAAC)
 ├── Device B (gets 2001:db8:abcd:01::<random>/64 via SLAAC)
 └── K8s Nodes (get 2001:db8:abcd:01::<random>/64 via SLAAC)
```

**Key insight**: Your Kubernetes nodes typically only see the `/64` that the router advertises to their VLAN, not the full `/56` that the ISP delegated.

---

## Mode 1: Address Range (Recommended)

**Use a reserved range within your existing /64**

This is the simplest approach and works for most home and small office setups.

### How it works

```
Router advertises:     2001:db8:abcd:01::/64
SLAAC/DHCPv6 uses:     2001:db8:abcd:01:0:* through 2001:db8:abcd:01:efff:*
Operator reserves:     2001:db8:abcd:01:f000:* through 2001:db8:abcd:01:ffff:*
                       └── 4096 addresses for LoadBalancers
```

The operator observes the /64 via Router Advertisements and allocates addresses from a range that your router is configured to leave unused.

### Requirements

1. **Configure your router** to exclude a range from DHCPv6/SLAAC
   - UniFi: Network → IPv6 → DHCPv6 Range (leave out the high range)
   - OpenWRT: Set DHCPv6 pool to exclude your reserved range

2. **Tell the operator** which range to use (must match router config)

### Example Configuration

```yaml
apiVersion: dynamic-prefix.io/v1alpha1
kind: DynamicPrefix
metadata:
  name: home-prefix
spec:
  acquisition:
    routerAdvertisement:
      interface: eth0
      enabled: true

  addressRanges:
    - name: loadbalancers
      # Reserve the last 4096 addresses of the /64
      startOffset: "::f000:0:0:0"
      endOffset: "::ffff:ffff:ffff:ffff"
```

### Pros

- **Simple setup** - no BGP or advanced routing required
- **Works immediately** - the /64 is already routed to your VLAN
- **Automatic updates** - when ISP prefix changes, operator detects new /64 and updates pools
- **Compatible with any router** - just needs DHCPv6 range configuration

### Cons

- **Requires router coordination** - must configure router to not hand out addresses in your range
- **Shared address space** - your K8s services share the /64 with other devices (though in separate ranges)
- **Theoretical SLAAC collision** - privacy addresses are random, but collision is extremely unlikely with proper range separation

### Who should use this

- Home labs and small office Kubernetes clusters
- Users who want simple, working IPv6 LoadBalancers
- Setups where the router handles DHCPv6-PD (most common)

---

## Mode 2: Dedicated Subnet (Advanced)

**Claim an unused /64 from your ISP-delegated prefix**

This approach gives you a completely separate /64 for Kubernetes, but requires BGP routing.

### How it works

```
ISP delegates to router:   2001:db8:abcd::/56
Router uses for VLANs:     2001:db8:abcd:00::/64 (VLAN 1)
                           2001:db8:abcd:01::/64 (VLAN 2)
                           ...
Operator claims:           2001:db8:abcd:ff::/64 (unused, high range)
                           └── Entire /64 for K8s (18 quintillion addresses)
```

Since routers typically assign /64s sequentially to VLANs, picking a high subnet index (like 255) avoids collision unless you have 250+ VLANs.

### Requirements

1. **BGP peering** between Cilium and your router
2. **Router configuration** to accept routes from your K8s nodes
3. **Operator manages Cilium BGP** - updates route announcements when prefix changes

### The Dynamic Prefix Challenge

When your ISP prefix changes, the announced route must also change:

```
Day 1: Announce 2001:db8:abcd:ff::/64
Day 2: ISP changes prefix
       Announce 2001:db8:9999:ff::/64
```

The operator handles the Cilium side automatically, but your router must be configured to accept route updates. For home routers, this typically means:

- Accept any IPv6 route from your K8s node IPs
- Or accept any /64 within GUA range (2000::/3)

### Example Configuration

```yaml
apiVersion: dynamic-prefix.io/v1alpha1
kind: DynamicPrefix
metadata:
  name: home-prefix
spec:
  acquisition:
    routerAdvertisement:
      interface: eth0
      enabled: true

  subnets:
    - name: k8s-services
      # Pick the 256th /64 from the parent prefix
      subnetIndex: 255
      prefixLength: 64
      advertiseBGP: true
```

### Pros

- **Clean separation** - dedicated /64 for K8s, no sharing with other devices
- **No SLAAC collision risk** - completely separate address space
- **More addresses** - full /64 instead of a range

### Cons

- **Requires BGP** - more complex setup
- **Router must accept dynamic routes** - security consideration
- **Not all routers support BGP** - UniFi does, many consumer routers don't
- **More things to break** - BGP peering, route filters, etc.

### Who should use this

- Advanced users comfortable with BGP
- Environments requiring strict address separation
- Larger deployments needing more addresses
- Users with BGP-capable routers (UniFi, MikroTik, OpenWRT with BIRD)

---

## Quick Decision Guide

```
Do you need IPv6 LoadBalancer IPs?
│
├─ Yes
│   │
│   └─ Are you comfortable with BGP?
│       │
│       ├─ No  → Use Mode 1 (Address Range)
│       │        Just configure your router's DHCPv6 range
│       │
│       └─ Yes → Do you need strict address separation?
│                │
│                ├─ No  → Use Mode 1 (simpler)
│                └─ Yes → Use Mode 2 (Dedicated Subnet)
│
└─ No → You might not need this operator
```

**When in doubt, start with Mode 1.** It's simpler, works with any router, and covers most home/SOHO use cases. You can always migrate to Mode 2 later if needed.

---

## Router Configuration Examples

### UniFi (Mode 1)

1. Go to **Network** → **Settings** → **Internet**
2. Under **IPv6**, find DHCPv6 settings
3. Set the DHCPv6 range to exclude your reserved range:
   - Start: `::1`
   - End: `::efff:ffff:ffff:ffff`
   - This leaves `::f000:0:0:0` through `::ffff:ffff:ffff:ffff` for K8s

### OpenWRT (Mode 1)

In `/etc/config/dhcp`:
```
config dhcp 'lan'
    option dhcpv6 'server'
    option ra 'server'
    list dns '2001:4860:4860::8888'
    # Limit the pool to avoid your reserved range
    option pool_start '::1000'
    option pool_end '::efff:ffff:ffff:ffff'
```

---

## Troubleshooting

### "Operator isn't detecting my prefix"

- Ensure the operator pod has access to the network interface
- For hostNetwork mode, verify the interface name is correct
- Check that Router Advertisements are being sent (use `rdisc6` or `tcpdump`)

### "Addresses conflict with my devices"

- Verify your router's DHCPv6 range excludes your operator's range
- Check that no static IPs are assigned in the reserved range
- Ensure the startOffset/endOffset don't overlap with SLAAC range

### "BGP routes aren't working" (Mode 2)

- Verify Cilium BGP peering is established
- Check router accepts routes from K8s node IPs
- Confirm the announced prefix is correct after ISP prefix change

---

## Further Reading

- [IPv6 Prefix Delegation (RFC 8415)](https://datatracker.ietf.org/doc/html/rfc8415)
- [Cilium BGP Documentation](https://docs.cilium.io/en/stable/network/bgp/)
- [SLAAC (RFC 4862)](https://datatracker.ietf.org/doc/html/rfc4862)
