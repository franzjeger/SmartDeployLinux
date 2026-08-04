package collections

import (
	"fmt"
	"sort"
	"strings"
)

// bruno emits a Bruno collection (folder-per-tag, one .bru file per
// operation) plus bruno.json and a local environment. Bruno's .bru files
// are a small text DSL — see https://docs.usebruno.com.
const brunoRoot = "clients/bruno/deployserver/"

func bruno(title string, ops []operation, tagOrder []string) map[string][]byte {
	files := map[string][]byte{}

	files[brunoRoot+"bruno.json"] = []byte(`{
  "version": "1",
  "name": "` + title + `",
  "type": "collection",
  "ignore": [
    "node_modules",
    ".git"
  ]
}
`)

	files[brunoRoot+"environments/local.bru"] = []byte(`vars {
  baseUrl: https://deploy.example.com
  token:
}
`)

	seqByTag := map[string]int{}
	// Emit in tag order so seq numbers are stable and grouped.
	tagIdx := map[string]int{}
	for i, t := range tagOrder {
		tagIdx[t] = i
	}
	sorted := append([]operation{}, ops...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if tagIdx[sorted[i].Tag] != tagIdx[sorted[j].Tag] {
			return tagIdx[sorted[i].Tag] < tagIdx[sorted[j].Tag]
		}
		return false // ops already globally sorted; keep relative order
	})

	for _, op := range sorted {
		seqByTag[op.Tag]++
		path := fmt.Sprintf("%s%s/%s.bru", brunoRoot, op.Tag, bruSlug(op))
		files[path] = []byte(bruFile(op, seqByTag[op.Tag]))
	}
	return files
}

func bruFile(op operation, seq int) string {
	name := op.Summary
	if name == "" {
		name = op.Method + " " + op.Path
	}

	var b strings.Builder
	fmt.Fprintf(&b, "meta {\n  name: %s\n  type: http\n  seq: %d\n}\n\n", name, seq)

	// Method block. url includes enabled query params so it is runnable.
	url := "{{baseUrl}}" + bruPath(op.Path)
	var enabledQ []string
	for _, q := range op.Query {
		if q.Enabled {
			enabledQ = append(enabledQ, q.Name+"="+q.Value)
		}
	}
	if len(enabledQ) > 0 {
		url += "?" + strings.Join(enabledQ, "&")
	}
	bodyKind := "none"
	if op.Body != nil {
		bodyKind = "json"
	}
	fmt.Fprintf(&b, "%s {\n  url: %s\n  body: %s\n  auth: bearer\n}\n\n",
		strings.ToLower(op.Method), url, bodyKind)

	b.WriteString("auth:bearer {\n  token: {{token}}\n}\n\n")

	if len(op.Path_) > 0 {
		b.WriteString("params:path {\n")
		for _, p := range op.Path_ {
			fmt.Fprintf(&b, "  %s: %s\n", p.Name, p.Value)
		}
		b.WriteString("}\n\n")
	}

	if len(op.Query) > 0 {
		b.WriteString("params:query {\n")
		for _, q := range op.Query {
			if q.Enabled {
				fmt.Fprintf(&b, "  %s: %s\n", q.Name, q.Value)
			} else {
				fmt.Fprintf(&b, "  ~%s: %s\n", q.Name, q.Value)
			}
		}
		b.WriteString("}\n\n")
	}

	b.WriteString("headers {\n")
	if op.Body != nil {
		b.WriteString("  Content-Type: application/json\n")
	}
	b.WriteString("  Accept: application/json\n}\n\n")

	if op.Body != nil {
		raw, _ := jsonIndent(op.Body)
		body := strings.TrimRight(string(raw), "\n")
		b.WriteString("body:json {\n")
		for _, line := range strings.Split(body, "\n") {
			b.WriteString("  " + line + "\n")
		}
		b.WriteString("}\n\n")
	}

	if op.Desc != "" {
		b.WriteString("docs {\n")
		for _, line := range strings.Split(op.Desc, "\n") {
			b.WriteString("  " + strings.TrimRight(line, " ") + "\n")
		}
		b.WriteString("}\n")
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}

// bruPath turns "/api/v1/machines/{id}" into "/api/v1/machines/:id".
func bruPath(path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			segs[i] = ":" + strings.Trim(s, "{}")
		}
	}
	return strings.Join(segs, "/")
}

// bruSlug builds a stable, filesystem-safe file name from method+path,
// e.g. GET /api/v1/machines/{id}/wake -> "get-machines-id-wake".
func bruSlug(op operation) string {
	p := strings.TrimPrefix(op.Path, "/api/v1/")
	p = strings.NewReplacer("{", "", "}", "", "/", "-", ".", "-").Replace(p)
	return strings.ToLower(op.Method) + "-" + p
}
