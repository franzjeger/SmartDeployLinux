package provider

import (
	"context"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/your-org/deployserver/sdk"
)

func TestProviderSchema(t *testing.T) {
	p := New("test")()
	var resp provider.SchemaResponse
	p.Schema(context.Background(), provider.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("provider schema diagnostics: %v", resp.Diagnostics)
	}
	for _, want := range []string{"endpoint", "token"} {
		if _, ok := resp.Schema.Attributes[want]; !ok {
			t.Errorf("provider schema missing %q", want)
		}
	}
}

func TestResourceSchemas(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		res   resource.Resource
		attrs []string
	}{
		{"machine", NewMachineResource(), []string{"id", "asset_tag", "created_at"}},
		{"site", NewSiteResource(), []string{"name", "mirror_base_url", "created_at"}},
		{"api_token", NewAPITokenResource(), []string{"id", "name", "roles", "token", "scope_roles"}},
	}
	for _, tc := range cases {
		var resp resource.SchemaResponse
		tc.res.Schema(ctx, resource.SchemaRequest{}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("%s schema diagnostics: %v", tc.name, resp.Diagnostics)
		}
		for _, a := range tc.attrs {
			if _, ok := resp.Schema.Attributes[a]; !ok {
				t.Errorf("%s schema missing %q", tc.name, a)
			}
		}
	}

	// api_token secret must be marked sensitive.
	var tokResp resource.SchemaResponse
	NewAPITokenResource().Schema(ctx, resource.SchemaRequest{}, &tokResp)
	if !tokResp.Schema.Attributes["token"].IsSensitive() {
		t.Error("api_token `token` attribute must be sensitive")
	}
}

func TestDataSourceSchema(t *testing.T) {
	var resp datasource.SchemaResponse
	NewMachinesDataSource().Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("machines data source diagnostics: %v", resp.Diagnostics)
	}
	if _, ok := resp.Schema.Attributes["machines"]; !ok {
		t.Error("machines data source missing `machines`")
	}
}

func TestMachineToModel(t *testing.T) {
	tag := "lab-01"
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := machineToModel(&sdk.Machine{ID: "m1", AssetTag: &tag, CreatedAt: created})
	if m.ID.ValueString() != "m1" {
		t.Errorf("id: %q", m.ID.ValueString())
	}
	if m.AssetTag.ValueString() != "lab-01" {
		t.Errorf("asset_tag: %q", m.AssetTag.ValueString())
	}
	if !m.MACPrimary.IsNull() {
		t.Error("nil MAC pointer should map to Terraform null")
	}
	if m.CreatedAt.ValueString() != "2026-01-02T03:04:05Z" {
		t.Errorf("created_at: %q", m.CreatedAt.ValueString())
	}
}

func TestAPITokenFromAPI(t *testing.T) {
	created := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	tok := &sdk.APIToken{ID: "t1", Name: "ci", Prefix: "dpsk_ab12cd", ScopeRoles: []string{"operator"}, CreatedAt: created}
	m := apiTokenFromAPI(tok, types.ListNull(types.StringType))
	if m.ID.ValueString() != "t1" || m.Name.ValueString() != "ci" || m.Prefix.ValueString() != "dpsk_ab12cd" {
		t.Fatalf("unexpected mapping: %+v", m)
	}
	var scope []string
	m.ScopeRoles.ElementsAs(context.Background(), &scope, false)
	if len(scope) != 1 || scope[0] != "operator" {
		t.Errorf("scope_roles: %v", scope)
	}
}

func TestHelpers(t *testing.T) {
	if optStr(types.StringNull()) != nil {
		t.Error("null string should be nil pointer")
	}
	if s := optStr(types.StringValue("x")); s == nil || *s != "x" {
		t.Error("value string should be a pointer")
	}
	if optInt(types.Int64Null()) != nil {
		t.Error("null int should be nil pointer")
	}
	if n := optInt(types.Int64Value(30)); n == nil || *n != 30 {
		t.Error("value int should be a pointer")
	}
	lst := stringListValue([]string{"a", "b"})
	var got []string
	lst.ElementsAs(context.Background(), &got, false)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("stringListValue: %v", got)
	}
}
