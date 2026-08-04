package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/your-org/deployserver/sdk"
)

// Ensure deployserverProvider satisfies the provider.Provider interface.
var _ provider.Provider = (*deployserverProvider)(nil)

type deployserverProvider struct {
	version string
}

// New returns the provider factory Terraform's plugin server expects.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &deployserverProvider{version: version}
	}
}

type providerModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	Token    types.String `tfsdk:"token"`
}

func (p *deployserverProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "deployserver"
	resp.Version = p.version
}

func (p *deployserverProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage a deployserver deployment (machines, sites, API tokens) declaratively. " +
			"Reach the server over your tailnet.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Base URL of the deployserver API, e.g. `https://deploy.example.com`. " +
					"Falls back to the `DEPLOY_API_URL` environment variable.",
				Optional: true,
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "Bearer token (an API token from `deployctl api-tokens create`, or an OIDC " +
					"ID token). Falls back to the `DEPLOY_API_TOKEN` environment variable.",
				Optional:  true,
				Sensitive: true,
			},
		},
	}
}

func (p *deployserverProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := os.Getenv("DEPLOY_API_URL")
	if !cfg.Endpoint.IsNull() {
		endpoint = cfg.Endpoint.ValueString()
	}
	token := os.Getenv("DEPLOY_API_TOKEN")
	if !cfg.Token.IsNull() {
		token = cfg.Token.ValueString()
	}
	if endpoint == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("endpoint"),
			"Missing API endpoint",
			"Set the provider `endpoint` argument or the DEPLOY_API_URL environment variable.",
		)
		return
	}

	client, err := sdk.New(sdk.Options{BaseURL: endpoint, Token: token})
	if err != nil {
		resp.Diagnostics.AddError("Unable to create deployserver client", err.Error())
		return
	}
	resp.ResourceData = client
	resp.DataSourceData = client
}

func (p *deployserverProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewMachineResource,
		NewSiteResource,
		NewAPITokenResource,
	}
}

func (p *deployserverProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewMachinesDataSource,
	}
}
