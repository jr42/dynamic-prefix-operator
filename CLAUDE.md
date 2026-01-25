# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Dynamic Prefix Operator is a Kubernetes operator that manages dynamic IPv6 prefix delegation for bare-metal and home/SOHO clusters. It automatically updates Cilium pool resources (LoadBalancerIPPools, CIDRGroups) when ISP-delegated IPv6 prefixes change, detected via Router Advertisements.

## Build Commands

```bash
make build            # Build manager binary
make test             # Run unit tests with coverage
make lint             # Run golangci-lint
make lint-fix         # Run linter with auto-fixes
make generate         # Generate code (deepcopy, CRDs)
make manifests        # Generate CRDs and RBAC manifests
make run              # Run operator locally against configured cluster
make docker-build     # Build container image
make helm-lint        # Lint Helm chart
make test-e2e         # Run e2e tests with Kind cluster
```

## Architecture

### Core Components

1. **Prefix Receiver Interface** (`internal/prefix/types.go`): Abstraction for prefix acquisition with channel-based events for prefix changes. Currently implements Router Advertisement monitoring.

2. **DynamicPrefix CRD** (`api/v1alpha1/dynamicprefix_types.go`): Custom resource defining prefix acquisition settings, address range definitions, and transition configuration. Status tracks current prefix, calculated ranges, and conditions (PrefixAcquired, PoolsSynced, Degraded).

3. **DynamicPrefixReconciler** (`internal/controller/dynamicprefix_controller.go`): Main reconciliation loop that manages prefix receivers, updates status, and handles graceful transitions with finalizer-based cleanup.

4. **Address Range Calculation** (`internal/prefix/addressrange.go`): Combines prefix with start/end suffixes to calculate full address ranges within the /64.

5. **PoolSyncReconciler** (`internal/controller/poolsync_controller.go`): Syncs annotated Cilium pools with calculated address ranges. Supports multi-block mode for graceful transitions, keeping blocks for current prefix plus historical prefixes.

6. **ServiceSyncReconciler** (`internal/controller/servicesync_controller.go`): HA mode controller that manages LoadBalancer Services. Sets `lbipam.cilium.io/ips` for multi-IP assignment and `external-dns.alpha.kubernetes.io/target` for DNS targeting.

### Data Flow

```
ISP/Router → RA Receiver → DynamicPrefix CR (status.currentPrefix)
    → Pool Controller (watches annotated pools, builds multi-block configs)
    → CiliumLoadBalancerIPPool/CiliumCIDRGroup (specs updated with current + historical blocks)
    → Service Controller (HA mode: manages Service IPs and DNS targeting)
```

### Pool Integration

Uses annotation-based binding (inspired by 1Password Operator):
- `dynamic-prefix.io/name`: References the DynamicPrefix CR
- `dynamic-prefix.io/address-range`: Specifies which address range to use

The operator watches annotated Cilium resources and auto-updates their `spec.blocks` (with start/stop addresses) or `spec.externalCIDRs`.

### Transition Modes

- **Simple mode** (default): Pools contain multiple blocks for current + historical prefixes. Services keep old IPs until blocks are pruned.
- **HA mode**: ServiceSync controller manages multi-IP Services with DNS targeting for zero-downtime transitions.

## Testing

- **Unit tests**: Ginkgo/Gomega BDD framework in `*_test.go` files
- **Integration tests**: `internal/integration/` for ISP simulation scenarios
- **E2E tests**: `test/e2e/` using Kind clusters

Run a single test file:
```bash
go test -v ./internal/prefix/addressrange_test.go ./internal/prefix/addressrange.go ./internal/prefix/types.go
```

Run tests matching a pattern:
```bash
go test -v ./... -run TestAddressRange
```

## Key Technologies

- **Go 1.24.6** with controller-runtime v0.22.4 (Kubebuilder 4.10.1)
- **mdlayher/ndp**: Router Advertisement (NDP) monitoring
- **Helm 3** and **Kustomize** for deployment

## Code Patterns

- Kubebuilder reconciler pattern with controller-runtime
- Interface-based design for pluggable prefix acquisition methods
- Channel-based event system for asynchronous prefix changes
- Annotation-based binding instead of separate binding CRDs
