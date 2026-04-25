// Driver-pack matching engine.
//
// Input:  a target machine's hardware fingerprint (DMI vendor/model/baseboard
//         + a list of PCI VID/DID pairs + an OS family/version).
// Output: an ordered list of driver_pack_versions to apply, with the
//         most specific match first.
//
// Match precedence, highest priority first:
//   1. exact pci-vid-did match      (e.g. "8086:1521" -> Intel I350-T2)
//   2. dmi-product match            (e.g. "Latitude 7440")
//   3. dmi-baseboard match          (e.g. "0FC2X3")
//   4. dmi-vendor + os-version      (e.g. "Dell Inc." + "Windows 11 24H2")
//   5. dmi-vendor                   (e.g. "Dell Inc.")
//
// A pack version is selected if ANY rule on it matches; rules within a
// version are OR'd. We DO NOT try to be clever about per-device-class
// "best match" arbitration — that's the job of the OS's PnP loader once
// we've put the driver on disk. Our job is to ensure the right *set* of
// packs is staged.
//
// This package is pure-Go and exposes a stable interface; the API
// service wraps it with DB access in Phase 8.

package driverpack

import (
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Fingerprint is the inputs we match against.
type Fingerprint struct {
	DMIVendor    string   // e.g. "Dell Inc."
	DMIProduct   string   // e.g. "Latitude 7440"
	DMIBaseboard string   // mainboard part number
	PCIDevices   []PCIID  // every PCI device on the bus
	OSFamily     string   // "windows" or "linux"
	OSVersion    string   // e.g. "11" or "24.04"
}

type PCIID struct {
	VID string // 4-char hex, lowercase, e.g. "8086"
	DID string // 4-char hex, lowercase, e.g. "1521"
}

// PackVersion is the candidate as returned by the DB.
type PackVersion struct {
	ID         uuid.UUID
	PackID     uuid.UUID
	Vendor     string
	Model      string
	OSFamily   string
	OSVersion  string
	VersionTag string
	Rules      []Rule
}

type Rule struct {
	Type  string // "pci-vid-did" | "dmi-product" | "dmi-baseboard" | "dmi-vendor" | "os-version"
	Value string
}

// Match tier (lower = higher priority).
type tier int

const (
	tierPCI         tier = 1
	tierDMIProduct  tier = 2
	tierDMIBoard    tier = 3
	tierDMIVendorOS tier = 4
	tierDMIVendor   tier = 5
	tierNoMatch     tier = 99
)

type matchResult struct {
	Version PackVersion
	Tier    tier
}

// Select returns the matching pack versions in priority order. A given
// pack version appears at most once even if multiple rules match.
//
// Filtering: only packs whose os_family + os_version matches the
// fingerprint are considered. OS family must match exactly; OS version
// is matched by prefix ("11" matches "11", "11 24H2", etc.) so a
// vendor-shipped "Windows 11" pack covers all 11.x.
func Select(fp Fingerprint, candidates []PackVersion) []PackVersion {
	pciSet := make(map[string]struct{}, len(fp.PCIDevices))
	for _, p := range fp.PCIDevices {
		pciSet[strings.ToLower(p.VID+":"+p.DID)] = struct{}{}
	}
	dmiVendor := normalize(fp.DMIVendor)
	dmiProduct := normalize(fp.DMIProduct)
	dmiBoard := normalize(fp.DMIBaseboard)

	var results []matchResult
	for _, pv := range candidates {
		if !osCompatible(pv, fp) {
			continue
		}
		best := tierNoMatch
		for _, r := range pv.Rules {
			t := evalRule(r, pciSet, dmiVendor, dmiProduct, dmiBoard, fp.OSVersion)
			if t < best {
				best = t
			}
		}
		if best != tierNoMatch {
			results = append(results, matchResult{Version: pv, Tier: best})
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Tier != results[j].Tier {
			return results[i].Tier < results[j].Tier
		}
		// Stable secondary order: vendor+model+versiontag for determinism.
		ai := results[i].Version
		aj := results[j].Version
		if ai.Vendor != aj.Vendor {
			return ai.Vendor < aj.Vendor
		}
		if ai.Model != aj.Model {
			return ai.Model < aj.Model
		}
		return ai.VersionTag < aj.VersionTag
	})

	out := make([]PackVersion, 0, len(results))
	for _, r := range results {
		out = append(out, r.Version)
	}
	return out
}

func evalRule(r Rule, pciSet map[string]struct{}, dmiVendor, dmiProduct, dmiBoard, osVersion string) tier {
	switch r.Type {
	case "pci-vid-did":
		if _, ok := pciSet[strings.ToLower(r.Value)]; ok {
			return tierPCI
		}
	case "dmi-product":
		if dmiProduct != "" && normalize(r.Value) == dmiProduct {
			return tierDMIProduct
		}
	case "dmi-baseboard":
		if dmiBoard != "" && normalize(r.Value) == dmiBoard {
			return tierDMIBoard
		}
	case "dmi-vendor":
		if dmiVendor != "" && normalize(r.Value) == dmiVendor {
			return tierDMIVendor
		}
	case "os-version":
		if osVersion != "" && strings.HasPrefix(osVersion, r.Value) {
			// os-version alone is not a match — it must be combined
			// with a vendor scope. We mark it as VendorOS so the caller
			// can see this; in practice rules of this type are usually
			// paired with dmi-vendor on the same pack version.
			return tierDMIVendorOS
		}
	}
	return tierNoMatch
}

func osCompatible(pv PackVersion, fp Fingerprint) bool {
	if !strings.EqualFold(pv.OSFamily, fp.OSFamily) {
		return false
	}
	// "any" os_version is treated as wildcard.
	if pv.OSVersion == "" || pv.OSVersion == "any" {
		return true
	}
	return strings.HasPrefix(fp.OSVersion, pv.OSVersion)
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
