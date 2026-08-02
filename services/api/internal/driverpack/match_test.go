package driverpack

import (
	"testing"

	"github.com/google/uuid"
)

func mkPV(vendor, model, family, version string, rules ...Rule) PackVersion {
	return PackVersion{
		ID:         uuid.New(),
		PackID:     uuid.New(),
		Vendor:     vendor,
		Model:      model,
		OSFamily:   family,
		OSVersion:  version,
		VersionTag: "1.0",
		Rules:      rules,
	}
}

func TestSelect_PCIBeatsDMIVendor(t *testing.T) {
	pciPack := mkPV("Intel", "I350", "windows", "11",
		Rule{Type: "pci-vid-did", Value: "8086:1521"})
	vendorPack := mkPV("Dell Inc.", "Generic", "windows", "11",
		Rule{Type: "dmi-vendor", Value: "Dell Inc."})

	fp := Fingerprint{
		DMIVendor:  "Dell Inc.",
		DMIProduct: "Latitude 7440",
		PCIDevices: []PCIID{{"8086", "1521"}},
		OSFamily:   "windows",
		OSVersion:  "11 24H2",
	}

	got := Select(fp, []PackVersion{vendorPack, pciPack})
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if got[0].ID != pciPack.ID {
		t.Fatalf("expected PCI pack first, got %s", got[0].Model)
	}
	if got[1].ID != vendorPack.ID {
		t.Fatalf("expected vendor pack second, got %s", got[1].Model)
	}
}

func TestSelect_DMIProductBeatsDMIVendor(t *testing.T) {
	productPack := mkPV("Dell Inc.", "Latitude 7440", "windows", "11",
		Rule{Type: "dmi-product", Value: "Latitude 7440"})
	vendorPack := mkPV("Dell Inc.", "Generic", "windows", "11",
		Rule{Type: "dmi-vendor", Value: "Dell Inc."})

	fp := Fingerprint{
		DMIVendor:  "Dell Inc.",
		DMIProduct: "Latitude 7440",
		OSFamily:   "windows",
		OSVersion:  "11 24H2",
	}

	got := Select(fp, []PackVersion{vendorPack, productPack})
	if got[0].ID != productPack.ID {
		t.Fatalf("expected product pack first")
	}
}

func TestSelect_OSFamilyMustMatch(t *testing.T) {
	winPack := mkPV("Dell Inc.", "Generic", "windows", "11",
		Rule{Type: "dmi-vendor", Value: "Dell Inc."})
	linPack := mkPV("Dell Inc.", "Generic", "linux", "24.04",
		Rule{Type: "dmi-vendor", Value: "Dell Inc."})

	fp := Fingerprint{
		DMIVendor: "Dell Inc.",
		OSFamily:  "linux",
		OSVersion: "24.04",
	}
	got := Select(fp, []PackVersion{winPack, linPack})
	if len(got) != 1 || got[0].ID != linPack.ID {
		t.Fatalf("expected only linPack, got %d packs", len(got))
	}
}

func TestSelect_OSVersionPrefix(t *testing.T) {
	pack := mkPV("Dell", "X", "windows", "11",
		Rule{Type: "dmi-vendor", Value: "Dell"})

	fp := Fingerprint{
		DMIVendor: "Dell",
		OSFamily:  "windows",
		OSVersion: "11 24H2",
	}
	if got := Select(fp, []PackVersion{pack}); len(got) != 1 {
		t.Fatalf("11 should prefix-match 11 24H2; got %d", len(got))
	}

	fp.OSVersion = "10 22H2"
	if got := Select(fp, []PackVersion{pack}); len(got) != 0 {
		t.Fatalf("11 should not prefix-match 10 22H2")
	}
}

func TestSelect_AnyOSVersionWildcard(t *testing.T) {
	pack := mkPV("Dell", "Universal", "windows", "any",
		Rule{Type: "dmi-vendor", Value: "Dell"})

	for _, ver := range []string{"10", "11", "11 24H2", "Server 2025"} {
		fp := Fingerprint{
			DMIVendor: "Dell",
			OSFamily:  "windows",
			OSVersion: ver,
		}
		if got := Select(fp, []PackVersion{pack}); len(got) != 1 {
			t.Errorf("wildcard pack should match osVersion=%q", ver)
		}
	}
}

func TestSelect_PCICaseInsensitive(t *testing.T) {
	pack := mkPV("Intel", "x", "windows", "11",
		Rule{Type: "pci-vid-did", Value: "8086:1521"})

	fp := Fingerprint{
		PCIDevices: []PCIID{{"8086", "1521"}},
		OSFamily:   "windows",
		OSVersion:  "11",
	}
	if got := Select(fp, []PackVersion{pack}); len(got) != 1 {
		t.Fatalf("lowercase PCI should match")
	}

	fp.PCIDevices[0].VID = "8086"
	fp.PCIDevices[0].DID = "1521"
	pack.Rules[0].Value = "8086:1521" // already lowercase
	if got := Select(fp, []PackVersion{pack}); len(got) != 1 {
		t.Fatalf("PCI should still match after re-set")
	}
}

