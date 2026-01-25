# Prefix Acquisition Modes

This guide explains how the Dynamic Prefix Operator works with your network setup.

## The Problem: Dynamic IPv6 Prefixes

Most home and small office internet connections receive a dynamic IPv6 prefix from the ISP. This prefix can change periodically (e.g., after router reboots or lease expiry). When running Kubernetes services that need stable IPv6 addresses (like LoadBalancers), you need a way to:

1. Detect when your prefix changes
2. Update your Kubernetes IP pools automatically
3. Ensure the addresses you use don't conflict with other devices

## How IPv6 Prefix Delegation Works

Understanding the typical home/SOHO network helps clarify the setup:

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

## Address Range Mode (Recommended)

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
      # Reserve the last portion of the /64
      start: "::f000:0:0:0"
      end: "::ffff:ffff:ffff:ffff"
```

### Advantages

- **Simple setup** - no BGP or advanced routing required
- **Works immediately** - the /64 is already routed to your VLAN
- **Automatic updates** - when ISP prefix changes, operator detects new /64 and updates pools
- **Compatible with any router** - just needs DHCPv6 range configuration

### Considerations

- **Requires router coordination** - must configure router to not hand out addresses in your range
- **Shared address space** - your K8s services share the /64 with other devices (though in separate ranges)

### Who should use this

- Home labs and small office Kubernetes clusters
- Users who want simple, working IPv6 LoadBalancers
- Setups where the router handles DHCPv6-PD (most common)

---

## Router Configuration Examples

### UniFi

1. Go to **Network** → **Settings** → **Internet**
2. Under **IPv6**, find DHCPv6 settings
3. Set the DHCPv6 range to exclude your reserved range:
   - Start: `::1`
   - End: `::efff:ffff:ffff:ffff`
   - This leaves `::f000:0:0:0` through `::ffff:ffff:ffff:ffff` for K8s

### OpenWRT

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
- Ensure the start/end don't overlap with SLAAC range

---

## Future: Subnet Mode with BGP

A future release will support carving dedicated /64 subnets from larger prefixes (e.g., /56 or /48) and announcing them via BGP. This requires Cilium BGP Control Plane and is currently in development.

---

## Further Reading

- [IPv6 Prefix Delegation (RFC 8415)](https://datatracker.ietf.org/doc/html/rfc8415)
- [SLAAC (RFC 4862)](https://datatracker.ietf.org/doc/html/rfc4862)
