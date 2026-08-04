package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"

	sdk "github.com/your-org/deployserver/sdk"
)

var (
	_ datasource.DataSource              = (*machinesDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*machinesDataSource)(nil)
)

func NewMachinesDataSource() datasource.DataSource { return &machinesDataSource{} }

type machinesDataSource struct {
	client *sdk.Client
}

type machinesDataSourceModel struct {
	Machines []machineModel `tfsdk:"machines"`
}

func (d *machinesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_machines"
}

func (d *machinesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "All machines in the deployserver inventory.",
		Attributes: map[string]dschema.Attribute{
			"machines": dschema.ListNestedAttribute{
				MarkdownDescription: "The machines, newest first.",
				Computed:            true,
				NestedObject: dschema.NestedAttributeObject{
					Attributes: map[string]dschema.Attribute{
						"id":                 dschema.StringAttribute{Computed: true},
						"asset_tag":          dschema.StringAttribute{Computed: true},
						"mac_primary":        dschema.StringAttribute{Computed: true},
						"vendor":             dschema.StringAttribute{Computed: true},
						"model":              dschema.StringAttribute{Computed: true},
						"default_profile_id": dschema.StringAttribute{Computed: true},
						"created_at":         dschema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (d *machinesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProvider(req.ProviderData, &resp.Diagnostics)
}

func (d *machinesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	machines, err := d.client.ListMachines(ctx)
	if err != nil {
		resp.Diagnostics.AddError("List machines failed", err.Error())
		return
	}
	var state machinesDataSourceModel
	for i := range machines {
		state.Machines = append(state.Machines, machineToModel(&machines[i]))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}
