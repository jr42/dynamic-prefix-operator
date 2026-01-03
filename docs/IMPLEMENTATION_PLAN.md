# Implementation Plan: Dynamic Prefix Operator

## Executive Summary

This document outlines the implementation plan for the Dynamic Prefix Operator, a Kubernetes operator that manages dynamic IPv6 prefix delegation for bare-metal and home/SOHO clusters. The operator will be built using Go with kubebuilder, following Kubernetes operator best practices.

## Technology Stack

### Core Framework

| Component | Technology | Justification |
|-----------|------------|---------------|
| Language | Go 1.22+ | Standard for Kubernetes operators, excellent concurrency |
| Operator Framework | [Kubebuilder](https://kubebuilder.io/) | Industry standard, generates boilerplate, CRD scaffolding |
| Controller Runtime | [controller-runtime](https://pkg.go.dev/sigs.k8s.io/controller-runtime) | Kubernetes SIG project, powers kubebuilder and Operator SDK |
| Kubernetes Client | client-go | Official Kubernetes Go client |

### Networking Libraries

| Component | Library | Justification |
|-----------|---------|---------------|
| DHCPv6-PD | [insomniacslk/dhcp](https://github.com/insomniacslk/dhcp) | Most maintained DHCPv6 library in Go (261+ imports), full PD support, BSD-3 license |
| NDP/RA | [mdlayher/ndp](https://github.com/mdlayher/ndp) | Production-proven (MetalLB, DigitalOcean), MIT license |

### Observability

| Component | Technology | Justification |
|-----------|------------|---------------|
| Metrics | Prometheus (controller-runtime native) | Standard for Kubernetes operators |
| Logging | zap (controller-runtime native) | Structured logging, JSON output |
| Tracing | OpenTelemetry (optional) | Distributed tracing for debugging |

## Project Structure

Following [Go project layout best practices](https://github.com/golang-standards/project-layout) and [kubebuilder conventions](https://kubebuilder.io/reference/good-practices):

```
dynamic-prefix-operator/
├── api/
│   └── v1alpha1/
│       ├── dynamicprefix_types.go      # DynamicPrefix CRD
│       ├── dynamicprefixbinding_types.go # DynamicPrefixBinding CRD
│       ├── groupversion_info.go
│       ├── zz_generated.deepcopy.go
│       └── webhook_validation.go       # CEL validation webhooks
│
├── cmd/
│   └── manager/
│       └── main.go                     # Operator entrypoint
│
├── internal/
│   ├── controller/
│   │   ├── dynamicprefix_controller.go     # Main reconciler
│   │   ├── dynamicprefixbinding_controller.go
│   │   ├── suite_test.go                   # envtest setup
│   │   └── dynamicprefix_controller_test.go
│   │
│   ├── prefix/
│   │   ├── acquirer.go           # Interface for prefix acquisition
│   │   ├── dhcpv6/
│   │   │   ├── client.go         # DHCPv6-PD client implementation
│   │   │   ├── lease.go          # Lease management
│   │   │   └── client_test.go
│   │   ├── ra/
│   │   │   ├── monitor.go        # Router Advertisement monitor
│   │   │   └── monitor_test.go
│   │   └── store.go              # Prefix state management
│   │
│   ├── backend/
│   │   ├── interface.go          # Backend plugin interface
│   │   ├── cilium/
│   │   │   ├── lbipam.go         # CiliumLoadBalancerIPPool updater
│   │   │   ├── cidrgroup.go      # CiliumCIDRGroup updater
│   │   │   └── cilium_test.go
│   │   ├── calico/               # Future: Calico backend
│   │   │   └── ippool.go
│   │   └── metallb/              # Future: MetalLB backend
│   │       └── addresspool.go
│   │
│   └── transition/
│       ├── manager.go            # Graceful transition logic
│       ├── drain.go              # Drain period management
│       └── manager_test.go
│
├── config/
│   ├── crd/
│   │   └── bases/                # Generated CRD YAML
│   ├── rbac/
│   │   ├── role.yaml
│   │   └── role_binding.yaml
│   ├── manager/
│   │   ├── manager.yaml          # Deployment manifest
│   │   └── kustomization.yaml
│   ├── samples/
│   │   ├── dynamicprefix.yaml
│   │   └── dynamicprefixbinding.yaml
│   └── default/
│       └── kustomization.yaml
│
├── charts/
│   └── dynamic-prefix-operator/  # Helm chart
│       ├── Chart.yaml
│       ├── values.yaml
│       └── templates/
│
├── hack/
│   ├── boilerplate.go.txt
│   └── tools.go                  # Tool dependencies
│
├── docs/
│   ├── IMPLEMENTATION_PLAN.md    # This document
│   ├── ARCHITECTURE.md
│   └── user-guide/
│
├── Makefile                      # Build automation
├── Dockerfile                    # Multi-stage container build
├── go.mod
├── go.sum
├── PROJECT                       # Kubebuilder project file
└── README.md
```

## Implementation Phases

### Phase 1: Project Scaffolding & Core CRDs (Week 1)

**Objective**: Set up the project structure and define the core CRDs.

#### Tasks

1. **Initialize kubebuilder project**
   ```bash
   kubebuilder init --domain reith.cloud --repo github.com/jr42/dynamic-prefix-operator
   ```

2. **Create CRD scaffolds**
   ```bash
   kubebuilder create api --group network --version v1alpha1 --kind DynamicPrefix --resource --controller
   kubebuilder create api --group network --version v1alpha1 --kind DynamicPrefixBinding --resource --controller
   ```

3. **Implement CRD types** (see [CRD Specifications](#crd-specifications) below)

4. **Add CEL validation rules** for CRDs

5. **Generate manifests**
   ```bash
   make manifests
   ```

#### Deliverables
- [ ] Compilable project structure
- [ ] CRD definitions with OpenAPI validation
- [ ] Basic controller stubs
- [ ] CI pipeline (GitHub Actions)

### Phase 2: DHCPv6-PD Client (Week 2)

**Objective**: Implement the DHCPv6 Prefix Delegation client.

#### Architecture

```go
// internal/prefix/acquirer.go
type Acquirer interface {
    // Acquire attempts to obtain a prefix
    Acquire(ctx context.Context) (*Prefix, error)

    // Watch returns a channel that emits prefix changes
    Watch(ctx context.Context) (<-chan PrefixEvent, error)

    // Release releases the current prefix
    Release(ctx context.Context) error

    // CurrentPrefix returns the currently held prefix
    CurrentPrefix() *Prefix
}

type Prefix struct {
    Network      netip.Prefix
    ValidLifetime   time.Duration
    PreferredLifetime time.Duration
    Source       PrefixSource
    AcquiredAt   time.Time
}

type PrefixSource string
const (
    PrefixSourceDHCPv6PD PrefixSource = "dhcpv6-pd"
    PrefixSourceRA       PrefixSource = "router-advertisement"
    PrefixSourceStatic   PrefixSource = "static"
)
```

#### DHCPv6-PD Client Implementation

```go
// internal/prefix/dhcpv6/client.go
type Client struct {
    interface_  string
    duid        dhcpv6.DUID
    requestedLen uint8

    conn        *dhcpv6.Client
    currentLease *Lease

    events      chan PrefixEvent
    mu          sync.RWMutex
}

func (c *Client) Acquire(ctx context.Context) (*Prefix, error) {
    // 1. Build SOLICIT message with IA_PD option
    solicit, err := dhcpv6.NewSolicit(c.interface_,
        dhcpv6.WithIAPD(
            dhcpv6.GenerateIAID(c.interface_),
            &dhcpv6.OptIAPrefix{
                PreferredLifetime: 3600,
                ValidLifetime:     7200,
                Prefix: &net.IPNet{
                    Mask: net.CIDRMask(c.requestedLen, 128),
                },
            },
        ),
    )

    // 2. Send SOLICIT, receive ADVERTISE
    advertise, err := c.conn.SendAndRead(ctx, solicit, nil)

    // 3. Build REQUEST based on ADVERTISE
    request, err := dhcpv6.NewRequestFromAdvertise(advertise)

    // 4. Send REQUEST, receive REPLY
    reply, err := c.conn.SendAndRead(ctx, request, nil)

    // 5. Extract IA_PD from REPLY
    iapd := reply.GetOneOption(dhcpv6.OptionIAPD)

    // 6. Store lease and start renewal timer
    c.storeLease(iapd)

    return c.currentPrefix(), nil
}

func (c *Client) runLeaseManager(ctx context.Context) {
    // Handles T1 (renew) and T2 (rebind) timers
    // Sends RENEW before T1 expires
    // Sends REBIND if RENEW fails before T2
    // Emits PrefixExpired event if all renewal fails
}
```

#### Tasks

1. **Implement DHCPv6-PD client** using `insomniacslk/dhcp`
   - SOLICIT → ADVERTISE → REQUEST → REPLY flow
   - IA_PD option handling
   - DUID generation and persistence

2. **Implement lease management**
   - T1/T2 timer handling
   - RENEW and REBIND flows
   - Lease persistence across restarts

3. **Add integration tests**
   - Mock DHCPv6 server for testing
   - Test prefix renewal flow
   - Test rebind on server failure

#### Deliverables
- [ ] Working DHCPv6-PD client
- [ ] Lease management with T1/T2 timers
- [ ] Unit and integration tests
- [ ] Metrics for lease state

### Phase 3: Router Advertisement Monitor (Week 2-3)

**Objective**: Implement RA monitoring as fallback/supplementary prefix detection.

#### Architecture

```go
// internal/prefix/ra/monitor.go
type Monitor struct {
    interface_ string
    conn       *ndp.Conn
    events     chan PrefixEvent
}

func (m *Monitor) Watch(ctx context.Context) (<-chan PrefixEvent, error) {
    go func() {
        for {
            msg, _, _, err := m.conn.ReadFrom()
            if err != nil {
                continue
            }

            ra, ok := msg.(*ndp.RouterAdvertisement)
            if !ok {
                continue
            }

            for _, opt := range ra.Options {
                if pi, ok := opt.(*ndp.PrefixInformation); ok {
                    if pi.OnLink && pi.AutonomousAddressConfiguration {
                        m.events <- PrefixEvent{
                            Type:   PrefixDetected,
                            Prefix: pi.Prefix,
                            ValidLifetime: pi.ValidLifetime,
                        }
                    }
                }
            }
        }
    }()

    return m.events, nil
}
```

#### Tasks

1. **Implement RA monitor** using `mdlayher/ndp`
   - Listen for Router Advertisements
   - Parse Prefix Information options
   - Track prefix validity

2. **Implement prefix reconciliation**
   - Compare DHCPv6-PD prefix with RA prefix
   - Log discrepancies
   - Fallback to RA if DHCPv6-PD fails

3. **Add tests**
   - Mock RA packets
   - Test prefix detection

#### Deliverables
- [ ] Working RA monitor
- [ ] Prefix source reconciliation
- [ ] Tests with mock RAs

### Phase 4: DynamicPrefix Controller (Week 3)

**Objective**: Implement the main controller that reconciles DynamicPrefix resources.

#### Reconciliation Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                   DynamicPrefix Reconciler                       │
│                                                                 │
│  1. Fetch DynamicPrefix CR                                      │
│  2. Ensure Acquirer is running for interface                    │
│  3. Get current prefix from Acquirer                            │
│  4. Calculate subnets from prefix                               │
│  5. Update status.currentPrefix and status.subnets              │
│  6. If prefix changed:                                          │
│     a. Add old prefix to history                                │
│     b. Trigger bindings reconciliation                          │
│     c. Start drain timer for old prefix                         │
│  7. Update conditions                                           │
│  8. Requeue based on lease expiry                               │
└─────────────────────────────────────────────────────────────────┘
```

#### Controller Implementation

```go
// internal/controller/dynamicprefix_controller.go
func (r *DynamicPrefixReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    log := log.FromContext(ctx)

    // 1. Fetch the DynamicPrefix
    var dp networkv1alpha1.DynamicPrefix
    if err := r.Get(ctx, req.NamespacedName, &dp); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // 2. Handle deletion with finalizer
    if !dp.DeletionTimestamp.IsZero() {
        return r.handleDeletion(ctx, &dp)
    }

    // 3. Ensure finalizer is set
    if !controllerutil.ContainsFinalizer(&dp, finalizerName) {
        controllerutil.AddFinalizer(&dp, finalizerName)
        if err := r.Update(ctx, &dp); err != nil {
            return ctrl.Result{}, err
        }
    }

    // 4. Get or create acquirer for this interface
    acquirer, err := r.acquirerManager.GetOrCreate(dp.Spec.Acquisition)
    if err != nil {
        return r.setCondition(ctx, &dp, ConditionPrefixAcquired, false, err.Error())
    }

    // 5. Get current prefix
    prefix := acquirer.CurrentPrefix()
    if prefix == nil {
        // Attempt acquisition
        prefix, err = acquirer.Acquire(ctx)
        if err != nil {
            return r.setCondition(ctx, &dp, ConditionPrefixAcquired, false, err.Error())
        }
    }

    // 6. Calculate subnets
    subnets, err := r.calculateSubnets(prefix.Network, dp.Spec.Subnets)
    if err != nil {
        return ctrl.Result{}, err
    }

    // 7. Check if prefix changed
    if dp.Status.CurrentPrefix != prefix.Network.String() {
        r.handlePrefixChange(ctx, &dp, prefix)
    }

    // 8. Update status
    dp.Status.CurrentPrefix = prefix.Network.String()
    dp.Status.Subnets = subnets
    dp.Status.LeaseExpiresAt = &metav1.Time{Time: prefix.AcquiredAt.Add(prefix.ValidLifetime)}

    if err := r.Status().Update(ctx, &dp); err != nil {
        return ctrl.Result{}, err
    }

    // 9. Requeue before lease expires
    requeueAfter := time.Until(prefix.AcquiredAt.Add(prefix.ValidLifetime / 2))
    return ctrl.Result{RequeueAfter: requeueAfter}, nil
}
```

#### Tasks

1. **Implement DynamicPrefix controller**
   - Reconciliation loop
   - Acquirer lifecycle management
   - Subnet calculation

2. **Implement prefix change handling**
   - Detect prefix changes
   - Update history
   - Emit events

3. **Add finalizer for cleanup**
   - Release DHCPv6 lease on deletion
   - Clean up acquirer resources

4. **Add controller tests**
   - envtest-based tests
   - Test reconciliation flow
   - Test error handling

#### Deliverables
- [ ] Working DynamicPrefix controller
- [ ] Prefix change detection
- [ ] Finalizer cleanup
- [ ] Controller tests

### Phase 5: Cilium Backend (Week 4)

**Objective**: Implement the Cilium backend for updating LB-IPAM pools and CIDRGroups.

#### Backend Interface

```go
// internal/backend/interface.go
type Backend interface {
    // Name returns the backend identifier
    Name() string

    // Supports checks if this backend can handle the target
    Supports(target TargetRef) bool

    // Update applies the new prefix to the target
    Update(ctx context.Context, target TargetRef, prefix netip.Prefix, strategy UpdateStrategy) error

    // Validate checks if the target exists and is accessible
    Validate(ctx context.Context, target TargetRef) error
}

type UpdateStrategy string
const (
    UpdateStrategyReplace UpdateStrategy = "Replace"
    UpdateStrategyAppend  UpdateStrategy = "Append"
)
```

#### Cilium LB-IPAM Implementation

```go
// internal/backend/cilium/lbipam.go
type LBIPAMBackend struct {
    client client.Client
}

func (b *LBIPAMBackend) Update(ctx context.Context, target TargetRef, prefix netip.Prefix, strategy UpdateStrategy) error {
    var pool ciliumv2alpha1.CiliumLoadBalancerIPPool
    if err := b.client.Get(ctx, client.ObjectKey{
        Name:      target.Name,
        Namespace: target.Namespace,
    }, &pool); err != nil {
        return err
    }

    switch strategy {
    case UpdateStrategyReplace:
        pool.Spec.Blocks = []ciliumv2alpha1.CiliumLoadBalancerIPPoolIPBlock{
            {Cidr: ciliumv2alpha1.IPv6CIDR(prefix.String())},
        }
    case UpdateStrategyAppend:
        pool.Spec.Blocks = append(pool.Spec.Blocks,
            ciliumv2alpha1.CiliumLoadBalancerIPPoolIPBlock{
                Cidr: ciliumv2alpha1.IPv6CIDR(prefix.String()),
            },
        )
    }

    return b.client.Update(ctx, &pool)
}
```

#### Cilium CIDRGroup Implementation

```go
// internal/backend/cilium/cidrgroup.go
type CIDRGroupBackend struct {
    client client.Client
}

func (b *CIDRGroupBackend) Update(ctx context.Context, target TargetRef, prefix netip.Prefix, strategy UpdateStrategy) error {
    var group ciliumv2.CiliumCIDRGroup
    if err := b.client.Get(ctx, client.ObjectKey{Name: target.Name}, &group); err != nil {
        return err
    }

    switch strategy {
    case UpdateStrategyReplace:
        group.Spec.ExternalCIDRs = []ciliumv2.ExternalCIDR{
            ciliumv2.ExternalCIDR(prefix.String()),
        }
    case UpdateStrategyAppend:
        group.Spec.ExternalCIDRs = append(group.Spec.ExternalCIDRs,
            ciliumv2.ExternalCIDR(prefix.String()),
        )
    }

    return b.client.Update(ctx, &group)
}
```

#### Tasks

1. **Implement Cilium LB-IPAM backend**
   - Update CiliumLoadBalancerIPPool
   - Handle Replace and Append strategies
   - Validate pool existence

2. **Implement Cilium CIDRGroup backend**
   - Update CiliumCIDRGroup
   - Handle external CIDR updates

3. **Implement DynamicPrefixBinding controller**
   - Watch DynamicPrefix changes
   - Trigger backend updates
   - Update binding status

4. **Add integration tests**
   - Test with Cilium CRDs
   - Test update strategies

#### Deliverables
- [ ] Working Cilium LB-IPAM backend
- [ ] Working Cilium CIDRGroup backend
- [ ] DynamicPrefixBinding controller
- [ ] Integration tests

### Phase 6: Graceful Transitions (Week 5)

**Objective**: Implement graceful prefix transition to minimize service disruption.

#### Transition Flow

```
Prefix Change Detected
        │
        ▼
┌─────────────────────────────────────┐
│ Add new prefix to pools (Append)    │
│ Keep old prefix active              │
└─────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────┐
│ Wait for DNS propagation            │
│ (configurable drain period)         │
└─────────────────────────────────────┘
        │
        ▼
┌─────────────────────────────────────┐
│ Remove old prefix from pools        │
│ Update history                      │
└─────────────────────────────────────┘
```

#### Implementation

```go
// internal/transition/manager.go
type Manager struct {
    client         client.Client
    drainPeriod    time.Duration
    maxHistory     int
    pendingDrains  map[string]*DrainTask
    mu             sync.Mutex
}

type DrainTask struct {
    OldPrefix    netip.Prefix
    NewPrefix    netip.Prefix
    StartedAt    time.Time
    DrainUntil   time.Time
    Bindings     []client.ObjectKey
    Status       DrainStatus
}

func (m *Manager) StartTransition(ctx context.Context, dp *networkv1alpha1.DynamicPrefix, oldPrefix, newPrefix netip.Prefix) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    // 1. Get all bindings for this DynamicPrefix
    bindings, err := m.getBindings(ctx, dp)
    if err != nil {
        return err
    }

    // 2. For each binding, add new prefix (Append strategy)
    for _, binding := range bindings {
        backend := m.backendManager.Get(binding.Spec.Target.Kind)
        if err := backend.Update(ctx, binding.Spec.Target, newPrefix, UpdateStrategyAppend); err != nil {
            return err
        }
    }

    // 3. Schedule drain task
    task := &DrainTask{
        OldPrefix:  oldPrefix,
        NewPrefix:  newPrefix,
        StartedAt:  time.Now(),
        DrainUntil: time.Now().Add(m.drainPeriod),
        Bindings:   bindingKeys,
        Status:     DrainStatusPending,
    }
    m.pendingDrains[dp.Name] = task

    // 4. Schedule drain completion
    go m.completeDrain(ctx, dp.Name, task)

    return nil
}

func (m *Manager) completeDrain(ctx context.Context, dpName string, task *DrainTask) {
    timer := time.NewTimer(time.Until(task.DrainUntil))
    defer timer.Stop()

    select {
    case <-timer.C:
        // Drain period elapsed, remove old prefix
        for _, bindingKey := range task.Bindings {
            // Remove old prefix from pool
        }
    case <-ctx.Done():
        return
    }
}
```

#### Tasks

1. **Implement transition manager**
   - Track pending transitions
   - Manage drain timers
   - Handle concurrent transitions

2. **Implement drain completion**
   - Remove old prefix after drain period
   - Update DynamicPrefix history
   - Emit completion events

3. **Add cancelation support**
   - Cancel drain if new prefix arrives during drain
   - Handle rapid prefix changes

4. **Add tests**
   - Test transition flow
   - Test cancelation
   - Test concurrent transitions

#### Deliverables
- [ ] Working transition manager
- [ ] Drain period support
- [ ] Transition cancelation
- [ ] Tests

### Phase 7: Observability & Production Hardening (Week 6)

**Objective**: Add metrics, events, and production-grade error handling.

#### Metrics

```go
// Metrics to expose
var (
    prefixAcquisitions = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "dynamic_prefix_acquisitions_total",
            Help: "Total number of prefix acquisitions",
        },
        []string{"interface", "source", "status"},
    )

    prefixChanges = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Name: "dynamic_prefix_changes_total",
            Help: "Total number of prefix changes detected",
        },
        []string{"name"},
    )

    leaseExpirySeconds = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "dynamic_prefix_lease_expiry_seconds",
            Help: "Seconds until current lease expires",
        },
        []string{"name"},
    )

    bindingSyncDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "dynamic_prefix_binding_sync_duration_seconds",
            Help:    "Duration of binding sync operations",
            Buckets: prometheus.DefBuckets,
        },
        []string{"binding", "backend"},
    )

    drainPeriodRemaining = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "dynamic_prefix_drain_period_remaining_seconds",
            Help: "Seconds remaining in current drain period",
        },
        []string{"name"},
    )
)
```

#### Events

```go
// Events to emit
const (
    EventPrefixAcquired     = "PrefixAcquired"
    EventPrefixChanged      = "PrefixChanged"
    EventPrefixRenewed      = "PrefixRenewed"
    EventPrefixExpired      = "PrefixExpired"
    EventBindingSynced      = "BindingSynced"
    EventBindingSyncFailed  = "BindingSyncFailed"
    EventDrainStarted       = "DrainStarted"
    EventDrainCompleted     = "DrainCompleted"
)
```

#### Tasks

1. **Add Prometheus metrics**
   - Prefix acquisition counters
   - Lease expiry gauges
   - Sync duration histograms

2. **Add Kubernetes events**
   - Emit events for all major state changes
   - Include relevant details in event messages

3. **Add health endpoints**
   - /healthz for liveness
   - /readyz for readiness
   - Leader election health

4. **Production hardening**
   - Rate limiting on API calls
   - Exponential backoff on failures
   - Circuit breakers for external dependencies

5. **Documentation**
   - Runbook for common issues
   - Metrics reference
   - Troubleshooting guide

#### Deliverables
- [ ] Prometheus metrics
- [ ] Kubernetes events
- [ ] Health endpoints
- [ ] Runbooks and documentation

### Phase 8: Deployment & Release (Week 7)

**Objective**: Create deployment artifacts and release automation.

#### Deployment Methods

1. **Helm Chart**
   - Configurable values
   - RBAC resources
   - ServiceMonitor for Prometheus

2. **Kustomize Bases**
   - Default configuration
   - HA configuration
   - Development configuration

3. **Plain YAML**
   - Single-file installation
   - Minimal dependencies

#### Tasks

1. **Create Helm chart**
   - Chart.yaml, values.yaml
   - Templates for all resources
   - Configurable resource limits

2. **Create Kustomize configurations**
   - Base configuration
   - Overlays for environments

3. **Create release automation**
   - GitHub Actions workflow
   - Semantic versioning
   - Container image publishing
   - Helm chart publishing

4. **Create installation documentation**
   - Quick start guide
   - Configuration reference
   - Upgrade procedures

#### Deliverables
- [ ] Helm chart
- [ ] Kustomize configurations
- [ ] Release automation
- [ ] Installation documentation

## CRD Specifications

### DynamicPrefix

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: dynamicprefixes.network.reith.cloud
spec:
  group: network.reith.cloud
  names:
    kind: DynamicPrefix
    listKind: DynamicPrefixList
    plural: dynamicprefixes
    singular: dynamicprefix
    shortNames:
      - dp
      - dprefix
  scope: Cluster  # Cluster-scoped, prefixes are global
  versions:
    - name: v1alpha1
      served: true
      storage: true
      subresources:
        status: {}
      additionalPrinterColumns:
        - name: Prefix
          type: string
          jsonPath: .status.currentPrefix
        - name: Source
          type: string
          jsonPath: .status.prefixSource
        - name: Expires
          type: date
          jsonPath: .status.leaseExpiresAt
        - name: Age
          type: date
          jsonPath: .metadata.creationTimestamp
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              required:
                - acquisition
              properties:
                acquisition:
                  type: object
                  properties:
                    dhcpv6:
                      type: object
                      required:
                        - interface
                      properties:
                        interface:
                          type: string
                          description: Network interface for DHCPv6-PD
                        requestedPrefixLength:
                          type: integer
                          minimum: 48
                          maximum: 64
                          default: 60
                          description: Requested prefix length
                        duid:
                          type: string
                          default: auto
                          description: DUID for DHCPv6 identification
                    routerAdvertisement:
                      type: object
                      properties:
                        interface:
                          type: string
                          description: Interface to monitor for RAs
                        enabled:
                          type: boolean
                          default: true
                subnets:
                  type: array
                  items:
                    type: object
                    required:
                      - name
                      - prefixLength
                    properties:
                      name:
                        type: string
                        description: Subnet identifier
                      offset:
                        type: integer
                        default: 0
                        description: Offset within acquired prefix
                      prefixLength:
                        type: integer
                        minimum: 64
                        maximum: 128
                        description: Subnet prefix length
                transition:
                  type: object
                  properties:
                    drainPeriodMinutes:
                      type: integer
                      default: 60
                      minimum: 0
                      maximum: 1440
                    maxPrefixHistory:
                      type: integer
                      default: 2
                      minimum: 1
                      maximum: 10
            status:
              type: object
              properties:
                currentPrefix:
                  type: string
                prefixSource:
                  type: string
                  enum:
                    - dhcpv6-pd
                    - router-advertisement
                    - static
                leaseExpiresAt:
                  type: string
                  format: date-time
                subnets:
                  type: array
                  items:
                    type: object
                    properties:
                      name:
                        type: string
                      cidr:
                        type: string
                history:
                  type: array
                  items:
                    type: object
                    properties:
                      prefix:
                        type: string
                      acquiredAt:
                        type: string
                        format: date-time
                      deprecatedAt:
                        type: string
                        format: date-time
                      state:
                        type: string
                        enum:
                          - active
                          - draining
                          - expired
                conditions:
                  type: array
                  items:
                    type: object
                    properties:
                      type:
                        type: string
                      status:
                        type: string
                        enum:
                          - "True"
                          - "False"
                          - Unknown
                      lastTransitionTime:
                        type: string
                        format: date-time
                      reason:
                        type: string
                      message:
                        type: string
```

