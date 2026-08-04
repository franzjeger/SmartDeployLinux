// terraform-provider-deployserver is the Terraform provider for the
// deployserver operator API. It is a thin, typed layer over the project's
// Go SDK (services/sdk) — which is itself kept in lockstep with the
// OpenAPI contract — so the provider inherits that same source of truth.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/your-org/deployserver/terraform-provider-deployserver/internal/provider"
)

// version is set at build time via -ldflags; "dev" for local builds.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run with support for a debugger")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/your-org/deployserver",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
