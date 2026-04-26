// Curated catalog of distro netboot configurations, modeled loosely on
// netboot.xyz. JSON is embedded into the binary at compile time so
// there's no runtime fetch and the operator gets a deterministic set
// per release. Update catalog.json + bump its `version` field to
// publish new entries.

package catalog

import (
	"encoding/json"
	_ "embed"
	"errors"
	"fmt"
)

//go:embed catalog.json
var rawJSON []byte

type Catalog struct {
	Version    string     `json:"version"`
	Doc        string     `json:"doc"`
	Categories []Category `json:"categories"`
}

type Category struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Entries []Entry `json:"entries"`
}

type Entry struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	OSFamily    string                 `json:"os_family"`
	OSVersion   string                 `json:"os_version"`
	Arch        string                 `json:"arch"`
	Description string                 `json:"description"`
	Media       map[string]interface{} `json:"media"`
}

var ErrNotFound = errors.New("catalog entry not found")

// Load returns the parsed catalog. Cheap; the JSON is only ~6 KB and
// re-parsed on each call so callers don't share mutable state.
// In hot paths, cache the result yourself.
func Load() (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(rawJSON, &c); err != nil {
		return nil, fmt.Errorf("parse embedded catalog: %w", err)
	}
	return &c, nil
}

// Lookup returns the entry with the given id, scanning all categories.
func (c *Catalog) Lookup(id string) (*Entry, *Category, error) {
	for _, cat := range c.Categories {
		for i := range cat.Entries {
			if cat.Entries[i].ID == id {
				return &cat.Entries[i], &cat, nil
			}
		}
	}
	return nil, nil, ErrNotFound
}

// Total returns the number of entries across all categories.
func (c *Catalog) Total() int {
	n := 0
	for _, cat := range c.Categories {
		n += len(cat.Entries)
	}
	return n
}
