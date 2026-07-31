package main

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/your-org/deployserver/api/internal/store"
)

func menuFixture(registered, assigned bool, profiles int) menuData {
	d := menuData{mac: "aa:bb:cc:dd:ee:ff", catalogOK: true}
	if registered {
		tag := "lab-01"
		d.machine = &store.Machine{ID: uuid.New(), AssetTag: &tag}
	}
	if assigned {
		d.bundle = &store.RenderBundle{ProfileID: uuid.New(), ProfileName: "Ubuntu base"}
	}
	for i := 0; i < profiles; i++ {
		d.profiles = append(d.profiles, store.Profile{
			ID: uuid.New(), Name: "prof", OSFamily: "linux", OSVersion: "24.04",
		})
	}
	return d
}

// assertValidIPXE checks the structural invariants every generated menu
// must hold: shebang, no backslash continuations, exactly one choose,
// and a matching :label block for every selectable item.
func assertValidIPXE(t *testing.T, script string) {
	t.Helper()
	if !strings.HasPrefix(script, "#!ipxe\n") {
		t.Fatal("missing #!ipxe shebang")
	}
	if strings.Contains(script, "\\\n") {
		t.Fatal("backslash line continuation (not iPXE syntax)")
	}
	if strings.Contains(script, "&amp;") {
		t.Fatal("HTML-escaped ampersand")
	}
	if n := strings.Count(script, "\nchoose "); n != 1 {
		t.Fatalf("choose count = %d, want 1", n)
	}
	itemRe := regexp.MustCompile(`(?m)^item (\S+)`)
	for _, m := range itemRe.FindAllStringSubmatch(script, -1) {
		label := m[1]
		if label == "--gap" {
			continue
		}
		if !strings.Contains(script, "\n:"+label+"\n") {
			t.Fatalf("item %q has no matching :%s block", label, label)
		}
	}
	if !strings.Contains(script, "goto ${selected}") {
		t.Fatal("missing goto ${selected}")
	}
}

func TestMenuScript_AssignedDefault(t *testing.T) {
	h := &handlers{deployFQDN: "deploy.example.com"}
	script := h.buildMenuScript(menuFixture(true, true, 0))
	assertValidIPXE(t, script)
	if !strings.Contains(script, "choose --default deploy_assigned --timeout 8000") {
		t.Fatalf("zero-touch default missing:\n%s", script)
	}
	if !strings.Contains(script, "/boot/menu/deploy/aa:bb:cc:dd:ee:ff/assigned.ipxe") {
		t.Fatalf("assigned deploy chain missing:\n%s", script)
	}
	if strings.Contains(script, "prof_0") {
		t.Fatal("locked-mode menu leaked profile items")
	}
}

func TestMenuScript_UnregisteredDefaultsLocal(t *testing.T) {
	h := &handlers{deployFQDN: "deploy.example.com"}
	script := h.buildMenuScript(menuFixture(false, false, 0))
	assertValidIPXE(t, script)
	if !strings.Contains(script, "choose --default local --timeout 30000") {
		t.Fatalf("unregistered machine must default to local boot:\n%s", script)
	}
	if strings.Contains(script, "deploy_assigned") {
		t.Fatal("unregistered menu offers deployment")
	}
	if !strings.Contains(script, "Register this MAC") {
		t.Fatal("missing registration hint")
	}
}

func TestMenuScript_OpenModeProfiles(t *testing.T) {
	h := &handlers{deployFQDN: "deploy.example.com"}
	d := menuFixture(true, true, 3)
	script := h.buildMenuScript(d)
	assertValidIPXE(t, script)
	for i := 0; i < 3; i++ {
		label := "prof_" + string(rune('0'+i))
		if !strings.Contains(script, "item "+label+" ") {
			t.Fatalf("missing %s item:\n%s", label, script)
		}
	}
	if !strings.Contains(script, "/boot/menu/deploy/aa:bb:cc:dd:ee:ff/"+d.profiles[0].ID.String()+".ipxe") {
		t.Fatalf("profile deploy chain missing:\n%s", script)
	}
}

func TestMenuScript_EmptyCatalogSkipsSection(t *testing.T) {
	h := &handlers{deployFQDN: "deploy.example.com"}
	d := menuFixture(true, true, 0)
	d.catalogOK = false
	script := h.buildMenuScript(d)
	assertValidIPXE(t, script)
	if strings.Contains(script, "item catalog") || strings.Contains(script, "\n:catalog\n") {
		t.Fatalf("empty catalog rendered a catalog section:\n%s", script)
	}
}

func TestIPXEError(t *testing.T) {
	rec := httptest.NewRecorder()
	ipxeError(rec, "boom")
	body := rec.Body.String()
	if !strings.HasPrefix(body, "#!ipxe\n") || !strings.Contains(body, "exit 1") {
		t.Fatalf("error script invalid:\n%s", body)
	}
	if rec.Code != 200 {
		t.Fatalf("iPXE errors must be HTTP 200 (menu recovers via ||), got %d", rec.Code)
	}
}
