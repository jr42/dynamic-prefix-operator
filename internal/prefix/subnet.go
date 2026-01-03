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

package prefix

import (
	"fmt"
	"math/big"
	"net/netip"
)

// SubnetConfig defines a subnet to be carved from a prefix
type SubnetConfig struct {
	// Name identifies the subnet
	Name string

	// Offset selects which Nth subnet to carve from the base prefix.
	// For example, with a /48 base and /64 target, offset 0 gives the first /64,
	// offset 1 gives the second /64, and so on.
	Offset int64

	// PrefixLength is the desired prefix length of the subnet
	PrefixLength int
}

// Subnet represents a calculated subnet
type Subnet struct {
	// Name identifies the subnet
	Name string

	// CIDR is the subnet in CIDR notation
	CIDR netip.Prefix
}

// CalculateSubnets computes subnet CIDRs from a base prefix and subnet configurations
func CalculateSubnets(basePrefix netip.Prefix, configs []SubnetConfig) ([]Subnet, error) {
	if !basePrefix.Addr().Is6() {
		return nil, fmt.Errorf("base prefix must be IPv6: %s", basePrefix)
	}

	subnets := make([]Subnet, 0, len(configs))

	for _, cfg := range configs {
		subnet, err := CalculateSubnet(basePrefix, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate subnet %q: %w", cfg.Name, err)
		}
		subnets = append(subnets, subnet)
	}

	return subnets, nil
}

// CalculateSubnet computes a single subnet from a base prefix and configuration
func CalculateSubnet(basePrefix netip.Prefix, cfg SubnetConfig) (Subnet, error) {
	if cfg.PrefixLength < basePrefix.Bits() {
		return Subnet{}, fmt.Errorf(
			"subnet prefix length %d is shorter than base prefix length %d",
			cfg.PrefixLength, basePrefix.Bits(),
		)
	}

	if cfg.PrefixLength > 128 {
		return Subnet{}, fmt.Errorf("subnet prefix length %d exceeds 128", cfg.PrefixLength)
	}

	// Get the base address as bytes
	baseAddr := basePrefix.Addr()
	baseBytes := baseAddr.As16()

	// Convert to big.Int for arithmetic
	baseInt := new(big.Int).SetBytes(baseBytes[:])

	// Calculate subnet size: 2^(128 - prefixLength)
	// This is how many addresses are in each subnet of the target prefix length
	hostBits := uint(128 - cfg.PrefixLength)
	subnetSize := new(big.Int).Lsh(big.NewInt(1), hostBits)

	// Calculate the address offset by multiplying subnet index by subnet size
	offset := new(big.Int).Mul(big.NewInt(cfg.Offset), subnetSize)
	subnetInt := new(big.Int).Add(baseInt, offset)

	// Convert back to bytes
	subnetBytes := subnetInt.FillBytes(make([]byte, 16))

	// Create the address
	var addr16 [16]byte
	copy(addr16[:], subnetBytes)
	subnetAddr := netip.AddrFrom16(addr16)

	// Create the prefix with the specified length
	subnetPrefix, err := subnetAddr.Prefix(cfg.PrefixLength)
	if err != nil {
		return Subnet{}, fmt.Errorf("failed to create subnet prefix: %w", err)
	}

	return Subnet{
		Name: cfg.Name,
		CIDR: subnetPrefix,
	}, nil
}

// ValidateSubnetFitsInPrefix checks if a subnet configuration fits within a base prefix
func ValidateSubnetFitsInPrefix(basePrefix netip.Prefix, cfg SubnetConfig) error {
	// Calculate the subnet
	subnet, err := CalculateSubnet(basePrefix, cfg)
	if err != nil {
		return err
	}

	// Check that the subnet's base address is within the base prefix
	if !basePrefix.Contains(subnet.CIDR.Addr()) {
		return fmt.Errorf(
			"subnet %s (%s) is outside base prefix %s",
			cfg.Name, subnet.CIDR, basePrefix,
		)
	}

	return nil
}

// ParsePrefix parses a CIDR string into a netip.Prefix.
// The returned prefix is normalized to the network address (host bits zeroed).
func ParsePrefix(cidr string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	// Normalize to network address by masking host bits
	return prefix.Masked(), nil
}
