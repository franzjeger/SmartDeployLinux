// Interactive PXE boot menu (Phase 23).
//
// Internal endpoints, reached via http-boot's nginx > render proxy:
//   GET /internal/render/menu/by-mac/{mac}          - the iPXE menu
//   GET /internal/render/menu/deploy/{mac}/{profile} - mint token, hand
//       off to the token boot path ({profile} = uuid | "assigned")
//
// Authorization: PXE_MENU_MODE=locked (default) shows/permits only the
// machine's assigned action - semantically identical to the existing
// network-trusted by-mac path. PXE_MENU_MODE=open additionally lets any
// LAN client deploy any registered profile to any registered machine
// ("lab mode" - see SECURITY.md). Unregistered MACs can never deploy.
//
// The deploy endpoint mints a real one-shot boot token and chains
// /boot/<token>.ipxe - which also fixes the latent by-mac bug where the
// rendered script carried an empty token (broken nocloud URL, empty
// WinPE bearer). Errors return HTTP 200 with a valid iPXE script so the
// menu's `|| goto menu_failed` recovers instead of iPXE dumping an
// HTTP error.

package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/catalog"
	"github.com/your-org/deployserver/api/internal/store"
	"github.com/your-org/deployserver/api/internal/tokens"
)

func pxeMenuMode() string {
	if os.Getenv("PXE_MENU_MODE") == "open" {
		return "open"
	}
	return "locked"
}

func pxeMenuTimeoutMS() int {
	if n, err := strconv.Atoi(os.Getenv("PXE_MENU_TIMEOUT_MS")); err == nil && n >= 1000 {
		return n
	}
	return 8000
}

// ipxeError writes a *valid* iPXE script that surfaces the message and
// fails, so the calling menu's || fallback path recovers.
func ipxeError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, "#!ipxe\necho %s\nsleep 3\nexit 1\n", msg)
}

type menuData struct {
	mac         string
	machine     *store.Machine
	bundle      *store.RenderBundle // nil when no assigned profile/job
	profiles    []store.Profile     // open mode only
	catalogOK   bool
	memtestURL  string
}

// GET /internal/render/menu/by-mac/{mac}
func (h *handlers) renderMenuByMAC(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(chi.URLParam(r, "mac"))
	d := menuData{mac: mac}

	if m, err := h.store.GetMachineByMAC(r.Context(), mac); err == nil {
		d.machine = m
		if b, err := h.store.LookupRenderBundle(r.Context(), m.ID); err == nil {
			d.bundle = b
		}
	}
	if pxeMenuMode() == "open" && d.machine != nil {
		d.profiles, _ = h.store.ListProfiles(r.Context())
	}
	if c, err := catalog.Load(); err == nil && len(c.Categories) > 0 {
		d.catalogOK = true
		for _, cat := range c.Categories {
			for _, e := range cat.Entries {
				if e.ID == "memtest" {
					if u, _ := e.Media["kernel_url"].(string); u != "" {
						d.memtestURL = u
					}
				}
			}
		}
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(h.buildMenuScript(d)))
}

