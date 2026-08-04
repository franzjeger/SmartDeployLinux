package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/your-org/deployserver/sdk"
)

var (
	_ resource.Resource                = (*siteResource)(nil)
	_ resource.ResourceWithConfigure   = (*siteResource)(nil)
	_ resource.ResourceWithImportState = (*siteResource)(nil)
)

func NewSiteResource() resource.Resource { return &siteResource{} }

type siteResource struct {
	client *sdk.Client
}

type siteModel struct {
	Name          types.String `tfsdk:"name"`
	MirrorBaseURL types.String `tfsdk:"mirror_base_url"`
	Description   types.String `tfsdk:"description"`
	CreatedAt     types.String `tfsdk:"created_at"`
}

func (r *siteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site"
}

func (r *siteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A site — a named location with an optional local image mirror.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				MarkdownDescription: "Unique site name (the identifier).",
				Required:            true,
				// The name is the primary key; changing it is a new site.
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"mirror_base_url": schema.StringAttribute{
				MarkdownDescription: "Base URL of the site-local image mirror.",
				Optional:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Free-text description.",
				Optional:            true,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "Creation timestamp (RFC 3339).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *siteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProvider(req.ProviderData, &resp.Diagnostics)
}

func (r *siteResource) upsert(ctx context.Context, plan siteModel) (*sdk.Site, error) {
	return r.client.UpsertSite(ctx, sdk.SiteInput{
		Name:          plan.Name.ValueString(),
		MirrorBaseURL: optStr(plan.MirrorBaseURL),
		Description:   optStr(plan.Description),
	})
}

func (r *siteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan siteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.upsert(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Create site failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, siteToModel(s))...)
}

func (r *siteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state siteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sites, err := r.client.ListSites(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Read site failed", err.Error())
		return
	}
	name := state.Name.ValueString()
	for i := range sites {
		if sites[i].Name == name {
			resp.Diagnostics.Append(resp.State.Set(ctx, siteToModel(&sites[i]))...)
			return
		}
	}
	resp.State.RemoveResource(ctx) // gone
}

func (r *siteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan siteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.upsert(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Update site failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, siteToModel(s))...)
}

func (r *siteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state siteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteSite(ctx, state.Name.ValueString()); err != nil && !sdk.IsNotFound(err) {
		resp.Diagnostics.AddError("Delete site failed", err.Error())
	}
}

func (r *siteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func siteToModel(s *sdk.Site) siteModel {
	return siteModel{
		Name:          types.StringValue(s.Name),
		MirrorBaseURL: types.StringPointerValue(s.MirrorBaseURL),
		Description:   types.StringPointerValue(s.Description),
		CreatedAt:     types.StringValue(s.CreatedAt.Format(rfc3339)),
	}
}
