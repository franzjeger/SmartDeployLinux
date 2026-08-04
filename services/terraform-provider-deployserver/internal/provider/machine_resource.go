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
	_ resource.Resource                = (*machineResource)(nil)
	_ resource.ResourceWithConfigure   = (*machineResource)(nil)
	_ resource.ResourceWithImportState = (*machineResource)(nil)
)

func NewMachineResource() resource.Resource { return &machineResource{} }

type machineResource struct {
	client *sdk.Client
}

type machineModel struct {
	ID               types.String `tfsdk:"id"`
	AssetTag         types.String `tfsdk:"asset_tag"`
	MACPrimary       types.String `tfsdk:"mac_primary"`
	Vendor           types.String `tfsdk:"vendor"`
	Model            types.String `tfsdk:"model"`
	DefaultProfileID types.String `tfsdk:"default_profile_id"`
	CreatedAt        types.String `tfsdk:"created_at"`
}

func (r *machineResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_machine"
}

func (r *machineResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A target machine in the deployserver inventory.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Server-assigned machine UUID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"asset_tag": schema.StringAttribute{
				MarkdownDescription: "Unique asset tag.",
				Optional:            true,
			},
			"mac_primary": schema.StringAttribute{
				MarkdownDescription: "Primary MAC address.",
				Optional:            true,
			},
			"vendor": schema.StringAttribute{
				MarkdownDescription: "DMI vendor.",
				Optional:            true,
			},
			"model": schema.StringAttribute{
				MarkdownDescription: "DMI model.",
				Optional:            true,
			},
			"default_profile_id": schema.StringAttribute{
				MarkdownDescription: "Default deployment profile UUID.",
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

func (r *machineResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProvider(req.ProviderData, &resp.Diagnostics)
}

func (r *machineResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan machineModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := sdk.CreateMachineInput{
		AssetTag:         optStr(plan.AssetTag),
		MACPrimary:       optStr(plan.MACPrimary),
		Vendor:           optStr(plan.Vendor),
		Model:            optStr(plan.Model),
		DefaultProfileID: optStr(plan.DefaultProfileID),
	}
	m, err := r.client.CreateMachine(ctx, in)
	if err != nil {
		resp.Diagnostics.AddError("Create machine failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, machineToModel(m))...)
}

func (r *machineResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state machineModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.client.GetMachine(ctx, state.ID.ValueString())
	if err != nil {
		if sdk.IsNotFound(err) {
			resp.State.RemoveResource(ctx) // drifted away; let TF recreate
			return
		}
		resp.Diagnostics.AddError("Read machine failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, machineToModel(m))...)
}

func (r *machineResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan machineModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := sdk.UpdateMachineInput{
		AssetTag:         optStr(plan.AssetTag),
		MACPrimary:       optStr(plan.MACPrimary),
		Vendor:           optStr(plan.Vendor),
		Model:            optStr(plan.Model),
		DefaultProfileID: optStr(plan.DefaultProfileID),
	}
	m, err := r.client.UpdateMachine(ctx, plan.ID.ValueString(), in)
	if err != nil {
		resp.Diagnostics.AddError("Update machine failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, machineToModel(m))...)
}

func (r *machineResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state machineModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteMachine(ctx, state.ID.ValueString()); err != nil && !sdk.IsNotFound(err) {
		resp.Diagnostics.AddError("Delete machine failed", err.Error())
	}
}

func (r *machineResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// machineToModel maps an SDK machine onto the Terraform model. The server
// serializes the machine struct with Go-style keys and pointer fields;
// null pointers become Terraform null, which round-trips cleanly.
func machineToModel(m *sdk.Machine) machineModel {
	return machineModel{
		ID:               types.StringValue(m.ID),
		AssetTag:         types.StringPointerValue(m.AssetTag),
		MACPrimary:       types.StringPointerValue(m.MACPrimary),
		Vendor:           types.StringPointerValue(m.Vendor),
		Model:            types.StringPointerValue(m.Model),
		DefaultProfileID: types.StringPointerValue(m.DefaultProfileID),
		CreatedAt:        types.StringValue(m.CreatedAt.Format(rfc3339)),
	}
}

const rfc3339 = "2006-01-02T15:04:05Z07:00"
