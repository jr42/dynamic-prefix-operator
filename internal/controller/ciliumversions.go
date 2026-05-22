/*
Copyright 2026 jr42.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	ctrl "sigs.k8s.io/controller-runtime"
)

const ciliumAPIGroup = "cilium.io"

var preferredVersions = []string{"v2", "v2alpha1"}

// CiliumVersions holds the resolved GroupVersionKind for Cilium resources used by the operator.
type CiliumVersions struct {
	LoadBalancerIPPool schema.GroupVersionKind
	CIDRGroup          schema.GroupVersionKind
	BGPAdvertisement   schema.GroupVersionKind
}

// DiscoverCiliumVersions probes the API server and resolves the preferred served version
// for each Cilium resource used by the operator.
func DiscoverCiliumVersions(dc discovery.DiscoveryInterface) (*CiliumVersions, error) {
	log := ctrl.Log.WithName("setup")

	available, err := discoverCiliumGroupVersions(dc)
	if err != nil {
		return nil, fmt.Errorf("failed to discover cilium.io API versions: %w", err)
	}

	resourceVersions := discoverCiliumResources(dc, available)

	resolve := func(kind, plural string) (schema.GroupVersionKind, error) {
		for _, v := range preferredVersions {
			if hasResource(resourceVersions, plural, v) {
				gvk := schema.GroupVersionKind{Group: ciliumAPIGroup, Version: v, Kind: kind}
				log.Info("Resolved Cilium API version", "kind", kind, "version", v)
				return gvk, nil
			}
		}

		return schema.GroupVersionKind{}, fmt.Errorf("no supported API version found for %s/%s (checked %v)", ciliumAPIGroup, kind, preferredVersions)
	}

	lbPool, err := resolve("CiliumLoadBalancerIPPool", "ciliumloadbalancerippools")
	if err != nil {
		return nil, err
	}
	cidrGroup, err := resolve("CiliumCIDRGroup", "ciliumcidrgroups")
	if err != nil {
		return nil, err
	}
	bgpAdv, err := resolve("CiliumBGPAdvertisement", "ciliumbgpadvertisements")
	if err != nil {
		return nil, err
	}

	return &CiliumVersions{
		LoadBalancerIPPool: lbPool,
		CIDRGroup:          cidrGroup,
		BGPAdvertisement:   bgpAdv,
	}, nil
}

// ListGVK returns the list variant of a given GVK.
func ListGVK(gvk schema.GroupVersionKind) schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind + "List",
	}
}

// APIVersion returns the apiVersion string for a GVK.
func APIVersion(gvk schema.GroupVersionKind) string {
	return gvk.Group + "/" + gvk.Version
}

func discoverCiliumGroupVersions(dc discovery.DiscoveryInterface) ([]string, error) {
	groups, err := dc.ServerGroups()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch API groups: %w", err)
	}

	for _, g := range groups.Groups {
		if g.Name == ciliumAPIGroup {
			versions := make([]string, 0, len(g.Versions))
			for _, v := range g.Versions {
				versions = append(versions, v.Version)
			}
			return versions, nil
		}
	}

	return nil, fmt.Errorf("%s API group not found on the cluster — is Cilium installed?", ciliumAPIGroup)
}

type resourceSet map[string]map[string]bool

func discoverCiliumResources(dc discovery.DiscoveryInterface, versions []string) resourceSet {
	rs := make(resourceSet)

	for _, v := range versions {
		resources, err := dc.ServerResourcesForGroupVersion(ciliumAPIGroup + "/" + v)
		if err != nil {
			continue
		}

		for _, r := range resources.APIResources {
			if _, ok := rs[r.Name]; !ok {
				rs[r.Name] = make(map[string]bool)
			}
			rs[r.Name][v] = true
		}
	}

	return rs
}

func hasResource(rs resourceSet, plural, version string) bool {
	if versions, ok := rs[plural]; ok {
		return versions[version]
	}
	return false
}

// CiliumControllerStarter polls for Cilium API availability and registers Cilium-dependent controllers once available.
type CiliumControllerStarter struct {
	Discovery        discovery.DiscoveryInterface
	PollInterval     time.Duration
	SetupControllers func(versions *CiliumVersions) error
}

// Start implements manager.Runnable.
func (s *CiliumControllerStarter) Start(ctx context.Context) error {
	log := ctrl.Log.WithName("setup")

	interval := s.PollInterval
	if interval == 0 {
		interval = 30 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Info("Waiting for Cilium APIs to become available", "pollInterval", interval)

	for {
		select {
		case <-ctx.Done():
			log.Info("Stopping Cilium API discovery (context cancelled)")
			return nil
		case <-ticker.C:
			versions, err := DiscoverCiliumVersions(s.Discovery)
			if err != nil {
				log.V(1).Info("Cilium API not yet available, will retry", "reason", err.Error())
				continue
			}

			log.Info("Cilium APIs detected, registering Cilium-dependent controllers")
			if err := s.SetupControllers(versions); err != nil {
				return fmt.Errorf("failed to register Cilium controllers: %w", err)
			}

			log.Info("Cilium-dependent controllers registered successfully")
			return nil
		}
	}
}

// NeedLeaderElection implements manager.LeaderElectionRunnable.
func (s *CiliumControllerStarter) NeedLeaderElection() bool {
	return true
}
