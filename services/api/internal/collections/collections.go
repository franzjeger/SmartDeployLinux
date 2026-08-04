// Package collections generates API-client collections (Postman v2.1 and
// Bruno) from the embedded OpenAPI spec. The output is deterministic — the
// same spec always produces byte-identical files — so a test can assert
// the committed collections are exactly what the current spec generates,
// exactly the way the SDKs are kept in lockstep with the contract.
package collections

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// httpMethods is the fixed emit order for methods sharing a path.
var httpMethods = []string{"get", "post", "put", "patch", "delete"}

func methodRank(m string) int {
	for i, x := range httpMethods {
		if x == m {
			return i
		}
	}
	return len(httpMethods)
}

type param struct {
	Name        string
	In          string // path | query
	Value       string
	Description string
	Enabled     bool // query only; path params are always enabled
}

type operation struct {
	Tag, Method, Path string
	Summary, Desc     string
	Path_, Query      []param
	Body              any // example request body, or nil
}

// doc is the parsed spec, kept for $ref resolution.
type doc struct {
	root map[string]any
}

// Generate returns a map of repo-relative output path -> file bytes for
// both the Postman and Bruno collections built from specYAML.
func Generate(specYAML []byte) (map[string][]byte, error) {
	var root map[string]any
	if err := yaml.Unmarshal(specYAML, &root); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	d := &doc{root: root}

	info, _ := root["info"].(map[string]any)
	title := str(info["title"])
	version := str(info["version"])

	ops, tagOrder, err := d.operations()
	if err != nil {
		return nil, err
	}

	files := map[string][]byte{}

	pm, err := postman(title, version, str(info["description"]), ops, tagOrder)
	if err != nil {
		return nil, err
	}
	files["clients/postman/deployserver.postman_collection.json"] = pm
	files["clients/postman/deployserver.postman_environment.json"] = postmanEnv()

	for path, body := range bruno(title, ops, tagOrder) {
		files[path] = body
	}
	return files, nil
}

// operations flattens the spec's paths into a deterministically ordered
// slice, and returns the tag order declared in the spec (for folders).
func (d *doc) operations() ([]operation, []string, error) {
	paths, _ := d.root["paths"].(map[string]any)

	var tagOrder []string
	if tags, ok := d.root["tags"].([]any); ok {
		for _, t := range tags {
			if m, ok := t.(map[string]any); ok {
				tagOrder = append(tagOrder, str(m["name"]))
			}
		}
	}

	var ops []operation
	for rawPath, pv := range paths {
		pathItem, ok := pv.(map[string]any)
		if !ok {
			continue
		}
		// Path-level parameters apply to every operation on the path.
		pathLevelParams := d.params(pathItem["parameters"])
		for _, method := range httpMethods {
			ov, ok := pathItem[method].(map[string]any)
			if !ok {
				continue
			}
			op := operation{
				Method:  strings.ToUpper(method),
				Path:    rawPath,
				Summary: str(ov["summary"]),
				Desc:    strings.TrimSpace(str(ov["description"])),
			}
			if tags, ok := ov["tags"].([]any); ok && len(tags) > 0 {
				op.Tag = str(tags[0])
			}
			all := append(append([]param{}, pathLevelParams...), d.params(ov["parameters"])...)
			for _, p := range all {
				if p.In == "path" {
					op.Path_ = append(op.Path_, p)
				} else if p.In == "query" {
					op.Query = append(op.Query, p)
				}
			}
			op.Body = d.requestBodyExample(ov["requestBody"])
			ops = append(ops, op)
		}
	}

	tagIdx := map[string]int{}
	for i, t := range tagOrder {
		tagIdx[t] = i
	}
	sort.SliceStable(ops, func(i, j int) bool {
		ti, tj := tagIdx[ops[i].Tag], tagIdx[ops[j].Tag]
		if ti != tj {
			return ti < tj
		}
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return methodRank(strings.ToLower(ops[i].Method)) < methodRank(strings.ToLower(ops[j].Method))
	})
	return ops, tagOrder, nil
}

