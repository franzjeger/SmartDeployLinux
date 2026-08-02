package vendorcatalog

import (
	"strings"
	"testing"
)

// Verbatim excerpt from the real catalogv2.xml (2026-08-02), including
// the UTF-8 BOM the file ships with.
const lenovoSample = "\ufeff" + `
<ModelList version="1.0">
 <Model name="ThinkCentre M715Q 2nd Gen" arch="AMD">
  <Types>
   <Type>10VL</Type>
   <Type>10VN</Type>
  </Types>
  <BIOS version="M1XKT63A" image="m1xk" date="2024-04-25" crc="6f42" md5="19">https://download.lenovo.com/pccbbs/thinkcentre_bios/m1xjy63usa.exe</BIOS>
  <SCCM os="win10" version="1809" date="2020-02-24" crc="82BE1BDDFD4873834D5578E78AE9DB1B96E5360F3CA775037F2B94548B8F9AD9" md5="de22">https://download.lenovo.com/pccbbs/thinkcentre_drivers/tc_m715q_2nd_w1064_1809_201903.exe</SCCM>
 </Model>
 <Model name="ThinkPad X1 Carbon Gen 9" arch="Intel">
  <Types>
   <Type>20XW</Type>
   <Type>20XX</Type>
  </Types>
  <SCCM os="win10" version="*" date="2021-06-01" crc="aa01" md5="bb">https://download.lenovo.com/pccbbs/mobiles/tp_x1c9_w10.exe</SCCM>
  <SCCM os="win11" version="22H2" date="2023-06-27" crc="bfacc1a5" md5="71e1">https://download.lenovo.com/pccbbs/mobiles/tp_x1c9_w11_22h2.exe</SCCM>
  <HSA os="win11" version="21H2" date="2022-05-24" crc="995c" md5="cafd">https://download.lenovo.com/pccbbs/mobiles/hsa.exe</HSA>
 </Model>
</ModelList>`

func parse(t *testing.T) []Entry {
	t.Helper()
	entries, err := parseLenovo(strings.NewReader(lenovoSample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return entries
}

func TestParseLenovo(t *testing.T) {
	entries := parse(t)
	// 3 SCCM entries; BIOS and HSA rows must be ignored.
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d: %+v", len(entries), entries)
	}
	for _, e := range entries {
		if e.Vendor != "Lenovo" || e.OSFamily != "windows" {
			t.Fatalf("bad vendor/os: %+v", e)
		}
		if strings.Contains(e.URL, "bios") || strings.Contains(e.URL, "hsa") {
			t.Fatalf("non-SCCM row leaked through: %+v", e)
		}
	}
}

func TestParseLenovoNormalizesFields(t *testing.T) {
	entries := parse(t)
	byURL := map[string]Entry{}
	for _, e := range entries {
		byURL[e.URL] = e
	}
	m715 := byURL["https://download.lenovo.com/pccbbs/thinkcentre_drivers/tc_m715q_2nd_w1064_1809_201903.exe"]
	if m715.OSVersion != "10 1809" {
		t.Fatalf("os version: %q", m715.OSVersion)
	}
	// The catalog uppercases some crc values; blob sha256 comparison is
	// lowercase-hex, so the parser must normalize.
	if m715.SHA256 != strings.ToLower(m715.SHA256) {
		t.Fatalf("sha not lowercased: %q", m715.SHA256)
	}
	// version="*" means "any build of that OS" → bare major version,
	// which the matcher treats as a prefix of any concrete build.
	wild := byURL["https://download.lenovo.com/pccbbs/mobiles/tp_x1c9_w10.exe"]
	if wild.OSVersion != "10" {
		t.Fatalf("wildcard version: %q", wild.OSVersion)
	}
}

func TestSearch(t *testing.T) {
	entries := parse(t)
	// By model substring, case-insensitive.
	if got := Search(entries, "x1 carbon"); len(got) != 2 {
		t.Fatalf("model search: want 2, got %d", len(got))
	}
	// By machine type.
	if got := Search(entries, "20XW"); len(got) != 2 {
		t.Fatalf("type search: want 2, got %d", len(got))
	}
	// By full MTM as DMI reports it — the query is longer than the type.
	if got := Search(entries, "20XW0026US"); len(got) != 2 {
		t.Fatalf("full MTM search: want 2, got %d", len(got))
	}
	// Newest OS first within a model.
	got := Search(entries, "x1 carbon")
	if got[0].OSVersion != "11 22H2" {
		t.Fatalf("order: first is %q", got[0].OSVersion)
	}
	// Empty query returns nothing, not everything.
	if got := Search(entries, "  "); got != nil {
		t.Fatalf("empty query must return nil")
	}
}