func TestSelect_NoDoubleEntryWhenManyRulesMatch(t *testing.T) {
	pack := mkPV("Dell", "Latitude 7440", "windows", "11",
		Rule{Type: "dmi-vendor", Value: "Dell"},
		Rule{Type: "dmi-product", Value: "Latitude 7440"},
		Rule{Type: "pci-vid-did", Value: "8086:1521"},
	)
	fp := Fingerprint{
		DMIVendor:  "Dell",
		DMIProduct: "Latitude 7440",
		PCIDevices: []PCIID{{"8086", "1521"}},
		OSFamily:   "windows",
		OSVersion:  "11",
	}
	got := Select(fp, []PackVersion{pack})
	if len(got) != 1 {
		t.Fatalf("multi-rule pack should appear once, got %d", len(got))
	}
}

func TestSelect_DeterministicOrderOnTies(t *testing.T) {
	a := mkPV("AVendor", "AModel", "windows", "11", Rule{Type: "dmi-vendor", Value: "ZBrand"})
	b := mkPV("ZVendor", "ZModel", "windows", "11", Rule{Type: "dmi-vendor", Value: "ZBrand"})
	fp := Fingerprint{DMIVendor: "ZBrand", OSFamily: "windows", OSVersion: "11"}

	got1 := Select(fp, []PackVersion{a, b})
	got2 := Select(fp, []PackVersion{b, a})
	if got1[0].Vendor != got2[0].Vendor {
		t.Fatal("Select is not deterministic across input orders")
	}
	if got1[0].Vendor != "AVendor" {
		t.Fatalf("expected vendor sort to break tie, got %q", got1[0].Vendor)
	}
}

func TestSelect_NormalizesDMIWhitespaceAndCase(t *testing.T) {
	pack := mkPV("Dell", "Latitude 7440", "windows", "11",
		Rule{Type: "dmi-product", Value: "Latitude 7440"})
	fp := Fingerprint{
		DMIProduct: "  LATITUDE 7440  ",
		OSFamily:   "windows",
		OSVersion:  "11",
	}
	if got := Select(fp, []PackVersion{pack}); len(got) != 1 {
		t.Fatal("DMI matching should be case- and whitespace-insensitive")
	}
}

func TestSelect_DMIProductPrefixMatchesLenovoMTM(t *testing.T) {
	// Lenovo: catalog gives 4-char machine types; DMI product_name is
	// the full MTM with a per-config suffix.
	pack := mkPV("Lenovo", "ThinkPad X1 Carbon Gen 9", "windows", "11",
		Rule{Type: "dmi-product-prefix", Value: "20XW"},
		Rule{Type: "dmi-product-prefix", Value: "20XX"})
	other := mkPV("Lenovo", "ThinkPad T14", "windows", "11",
		Rule{Type: "dmi-product-prefix", Value: "20UD"})

	fp := Fingerprint{
		DMIVendor:  "LENOVO",
		DMIProduct: "20XW0026US",
		OSFamily:   "windows",
		OSVersion:  "11 24H2",
	}
	got := Select(fp, []PackVersion{other, pack})
	if len(got) != 1 || got[0].ID != pack.ID {
		t.Fatalf("expected only the X1 Carbon pack, got %+v", got)
	}
}

func TestSelect_DMIProductPrefixTiesWithExactProductTier(t *testing.T) {
	prefix := mkPV("Lenovo", "X1", "windows", "11",
		Rule{Type: "dmi-product-prefix", Value: "20XW"})
	vendorOnly := mkPV("Lenovo", "Generic", "windows", "11",
		Rule{Type: "dmi-vendor", Value: "LENOVO"})
	fp := Fingerprint{
		DMIVendor: "LENOVO", DMIProduct: "20XW0026US",
		OSFamily: "windows", OSVersion: "11",
	}
	got := Select(fp, []PackVersion{vendorOnly, prefix})
	if len(got) != 2 || got[0].ID != prefix.ID {
		t.Fatalf("prefix match must outrank vendor-only: %+v", got)
	}
}

func TestSelect_DMIProductPrefixEmptyValueNeverMatches(t *testing.T) {
	pack := mkPV("Lenovo", "X1", "windows", "11",
		Rule{Type: "dmi-product-prefix", Value: ""})
	fp := Fingerprint{DMIProduct: "20XW0026US", OSFamily: "windows", OSVersion: "11"}
	if got := Select(fp, []PackVersion{pack}); len(got) != 0 {
		t.Fatalf("empty prefix must not match everything: %+v", got)
	}
}
