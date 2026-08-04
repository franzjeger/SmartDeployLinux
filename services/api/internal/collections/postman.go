package collections

import (
	"strings"
)

// Postman Collection Format v2.1 (https://schema.getpostman.com).

type pmCollection struct {
	Info     pmInfo   `json:"info"`
	Item     []pmItem `json:"item"`
	Auth     *pmAuth  `json:"auth,omitempty"`
	Variable []pmVar  `json:"variable"`
}

type pmInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Schema      string `json:"schema"`
}

type pmItem struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Item        []pmItem   `json:"item,omitempty"`    // folder
	Request     *pmRequest `json:"request,omitempty"` // leaf
}

type pmRequest struct {
	Method      string     `json:"method"`
	Header      []pmHeader `json:"header"`
	Body        *pmBody    `json:"body,omitempty"`
	URL         pmURL      `json:"url"`
	Description string     `json:"description,omitempty"`
}

type pmHeader struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type,omitempty"`
}

type pmBody struct {
	Mode    string     `json:"mode"`
	Raw     string     `json:"raw"`
	Options pmBodyOpts `json:"options"`
}

type pmBodyOpts struct {
	Raw struct {
		Language string `json:"language"`
	} `json:"raw"`
}

type pmURL struct {
	Raw      string    `json:"raw"`
	Host     []string  `json:"host"`
	Path     []string  `json:"path"`
	Query    []pmQuery `json:"query,omitempty"`
	Variable []pmVar   `json:"variable,omitempty"`
}

type pmQuery struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Disabled    bool   `json:"disabled,omitempty"`
	Description string `json:"description,omitempty"`
}

type pmAuth struct {
	Type   string    `json:"type"`
	Bearer []pmAuthP `json:"bearer"`
}

type pmAuthP struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type pmVar struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

func postman(title, version, description string, ops []operation, tagOrder []string) ([]byte, error) {
	col := pmCollection{
		Info: pmInfo{
			Name:        title,
			Description: strings.TrimSpace(description) + "\n\nGenerated from the OpenAPI spec — do not edit by hand.\nSet the `baseUrl` and `token` collection variables (or use the bundled environment).",
			Schema:      "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
		},
		Auth: &pmAuth{
			Type:   "bearer",
			Bearer: []pmAuthP{{Key: "token", Value: "{{token}}", Type: "string"}},
		},
		Variable: []pmVar{
			{Key: "baseUrl", Value: "https://deploy.example.com", Type: "string"},
			{Key: "token", Value: "", Type: "string"},
		},
	}

	// Group operations into folders in the spec's tag order.
	byTag := map[string][]operation{}
	for _, op := range ops {
		byTag[op.Tag] = append(byTag[op.Tag], op)
	}
	for _, tag := range tagOrder {
		group := byTag[tag]
		if len(group) == 0 {
			continue
		}
		folder := pmItem{Name: tag}
		for _, op := range group {
			folder.Item = append(folder.Item, pmRequestItem(op))
		}
		col.Item = append(col.Item, folder)
	}

	return jsonIndent(col)
}

func pmRequestItem(op operation) pmItem {
	segs := pathSegments(op.Path)

	url := pmURL{
		Raw:  "{{baseUrl}}" + op.Path,
		Host: []string{"{{baseUrl}}"},
		Path: segs,
	}
	for _, p := range op.Path_ {
		url.Variable = append(url.Variable, pmVar{Key: p.Name, Value: p.Value, Description: p.Description})
	}
	// Reflect enabled query params in the raw URL so it is copy-pasteable.
	var qs []string
	for _, q := range op.Query {
		url.Query = append(url.Query, pmQuery{
			Key: q.Name, Value: q.Value, Disabled: !q.Enabled, Description: q.Description,
		})
		if q.Enabled {
			qs = append(qs, q.Name+"="+q.Value)
		}
	}
	if len(qs) > 0 {
		url.Raw += "?" + strings.Join(qs, "&")
	}

	req := &pmRequest{
		Method:      op.Method,
		Header:      []pmHeader{{Key: "Accept", Value: "application/json"}},
		URL:         url,
		Description: op.Desc,
	}
	if op.Body != nil {
		raw, _ := jsonIndent(op.Body)
		body := &pmBody{Mode: "raw", Raw: strings.TrimRight(string(raw), "\n")}
		body.Options.Raw.Language = "json"
		req.Body = body
		req.Header = append([]pmHeader{{Key: "Content-Type", Value: "application/json"}}, req.Header...)
	}

	name := op.Summary
	if name == "" {
		name = op.Method + " " + op.Path
	}
	return pmItem{Name: name, Request: req}
}

// pathSegments turns "/api/v1/machines/{id}" into
// ["api","v1","machines",":id"] (Postman path-variable syntax).
func pathSegments(path string) []string {
	var out []string
	for _, s := range strings.Split(strings.Trim(path, "/"), "/") {
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			out = append(out, ":"+strings.Trim(s, "{}"))
		} else {
			out = append(out, s)
		}
	}
	return out
}

func postmanEnv() []byte {
	type ev struct {
		Key     string `json:"key"`
		Value   string `json:"value"`
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
	}
	type env struct {
		Name                 string `json:"name"`
		Values               []ev   `json:"values"`
		PostmanVariableScope string `json:"_postman_variable_scope"`
		PostmanExportedUsing string `json:"_postman_exported_using,omitempty"`
	}
	e := env{
		Name: "deployserver (local)",
		Values: []ev{
			{Key: "baseUrl", Value: "https://deploy.example.com", Type: "default", Enabled: true},
			{Key: "token", Value: "", Type: "secret", Enabled: true},
		},
		PostmanVariableScope: "environment",
	}
	b, _ := jsonIndent(e)
	return b
}
