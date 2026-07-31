// GET /api/docs — a self-contained API reference rendered from the
// embedded OpenAPI spec. No CDN (the deploy server may be air-gapped on
// a tailnet), so instead of pulling Swagger-UI we parse the spec at
// startup and emit a compact static reference: grouped by tag, each
// operation showing method, path, summary, and the RBAC note. The
// machine-readable spec lives at /api/openapi.yaml for real tooling
// (openapi-generator, Postman import, etc.).

package main

import (
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/your-org/deployserver/api/internal/apispec"
)

type specOp struct {
	Tags        []string `yaml:"tags"`
	Summary     string   `yaml:"summary"`
	Description string   `yaml:"description"`
}

type specDoc struct {
	Info struct {
		Title       string `yaml:"title"`
		Version     string `yaml:"version"`
		Description string `yaml:"description"`
	} `yaml:"info"`
	// Each path maps keys (http methods AND "parameters") to nodes; we
	// decode only the method keys into specOp — "parameters" is a
	// sequence and would fail a struct decode.
	Paths map[string]map[string]yaml.Node `yaml:"paths"`
}

var httpMethods = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true, "delete": true,
}

func (h *handlers) apiDocs(w http.ResponseWriter, _ *http.Request) {
	var doc specDoc
	if err := yaml.Unmarshal(apispec.YAML(), &doc); err != nil {
		http.Error(w, "spec parse: "+err.Error(), http.StatusInternalServerError)
		return
	}

	type op struct{ method, path, summary, desc string }
	byTag := map[string][]op{}
	var tagOrder []string
	seen := map[string]bool{}
	for path, methods := range doc.Paths {
		for method, node := range methods {
			if !httpMethods[method] {
				continue
			}
			var o specOp
			if err := node.Decode(&o); err != nil {
				continue
			}
			tag := "other"
			if len(o.Tags) > 0 {
				tag = o.Tags[0]
			}
			if !seen[tag] {
				seen[tag] = true
				tagOrder = append(tagOrder, tag)
			}
			byTag[tag] = append(byTag[tag], op{strings.ToUpper(method), path, o.Summary, o.Description})
		}
	}
	sort.Strings(tagOrder)
	for _, t := range tagOrder {
		sort.Slice(byTag[t], func(i, j int) bool {
			if byTag[t][i].path != byTag[t][j].path {
				return byTag[t][i].path < byTag[t][j].path
			}
			return byTag[t][i].method < byTag[t][j].method
		})
	}

	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	fmt.Fprintf(&b, "<title>%s</title>", html.EscapeString(doc.Info.Title))
	b.WriteString(`<style>
:root{--bg:#0d1117;--panel:#161b22;--line:#2a3442;--ink:#c9d1d9;--ink2:#8a94a6;--accent:#4493e6;
--get:#3fb950;--post:#4493e6;--put:#d29922;--patch:#d29922;--delete:#f85149;}
@media(prefers-color-scheme:light){:root{--bg:#f3f5f8;--panel:#fff;--line:#d8dee8;--ink:#1a2230;--ink2:#5a6675;}}
*{box-sizing:border-box}body{background:var(--bg);color:var(--ink);margin:0;
font:15px/1.55 -apple-system,Segoe UI,Roboto,sans-serif;padding:32px 20px 80px}
main{max-width:900px;margin:0 auto}h1{font-size:26px;margin:0 0 4px}
.sub{color:var(--ink2);margin:0 0 6px}.desc{color:var(--ink2);max-width:70ch;margin:0 0 24px}
.dl{margin:0 0 28px;font-size:13px}.dl a{color:var(--accent)}
h2{font-size:13px;text-transform:uppercase;letter-spacing:.09em;color:var(--ink2);
margin:26px 0 10px;border-top:1px solid var(--line);padding-top:18px}
.op{display:grid;grid-template-columns:64px 1fr;gap:12px;align-items:baseline;
background:var(--panel);border:1px solid var(--line);border-radius:8px;padding:10px 14px;margin-bottom:8px}
.m{font:700 11px/1 ui-monospace,Menlo,monospace;letter-spacing:.04em;text-align:center;
padding:5px 6px;border-radius:5px;color:#fff}
.GET{background:var(--get)}.POST{background:var(--post)}.PUT{background:var(--put)}
.PATCH{background:var(--patch)}.DELETE{background:var(--delete)}
.p{font-family:ui-monospace,Menlo,monospace;font-size:13px}
.s{color:var(--ink);display:block;margin-top:2px}.rbac{color:var(--ink2);font-size:12px}
code{background:var(--bg);border:1px solid var(--line);border-radius:4px;padding:0 5px;font-size:.85em}
</style></head><body><main>`)
	fmt.Fprintf(&b, "<h1>%s</h1><p class=sub>v%s</p><p class=desc>%s</p>",
		html.EscapeString(doc.Info.Title), html.EscapeString(doc.Info.Version),
		html.EscapeString(doc.Info.Description))
	b.WriteString(`<p class=dl>Machine-readable spec: <a href="/api/openapi.yaml">/api/openapi.yaml</a> · ` +
		`generate a client with <code>openapi-generator</code>, or sign in with <code>deployctl auth login</code>.</p>`)

	for _, tag := range tagOrder {
		fmt.Fprintf(&b, "<h2>%s</h2>", html.EscapeString(tag))
		for _, o := range byTag[tag] {
			rbac := extractRBAC(o.desc)
			fmt.Fprintf(&b, `<div class=op><span class="m %s">%s</span><div><span class=p>%s</span>`+
				`<span class=s>%s</span>%s</div></div>`,
				o.method, o.method, html.EscapeString(o.path), html.EscapeString(o.summary), rbac)
		}
	}
	b.WriteString(`</main></body></html>`)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// extractRBAC pulls the "Requires `perm`." note out of a description
// into a small labelled line. Permission names contain dots
// (audit.read, machine.write), so the clause ends at a period that is
// followed by a space or end-of-string, not the first period.
func extractRBAC(desc string) string {
	i := strings.Index(desc, "Requires ")
	if i < 0 {
		return ""
	}
	clause := desc[i:]
	for j := 0; j < len(clause); j++ {
		if clause[j] == '.' && (j+1 == len(clause) || clause[j+1] == ' ') {
			clause = clause[:j]
			break
		}
	}
	return `<span class=rbac>` + html.EscapeString(clause) + `.</span>`
}
