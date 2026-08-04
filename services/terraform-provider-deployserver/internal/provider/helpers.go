package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	sdk "github.com/your-org/deployserver/sdk"
)

// clientFromProvider pulls the configured *sdk.Client out of the value the
// provider's Configure stashed in ProviderData. It is nil during the first
// (validation) pass, which is expected.
func clientFromProvider(providerData any, diags *diag.Diagnostics) *sdk.Client {
	if providerData == nil {
		return nil
	}
	client, ok := providerData.(*sdk.Client)
	if !ok {
		diags.AddError(
			"Unexpected provider data type",
			"The provider was configured with an unexpected client type; this is a provider bug.",
		)
		return nil
	}
	return client
}

// optStr converts an optional Terraform string into an SDK *string: a null
// or unknown value becomes nil (field omitted), otherwise a pointer.
func optStr(v types.String) *string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	s := v.ValueString()
	return &s
}

// optInt converts an optional Terraform int64 into an SDK *int.
func optInt(v types.Int64) *int {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	n := int(v.ValueInt64())
	return &n
}

// stringListValue turns a Go string slice into a Terraform list value.
func stringListValue(ss []string) types.List {
	elems := make([]attr.Value, len(ss))
	for i, s := range ss {
		elems[i] = types.StringValue(s)
	}
	return types.ListValueMust(types.StringType, elems)
}
