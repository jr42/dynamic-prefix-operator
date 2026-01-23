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
	"net/netip"
)

// AddressRangeConfig defines an address range to be calculated within a prefix.
type AddressRangeConfig struct {
	// Name identifies this address range
	Name string

	// Start is the start offset suffix (e.g., "::f000:0:0:0")
	Start string

	// End is the end offset suffix (e.g., "::ffff:ffff:ffff:ffff")
	End string
}

// AddressRange represents a calculated address range.
type AddressRange struct {
	// Name is the identifier
	Name string

	// Start is the first address in the range
	Start netip.Addr

	// End is the last address in the range
	End netip.Addr
}

// CalculateAddressRanges calculates address ranges from a base prefix and range configs.
func CalculateAddressRanges(basePrefix netip.Prefix, configs []AddressRangeConfig) ([]AddressRange, error) {
	if !basePrefix.Addr().Is6() {
		return nil, fmt.Errorf("address ranges only supported for IPv6 prefixes")
	}

	results := make([]AddressRange, 0, len(configs))
	for _, cfg := range configs {
		ar, err := CalculateAddressRange(basePrefix, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate address range %q: %w", cfg.Name, err)
		}
		results = append(results, ar)
	}

	return results, nil
}

// CalculateAddressRange calculates a single address range from a base prefix.
func CalculateAddressRange(basePrefix netip.Prefix, cfg AddressRangeConfig) (AddressRange, error) {
	// Parse the start and end suffixes
	startAddr, err := parseOffsetSuffix(basePrefix, cfg.Start)
	if err != nil {
		return AddressRange{}, fmt.Errorf("invalid start offset %q: %w", cfg.Start, err)
	}

	endAddr, err := parseOffsetSuffix(basePrefix, cfg.End)
	if err != nil {
		return AddressRange{}, fmt.Errorf("invalid end offset %q: %w", cfg.End, err)
	}

	// Validate start <= end
	if startAddr.Compare(endAddr) > 0 {
		return AddressRange{}, fmt.Errorf("start address %s is greater than end address %s", startAddr, endAddr)
	}

	// Validate both addresses are within the prefix
	if !basePrefix.Contains(startAddr) {
		return AddressRange{}, fmt.Errorf("start address %s is outside prefix %s", startAddr, basePrefix)
	}
	if !basePrefix.Contains(endAddr) {
		return AddressRange{}, fmt.Errorf("end address %s is outside prefix %s", endAddr, basePrefix)
	}

	return AddressRange{
		Name:  cfg.Name,
		Start: startAddr,
		End:   endAddr,
	}, nil
}

// parseOffsetSuffix parses an offset suffix like "::f000:0:0:0" and combines it with
// the base prefix to produce a full address.
func parseOffsetSuffix(basePrefix netip.Prefix, suffix string) (netip.Addr, error) {
	// Parse the suffix as an address (it will be zero-padded on the left)
	suffixAddr, err := netip.ParseAddr(suffix)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid suffix address: %w", err)
	}

	if !suffixAddr.Is6() {
		return netip.Addr{}, fmt.Errorf("suffix must be an IPv6 address")
	}

	// Get the base prefix address and mask
	baseAddr := basePrefix.Masked().Addr()
	prefixBits := basePrefix.Bits()

	// Combine: take prefix bits from base, remaining bits from suffix
	baseBytes := baseAddr.As16()
	suffixBytes := suffixAddr.As16()
	resultBytes := [16]byte{}

	// Copy the prefix portion from base
	fullBytes := prefixBits / 8
	remainingBits := prefixBits % 8

	for i := 0; i < fullBytes; i++ {
		resultBytes[i] = baseBytes[i]
	}

	// Handle partial byte at the boundary
	if remainingBits > 0 && fullBytes < 16 {
		mask := byte(0xFF << (8 - remainingBits))
		resultBytes[fullBytes] = (baseBytes[fullBytes] & mask) | (suffixBytes[fullBytes] & ^mask)
		fullBytes++
	}

	// Copy the remaining suffix portion
	for i := fullBytes; i < 16; i++ {
		resultBytes[i] = suffixBytes[i]
	}

	return netip.AddrFrom16(resultBytes), nil
}

// RangeToCIDR attempts to convert an address range to a CIDR.
// If the range doesn't align to CIDR boundaries, it returns the smallest
// CIDR that contains the entire range.
func RangeToCIDR(start, end netip.Addr) netip.Prefix {
	// Find the common prefix bits
	startBytes := start.As16()
	endBytes := end.As16()

	commonBits := 0
	for i := 0; i < 16; i++ {
		if startBytes[i] == endBytes[i] {
			commonBits += 8
		} else {
			// Find common bits within this byte
			xor := startBytes[i] ^ endBytes[i]
			for xor != 0 {
				xor >>= 1
			}
			// Count leading zeros in the XOR
			diff := startBytes[i] ^ endBytes[i]
			for bit := 7; bit >= 0; bit-- {
				if (diff & (1 << bit)) != 0 {
					break
				}
				commonBits++
			}
			break
		}
	}

	// Create prefix with the common bits
	prefix, _ := start.Prefix(commonBits)
	return prefix.Masked()
}

// AddressCount returns the number of addresses in a range.
// Returns 0 if the range is too large to represent (>2^64).
func AddressCount(start, end netip.Addr) uint64 {
	startBytes := start.As16()
	endBytes := end.As16()

	// Check if the upper 8 bytes differ - if so, range is > 2^64
	for i := 0; i < 8; i++ {
		if startBytes[i] != endBytes[i] {
			return 0 // Too large
		}
	}

	// Calculate from lower 8 bytes
	var startLow, endLow uint64
	for i := 8; i < 16; i++ {
		startLow = (startLow << 8) | uint64(startBytes[i])
		endLow = (endLow << 8) | uint64(endBytes[i])
	}

	return endLow - startLow + 1
}
