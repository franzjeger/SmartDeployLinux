package catalog

import "testing"

func TestLoad_Parses(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Version == "" {
		t.Error("Version empty")
	}
	if len(c.Categories) == 0 {
		t.Fatal("no categories")
	}
	if c.Total() == 0 {
		t.Fatal("no entries")
	}
}

func TestLoad_Lookup(t *testing.T) {
	c, _ := Load()
	e, cat, err := c.Lookup("ubuntu-2404-server")
	if err != nil {
		t.Fatalf("ubuntu-2404-server lookup: %v", err)
	}
	if e.OSFamily != "linux" || e.OSVersion != "24.04" {
		t.Errorf("ubuntu entry malformed: %+v", e)
	}
	if cat.ID != "ubuntu" {
		t.Errorf("expected ubuntu category, got %s", cat.ID)
	}
	if _, _, err := c.Lookup("nonexistent-distro"); err == nil {
		t.Error("expected ErrNotFound for unknown id")
	}
}

func TestLoad_AllEntriesValid(t *testing.T) {
	c, _ := Load()
	seenIDs := map[string]bool{}
	for _, cat := range c.Categories {
		if cat.ID == "" || cat.Name == "" {
			t.Errorf("category missing id/name: %+v", cat)
		}
		for _, e := range cat.Entries {
			if e.ID == "" {
				t.Errorf("entry missing id in %s", cat.ID)
			}
			if seenIDs[e.ID] {
				t.Errorf("duplicate entry id: %s", e.ID)
			}
			seenIDs[e.ID] = true
			if e.OSFamily != "linux" && e.OSFamily != "windows" {
				t.Errorf("entry %s: bad os_family %q", e.ID, e.OSFamily)
			}
			if e.Arch != "amd64" && e.Arch != "arm64" {
				t.Errorf("entry %s: bad arch %q", e.ID, e.Arch)
			}
		}
	}
}
