// deployctl — operator CLI for deployserver.
//
// Subcommands:
//   machines list
//   machines create --asset-tag X [--mac Y] [--vendor V] [--model M]
//   machines get <id>
//   deployments issue --machine <id> --profile <id> [--ttl 24h] [--label L] [--binding-cidr CIDR]
//   audit query [--since 1h] [--action PATTERN]
//   bootstrap-sticks register --image-sha X --tailnet T --deploy-url U --ca-fingerprint F [--label L]
//
// Auth: requires env DEPLOY_API_URL and DEPLOY_API_TOKEN.
// The token is the OIDC ID token from your IdP. For dev where OIDC isn't
// configured, set DEPLOY_API_INSECURE_SKIP_VERIFY=1 and any non-empty
// DEPLOY_API_TOKEN.

package main

import (
	"flag"
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "machines":
		machinesMain(os.Args[2:])
	case "deployments":
		deploymentsMain(os.Args[2:])
	case "audit":
		auditMain(os.Args[2:])
	case "bootstrap-sticks":
		sticksMain(os.Args[2:])
	case "images":
		imagesMain(os.Args[2:])
	case "auth":
		authMain(os.Args[2:])
	case "api-tokens":
		apiTokensMain(os.Args[2:])
	case "version":
		fmt.Println(version)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `deployctl — operator CLI for deployserver

Usage:
  deployctl <group> <command> [flags]

Groups:
  machines           Register and inspect target machines
  deployments        Issue deployment codes
  audit              Query the audit log
  bootstrap-sticks   Inventory of physical bootstrap USB sticks
  images             List images, upload new image versions
  auth               OIDC device-flow login for the CLI
  api-tokens         Create/list/revoke long-lived API tokens

Environment:
  DEPLOY_API_URL                      e.g. https://deploy.example.com
  DEPLOY_API_TOKEN                    OIDC ID token or service bearer
  DEPLOY_API_INSECURE_SKIP_VERIFY=1   skip TLS verify (dev only)

Examples:
  deployctl machines list
  deployctl machines create --asset-tag lab-01 --mac aa:bb:cc:dd:ee:ff
  deployctl deployments issue --machine <uuid> --profile <uuid> --ttl 4h
  deployctl audit query --since 24h
`)
}

func parseFlags(args []string, fs *flag.FlagSet) []string {
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	return fs.Args()
}
