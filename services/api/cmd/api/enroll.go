package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/driverpack"
	"github.com/your-org/deployserver/api/internal/store"
)

// ipxeSafe strips characters that could break out of an `echo` line or
// inject iPXE commands (newlines, ${...} expansion). SMBIOS strings come
// from untrusted firmware, so never echo them raw.
func ipxeSafe(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", "$", " ", "{", " ", "}", " ").Replace(s)
	s = strings.TrimSpace(s)
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "" {
		return "-"
	}
	return s
}

func enrollStrPtr(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

// POST /enroll — self-service hardware gather + registration from the PXE
// "Gather Computer Info" menu item. Network-trust path (same model as the
// by-mac menu): no operator auth. iPXE POSTs SMBIOS fields as a urlencoded
// form; we upsert the machine by MAC and report matched driver packs.
// Always returns a valid iPXE script that chains back to the menu.
func (h *handlers) renderEnroll(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		ipxeError(w, "enroll: bad form")
		return
	}
	mac := strings.ToLower(strings.TrimSpace(r.FormValue("mac")))
	vendor := strings.TrimSpace(r.FormValue("manufacturer"))
	product := strings.TrimSpace(r.FormValue("product"))
	serial := strings.TrimSpace(r.FormValue("serial"))
	smbiosUUID := strings.TrimSpace(r.FormValue("uuid"))
	if mac == "" {
		ipxeError(w, "enroll: missing mac")
		return
	}

	var uPtr *uuid.UUID
	if u, err := uuid.Parse(smbiosUUID); err == nil && u != uuid.Nil {
		uPtr = &u
	}
	attrs, _ := json.Marshal(map[string]any{
		"enrolled_via": "pxe-gather",
		"hardware": map[string]string{
			"manufacturer": vendor,
			"product":      product,
			"serial":       serial,
			"smbios_uuid":  smbiosUUID,
		},
	})

	var m *store.Machine
	var err error
	created := false
	if existing, e := h.store.GetMachineByMAC(r.Context(), mac); e == nil && existing != nil {
		in := store.UpdateMachineInput{Vendor: enrollStrPtr(vendor), Model: enrollStrPtr(product), Attributes: attrs}
		m, err = h.store.UpdateMachine(r.Context(), existing.ID, in)
	} else {
		created = true
		in := store.CreateMachineInput{MACPrimary: &mac, Vendor: enrollStrPtr(vendor), Model: enrollStrPtr(product), Attributes: attrs, UUIDSMBIOS: uPtr}
		if serial != "" {
			in.AssetTag = &serial
		}
		m, err = h.store.CreateMachine(r.Context(), in)
	}
	if err != nil || m == nil {
		ipxeError(w, "enroll: could not save machine")
		return
	}

	// DMI-based driver-pack match (iPXE has no PCI list). Check both OS
	// families and de-dup by version.
	seen := map[uuid.UUID]bool{}
	var packs []store.MatchedDriverPack
	for _, osf := range []string{"windows", "linux"} {
		fp := driverpack.Fingerprint{DMIVendor: vendor, DMIProduct: product, OSFamily: osf}
		ms, _ := h.store.MatchDriverPacks(r.Context(), fp)
		for _, p := range ms {
			if !seen[p.VersionID] {
				seen[p.VersionID] = true
				packs = append(packs, p)
			}
		}
	}

	srcIP, _ := readSourceIP(r)
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorKind: "pxe-gather", Action: "machine.enrolled_via_pxe",
		SubjectID: &m.ID, SubjectKind: "machine",
		Data:     mustJSON(map[string]any{"mac": mac, "vendor": vendor, "model": product, "created": created, "driver_packs": len(packs)}),
		SourceIP: srcIP,
	})

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	var b strings.Builder
	b.WriteString("#!ipxe\n")
	b.WriteString("echo ====================================\n")
	if created {
		b.WriteString("echo  Computer registered\n")
	} else {
		b.WriteString("echo  Computer info updated\n")
	}
	b.WriteString("echo ====================================\n")
	fmt.Fprintf(&b, "echo  Vendor : %s\n", ipxeSafe(vendor))
	fmt.Fprintf(&b, "echo  Model  : %s\n", ipxeSafe(product))
	fmt.Fprintf(&b, "echo  Serial : %s\n", ipxeSafe(serial))
	fmt.Fprintf(&b, "echo  MAC    : %s\n", ipxeSafe(mac))
	fmt.Fprintf(&b, "echo  ID     : %.8s\n", m.ID.String())
	if len(packs) == 0 {
		b.WriteString("echo  Driver packs: none matched on vendor/model\n")
	} else {
		fmt.Fprintf(&b, "echo  Driver packs matched: %d\n", len(packs))
		for _, p := range packs {
			fmt.Fprintf(&b, "echo   - %s / %s (%s)\n", ipxeSafe(p.Vendor), ipxeSafe(p.Model), ipxeSafe(p.VersionTag))
		}
	}
	b.WriteString("echo ====================================\n")
	b.WriteString("prompt --timeout 15000 Press any key to return to the menu ... ||\n")
	fmt.Fprintf(&b, "chain https://%s/boot/menu/by-mac/%s.ipxe\n", h.deployFQDN, mac)
	_, _ = w.Write([]byte(b.String()))
}
