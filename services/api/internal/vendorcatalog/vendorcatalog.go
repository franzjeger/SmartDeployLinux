// Vendor driver-pack catalogs.
//
// PC vendors publish machine-readable catalogs of their enterprise
// driver packs — the per-model bundles SCCM/MDT shops consume. This
// package fetches, caches and searches them so an operator never has to
// hunt vendor support pages for a download URL.
//
// Lenovo ships first because its catalog is plain XML. Dell and HP wrap
// theirs in CAB archives, which need a pure-Go CAB reader before they
// can join (docs/DRIVERPACK_VENDOR_FETCH.md).
package vendorcatalog

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const lenovoCatalogURL = "https://download.lenovo.com/cdrt/td/catalogv2.xml"

// Entry is one downloadable driver pack, normalized across vendors.
type Entry struct {
	Vendor    string   `json:"vendor"`     // "Lenovo"
	Model     string   `json:"model"`      // "ThinkPad X1 Carbon Gen 9"
	Types     []string `json:"types"`      // Lenovo 4-char machine types; DMI product_name prefixes
	OSFamily  string   `json:"os_family"`  // "windows"
	OSVersion string   `json:"os_version"` // "10 1809", "11 22H2", "10" (catalog version="*")
	URL       string   `json:"url"`
	SHA256    string   `json:"sha256"` // catalog "crc" attribute; verified after download
	Date      string   `json:"date"`
}

// --- Lenovo XML shapes -------------------------------------------------

type lenovoModelList struct {
	Models []lenovoModel `xml:"Model"`
}

type lenovoModel struct {
	Name  string       `xml:"name,attr"`
	Types []string     `xml:"Types>Type"`
	SCCM  []lenovoSCCM `xml:"SCCM"`
}

type lenovoSCCM struct {
	OS      string `xml:"os,attr"`      // win10 | win11
	Version string `xml:"version,attr"` // 1809, 22H2, or "*"
	Date    string `xml:"date,attr"`
	CRC     string `xml:"crc,attr"` // sha256 hex
	URL     string `xml:",chardata"`
}

func parseLenovo(r io.Reader) ([]Entry, error) {
	// The file starts with a UTF-8 BOM; the decoder handles it, but be
	// explicit that we only accept the documented root.
	var list lenovoModelList
	if err := xml.NewDecoder(r).Decode(&list); err != nil {
		return nil, fmt.Errorf("parse lenovo catalog: %w", err)
	}
	var out []Entry
	for _, m := range list.Models {
		for _, p := range m.SCCM {
			osVersion := ""
			switch p.OS {
			case "win10":
				osVersion = "10"
			case "win11":
				osVersion = "11"
			default:
				continue // future OS ids: skip rather than mislabel
			}
			if p.Version != "" && p.Version != "*" {
				osVersion += " " + p.Version
			}
			out = append(out, Entry{
				Vendor:    "Lenovo",
				Model:     m.Name,
				Types:     m.Types,
				OSFamily:  "windows",
				OSVersion: osVersion,
				URL:       strings.TrimSpace(p.URL),
				SHA256:    strings.ToLower(strings.TrimSpace(p.CRC)),
				Date:      p.Date,
			})
		}
	}
	return out, nil
}

// --- cache + search ----------------------------------------------------

// Client fetches and caches vendor catalogs. Safe for concurrent use.
type Client struct {
	HTTP *http.Client
	TTL  time.Duration // cache lifetime; default 24h

	mu      sync.Mutex
	entries []Entry
	fetched time.Time
}

func (c *Client) ttl() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return 24 * time.Hour
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

// Entries returns the cached catalog, refreshing it when stale. A
// refresh failure with a warm cache returns the stale data — a vendor
// CDN hiccup should not take the search feature down.
func (c *Client) Entries(ctx context.Context) ([]Entry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries != nil && time.Since(c.fetched) < c.ttl() {
		return c.entries, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lenovoCatalogURL, nil)
	if err != nil {
		return c.entries, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		if c.entries != nil {
			return c.entries, nil
		}
		return nil, fmt.Errorf("fetch lenovo catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if c.entries != nil {
			return c.entries, nil
		}
		return nil, fmt.Errorf("fetch lenovo catalog: HTTP %d", resp.StatusCode)
	}
	entries, err := parseLenovo(resp.Body)
	if err != nil {
		if c.entries != nil {
			return c.entries, nil
		}
		return nil, err
	}
	c.entries = entries
	c.fetched = time.Now()
	return c.entries, nil
}

// Search filters entries by a free-text query matched against the model
// name and machine types, newest-OS-first. An empty query returns
// nothing (the full catalog is ~4k entries; force some intent).
func Search(entries []Entry, query string) []Entry {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return nil
	}
	var out []Entry
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Model), q) {
			out = append(out, e)
			continue
		}
		for _, t := range e.Types {
			// A full MTM like "20xw0026us" should find its type "20xw".
			tl := strings.ToLower(t)
			if strings.Contains(tl, q) || strings.HasPrefix(q, tl) {
				out = append(out, e)
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].OSVersion > out[j].OSVersion // 11 before 10, 22H2 before 21H2
	})
	return out
}