func (h *handlers) buildMenuScript(d menuData) string {
	var b strings.Builder
	fqdn := h.deployFQDN
	assigned := d.bundle != nil

	title := "unregistered MAC " + d.mac
	if d.machine != nil {
		tag := d.machine.ID.String()[:8]
		if d.machine.AssetTag != nil {
			tag = *d.machine.AssetTag
		}
		if assigned {
			title = fmt.Sprintf("%s (profile %s)", tag, d.bundle.ProfileName)
		} else {
			title = tag + " (no profile assigned)"
		}
	}

	fmt.Fprintf(&b, "#!ipxe\n# deployserver PXE menu - mac %s mode %s\n\n:start\nmenu deployserver - %s\n", d.mac, pxeMenuMode(), title)
	if assigned {
		fmt.Fprintf(&b, "item deploy_assigned Deploy assigned profile: %s\n", d.bundle.ProfileName)
	}
	if len(d.profiles) > 0 {
		b.WriteString("item --gap -- -- Deploy a registered profile to this machine --\n")
		for i, p := range d.profiles {
			fmt.Fprintf(&b, "item prof_%d %s (%s %s)\n", i, p.Name, p.OSFamily, p.OSVersion)
		}
	}
	if d.catalogOK {
		b.WriteString("item --gap -- -- Distro catalog (untracked net-install) --\n")
		b.WriteString("item catalog Browse distro catalog ...\n")
	}
	b.WriteString("item --gap -- -- Enrollment --\nitem gather Gather Computer Info (register this machine)\n")
	b.WriteString("item --gap -- -- Tools --\n")
	if d.memtestURL != "" {
		b.WriteString("item memtest Memtest86+\n")
	}
	b.WriteString("item shell iPXE shell\nitem reboot Reboot\nitem --gap -- --\nitem local Boot from local disk\n")
	if d.machine == nil {
		b.WriteString("item --gap -- Register this MAC in the operator UI to enable deployment\n")
	} else if !assigned {
		b.WriteString("item --gap -- Assign a default profile in the operator UI to enable deployment\n")
	}

	defaultItem, timeout := "local", 30000
	if assigned {
		defaultItem, timeout = "deploy_assigned", pxeMenuTimeoutMS()
	}
	fmt.Fprintf(&b, "choose --default %s --timeout %d selected || goto local\ngoto ${selected}\n\n", defaultItem, timeout)

	if assigned {
		fmt.Fprintf(&b, ":deploy_assigned\nchain https://%s/boot/menu/deploy/%s/assigned.ipxe || goto menu_failed\n\n", fqdn, d.mac)
	}
	for i, p := range d.profiles {
		fmt.Fprintf(&b, ":prof_%d\nchain https://%s/boot/menu/deploy/%s/%s.ipxe || goto menu_failed\n\n", i, fqdn, d.mac, p.ID)
	}
	if d.catalogOK {
		fmt.Fprintf(&b, ":catalog\nchain https://%s/catalog/menu.ipxe || goto menu_failed\n\n", fqdn)
	}
	if d.memtestURL != "" {
		fmt.Fprintf(&b, ":memtest\nkernel %s\nboot || goto menu_failed\n\n", d.memtestURL)
	}
	fmt.Fprintf(&b, `:gather
echo Gathering hardware info from SMBIOS ...
params
param mac ${net0/mac}
param manufacturer ${manufacturer}
param product ${product}
param serial ${serial}
param uuid ${uuid}
param asset ${asset}
chain https://%s/enroll##params || goto menu_failed

`, fqdn)
	b.WriteString(":shell\nshell\ngoto start\n\n:reboot\nreboot\n\n:local\necho Booting from local disk ...\nexit 1 || sanboot --no-describe --drive 0x80 || goto start\n\n:menu_failed\necho Action failed. Returning to menu in 5s ...\nsleep 5\ngoto start\n")
	return b.String()
}

// GET /internal/render/menu/deploy/{mac}/{profile}
func (h *handlers) renderMenuDeploy(w http.ResponseWriter, r *http.Request) {
	mac := strings.ToLower(chi.URLParam(r, "mac"))
	profileArg := chi.URLParam(r, "profile")

	m, err := h.store.GetMachineByMAC(r.Context(), mac)
	if err != nil {
		ipxeError(w, "machine not registered; register the MAC in the operator UI")
		return
	}

	// Resolve the assigned profile (active job's, else default).
	var assignedProfile uuid.UUID
	if b, err := h.store.LookupRenderBundle(r.Context(), m.ID); err == nil {
		assignedProfile = b.ProfileID
	}

	var target uuid.UUID
	switch {
	case profileArg == "assigned":
		if assignedProfile == uuid.Nil {
			ipxeError(w, "no profile assigned to this machine")
			return
		}
		target = assignedProfile
	default:
		p, err := uuid.Parse(profileArg)
		if err != nil {
			ipxeError(w, "bad profile id")
			return
		}
		if pxeMenuMode() != "open" && p != assignedProfile {
			ipxeError(w, "PXE menu is locked to the assigned profile (PXE_MENU_MODE=locked)")
			return
		}
		target = p
	}

	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		ipxeError(w, "token generation failed")
		return
	}
	tok := base64.RawURLEncoding.EncodeToString(raw[:])
	jobID, err := h.store.MintMenuBootToken(r.Context(), m.ID, target,
		tokens.HashBootToken(tok, h.bootTokenPepper()), time.Hour)
	if err != nil {
		ipxeError(w, "could not create deployment")
		return
	}

	srcIP, _ := readSourceIP(r)
	_ = h.store.Audit(r.Context(), store.AuditEvent{
		ActorKind: "pxe-menu", Action: "job.created_via_pxe_menu",
		SubjectID: &m.ID, SubjectKind: "machine",
		Data:     mustJSON(map[string]string{"job_id": jobID.String(), "profile_id": target.String()}),
		SourceIP: srcIP,
	})

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, "#!ipxe\necho Starting deployment (job %.8s) ...\nchain https://%s/boot/%s.ipxe\n", jobID.String(), h.deployFQDN, tok)
}
