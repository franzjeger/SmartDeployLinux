package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	sdk "github.com/your-org/deployserver/sdk"
)

func apiTokensMain(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "api-tokens: subcommand required (create|list|revoke)")
		os.Exit(2)
	}
	switch args[0] {
	case "create":
		apiTokensCreate(args[1:])
	case "list":
		apiTokensList(args[1:])
	case "revoke":
		apiTokensRevoke(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "api-tokens: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

// apiTokensCreate mints a long-lived token. The secret is printed once (in
// the `token` field) and never recoverable afterward.
func apiTokensCreate(args []string) {
	fs := flag.NewFlagSet("api-tokens create", flag.ExitOnError)
	name := fs.String("name", "", "label for the token (required)")
	days := fs.Int("expires-in-days", 0, "expiry in days (0 = never expires)")
	roles := fs.String("roles", "", "comma-separated roles to scope the token to (must be a subset of your own; default: full access)")
	parseFlags(args, fs)
	if *name == "" {
		fmt.Fprintln(os.Stderr, "api-tokens create: --name required")
		os.Exit(2)
	}
	c, err := newSDK()
	if err != nil {
		die(err)
	}
	in := sdk.CreateAPITokenInput{Name: *name}
	if *days > 0 {
		in.ExpiresInDays = days
	}
	for _, r := range strings.Split(*roles, ",") {
		if r = strings.TrimSpace(r); r != "" {
			in.Roles = append(in.Roles, r)
		}
	}
	out, err := c.CreateAPIToken(context.Background(), in)
	if err != nil {
		die(err)
	}
	fmt.Fprintln(os.Stderr, "Token created. The 'token' field is shown ONLY once — store it now.")
	printJSON(out)
}

func apiTokensList(args []string) {
	parseFlags(args, flag.NewFlagSet("api-tokens list", flag.ExitOnError))
	c, err := newSDK()
	if err != nil {
		die(err)
	}
	out, err := c.ListAPITokens(context.Background())
	if err != nil {
		die(err)
	}
	printJSON(out)
}

func apiTokensRevoke(args []string) {
	if len(args) < 1 || args[0] == "" || args[0][0] == '-' {
		fmt.Fprintln(os.Stderr, "usage: deployctl api-tokens revoke <id>")
		os.Exit(2)
	}
	c, err := newSDK()
	if err != nil {
		die(err)
	}
	if err := c.RevokeAPIToken(context.Background(), args[0]); err != nil {
		die(err)
	}
	fmt.Printf("revoked %s\n", args[0])
}