// params resolves an operation/path "parameters" node into path+query
// params, following $ref into components/parameters.
func (d *doc) params(node any) []param {
	list, ok := node.([]any)
	if !ok {
		return nil
	}
	var out []param
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		m = d.deref(m, nil)
		in := str(m["in"])
		if in != "path" && in != "query" {
			continue
		}
		schema, _ := m["schema"].(map[string]any)
		p := param{
			Name:        str(m["name"]),
			In:          in,
			Description: strings.TrimSpace(str(m["description"]) + " " + schemaDesc(schema)),
			Value:       paramValue(schema, in),
			Enabled:     in == "path" || hasKey(schema, "default"),
		}
		p.Description = strings.TrimSpace(p.Description)
		out = append(out, p)
	}
	return out
}

// requestBodyExample resolves a requestBody's application/json schema into
// a fillable example value (nil when there is no JSON body).
func (d *doc) requestBodyExample(node any) any {
	rb, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	content, _ := rb["content"].(map[string]any)
	appJSON, _ := content["application/json"].(map[string]any)
	schema, _ := appJSON["schema"].(map[string]any)
	if schema == nil {
		return nil
	}
	return d.example(schema, map[string]bool{})
}

// example builds a placeholder value for a schema, resolving $ref, allOf,
// arrays and nested objects. seen guards against recursive schemas.
func (d *doc) example(schema map[string]any, seen map[string]bool) any {
	schema = d.deref(schema, seen)
	if schema == nil {
		return nil
	}
	if v, ok := schema["example"]; ok {
		return v
	}
	if v, ok := schema["default"]; ok {
		return v
	}
	if allOf, ok := schema["allOf"].([]any); ok {
		merged := map[string]any{}
		for _, sub := range allOf {
			if sm, ok := sub.(map[string]any); ok {
				sm = d.deref(sm, seen)
				if props, ok := sm["properties"].(map[string]any); ok {
					for k, v := range props {
						merged[k] = v
					}
				}
			}
		}
		return d.exampleObject(merged, seen)
	}
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}
	switch typeOf(schema) {
	case "object":
		props, _ := schema["properties"].(map[string]any)
		return d.exampleObject(props, seen)
	case "array":
		items, _ := schema["items"].(map[string]any)
		return []any{d.example(items, seen)}
	case "integer", "number":
		return 0
	case "boolean":
		return false
	case "string":
		return stringPlaceholder(schema)
	default:
		if props, ok := schema["properties"].(map[string]any); ok {
			return d.exampleObject(props, seen)
		}
		return "<any>"
	}
}

func (d *doc) exampleObject(props map[string]any, seen map[string]bool) any {
	obj := map[string]any{}
	for k, v := range props {
		if sm, ok := v.(map[string]any); ok {
			obj[k] = d.example(sm, seen)
		} else {
			obj[k] = nil
		}
	}
	return obj
}

// deref follows a single $ref (recording it in seen to break cycles).
func (d *doc) deref(node map[string]any, seen map[string]bool) map[string]any {
	ref, ok := node["$ref"].(string)
	if !ok {
		return node
	}
	if seen != nil {
		if seen[ref] {
			return map[string]any{"type": "object"} // cycle guard
		}
		seen[ref] = true
	}
	cur := any(d.root)
	for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[seg]
	}
	if m, ok := cur.(map[string]any); ok {
		return d.deref(m, seen)
	}
	return nil
}

// ---- helpers --------------------------------------------------------

func typeOf(schema map[string]any) string {
	switch t := schema["type"].(type) {
	case string:
		return t
	case []any: // OpenAPI 3.1 nullable, e.g. [string, "null"]
		for _, x := range t {
			if s, ok := x.(string); ok && s != "null" {
				return s
			}
		}
	}
	return ""
}

func stringPlaceholder(schema map[string]any) string {
	switch str(schema["format"]) {
	case "uuid":
		return "<uuid>"
	case "date-time":
		return "<date-time>"
	}
	return "<string>"
}

func paramValue(schema map[string]any, in string) string {
	if schema == nil {
		if in == "path" {
			return "<value>"
		}
		return ""
	}
	if def, ok := schema["default"]; ok {
		return str(def)
	}
	if in == "path" {
		return strings.Trim(stringPlaceholder(schema), "<>")
	}
	return ""
}

func schemaDesc(schema map[string]any) string {
	if schema == nil {
		return ""
	}
	return str(schema["description"])
}

func hasKey(m map[string]any, k string) bool {
	if m == nil {
		return false
	}
	_, ok := m[k]
	return ok
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		return fmt.Sprint(x)
	}
}

// jsonIndent marshals v as 2-space-indented JSON with a trailing newline,
// deterministically (map keys sorted by encoding/json).
func jsonIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