### DynamicPrefixBinding

```yaml
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: dynamicprefixbindings.network.reith.cloud
spec:
  group: network.reith.cloud
  names:
    kind: DynamicPrefixBinding
    listKind: DynamicPrefixBindingList
    plural: dynamicprefixbindings
    singular: dynamicprefixbinding
    shortNames:
      - dpb
      - dpbinding
  scope: Cluster
  versions:
    - name: v1alpha1
      served: true
      storage: true
      subresources:
        status: {}
      additionalPrinterColumns:
        - name: Prefix
          type: string
          jsonPath: .status.boundPrefix
        - name: Target
          type: string
          jsonPath: .spec.target.kind
        - name: Synced
          type: boolean
          jsonPath: .status.targetSynced
        - name: Age
          type: date
          jsonPath: .metadata.creationTimestamp
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              required:
                - prefixRef
                - target
              properties:
                prefixRef:
                  type: object
                  required:
                    - name
                  properties:
                    name:
                      type: string
                      description: Name of the DynamicPrefix
                    subnet:
                      type: string
                      description: Subnet name from DynamicPrefix
                target:
                  type: object
                  required:
                    - kind
                    - name
                  properties:
                    kind:
                      type: string
                      enum:
                        - CiliumLoadBalancerIPPool
                        - CiliumCIDRGroup
                        - IPPool  # Future: Calico
                        - IPAddressPool  # Future: MetalLB
                    name:
                      type: string
                    namespace:
                      type: string
                updateStrategy:
                  type: object
                  properties:
                    type:
                      type: string
                      enum:
                        - Replace
                        - Append
                      default: Replace
            status:
              type: object
              properties:
                boundPrefix:
                  type: string
                targetSynced:
                  type: boolean
                lastSyncTime:
                  type: string
                  format: date-time
                lastSyncError:
                  type: string
```

## Testing Strategy

### Unit Tests
- Individual function testing
- Mock interfaces for dependencies
- Table-driven tests

### Integration Tests
- envtest for Kubernetes API testing
- Mock DHCPv6 server
- Mock RA sender
- Cilium CRD fixtures

### End-to-End Tests
- Kind cluster with Cilium
- Real DHCPv6-PD (containerized)
- Prefix change simulation
- Full reconciliation flow

### Performance Tests
- Reconciliation latency
- Memory usage under load
- Lease renewal timing accuracy

## Security Considerations

### RBAC

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: dynamic-prefix-operator
rules:
  # Own CRDs
  - apiGroups: ["network.reith.cloud"]
    resources: ["dynamicprefixes", "dynamicprefixbindings"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["network.reith.cloud"]
    resources: ["dynamicprefixes/status", "dynamicprefixbindings/status"]
    verbs: ["get", "update", "patch"]

  # Cilium resources
  - apiGroups: ["cilium.io"]
    resources: ["ciliumloadbalancerippools", "ciliumcidrgroups"]
    verbs: ["get", "list", "watch", "update", "patch"]

  # Events
  - apiGroups: [""]
    resources: ["events"]
    verbs: ["create", "patch"]

  # Leader election
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

### Network Capabilities

The operator requires:
- `CAP_NET_RAW` for DHCPv6 and NDP raw sockets
- Host network access for interface binding
- Typically runs as a DaemonSet on nodes with the monitored interface

### Security Best Practices

- Non-root user where possible
- Read-only root filesystem
- No privileged containers
- Minimal RBAC permissions
- Secret management for credentials (if any)

## Risk Assessment

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| DHCPv6-PD lease loss | Medium | High | RA fallback, aggressive renewal |
| Rapid prefix changes | Low | Medium | Transition queue, debouncing |
| Backend API rate limiting | Low | Low | Exponential backoff, caching |
| Cilium API changes | Medium | Medium | Pin Cilium versions, abstraction layer |
| Memory leak in watchers | Low | Medium | Resource limits, monitoring |

## Success Criteria

1. **Functional**
   - Successfully acquire prefix via DHCPv6-PD
   - Detect prefix changes within 60 seconds
   - Update Cilium pools without manual intervention
   - Graceful transitions with configurable drain period

2. **Performance**
   - Reconciliation latency < 5 seconds
   - Memory usage < 128Mi under normal operation
   - CPU usage < 100m cores

3. **Reliability**
   - Zero prefix loss due to operator bugs
   - Automatic recovery from transient failures
   - Leader election for HA deployment

4. **Observability**
   - All major events surfaced as Kubernetes events
   - Prometheus metrics for dashboards
   - Structured logs for debugging

## References

- [Kubebuilder Book - Good Practices](https://kubebuilder.io/reference/good-practices)
- [Controller Runtime Documentation](https://pkg.go.dev/sigs.k8s.io/controller-runtime)
- [insomniacslk/dhcp - DHCPv6 Library](https://github.com/insomniacslk/dhcp)
- [mdlayher/ndp - NDP Library](https://github.com/mdlayher/ndp)
- [Cilium LB-IPAM Documentation](https://docs.cilium.io/en/stable/network/lb-ipam/)
- [RFC 3633 - IPv6 Prefix Options for DHCPv6](https://datatracker.ietf.org/doc/html/rfc3633)
- [RFC 4861 - Neighbor Discovery for IPv6](https://datatracker.ietf.org/doc/html/rfc4861)
- [k6u - Similar Project (Rust)](https://github.com/0xC0ncord/k6u)
- [Kubernetes Operators in 2025](https://outerbyte.com/kubernetes-operators-2025-guide/)
