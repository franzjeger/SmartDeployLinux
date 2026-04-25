package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/your-org/deployserver/deployctl/internal/client"
)

func deploymentsMain(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "deployments: subcommand required (issue)")
		os.Exit(2)
	}
	switch args[0] {
	case "issue":
		deploymentsIssue(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "deployments: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

func deploymentsIssue(args []string) {
	fs := flag.NewFlagSet("deployments issue", flag.ExitOnError)
	machine := fs.String("machine", "", "machine UUID (required)")
	profile := fs.String("profile", "", "deployment profile UUID (required)")
	ttl := fs.Int("ttl-seconds", 86400, "code TTL in seconds (max 7d)")
	label := fs.String("label", "", "free-text label, e.g. 'sent to alice@branch'")
	bindingCIDR := fs.String("binding-cidr", "", "restrict redeem to CIDR")
	parseFlags(args, fs)

	if *machine == "" || *profile == "" {
		fmt.Fprintln(os.Stderr, "--machine and --profile required")
		os.Exit(2)
	}

	body := map[string]any{
		"machine_id":  *machine,
		"profile_id":  *profile,
		"ttl_seconds": *ttl,
	}
	if *label != "" {
		body["issued_for"] = *label
	}
	if *bindingCIDR != "" {
		body["binding_cidr"] = *bindingCIDR
	}

	// The auth-broker is the issuer. The CLI hits the API which forwards.
	// In v1 we hit the broker directly via the public Caddy route.
	c, err := client.New()
	if err != nil {
		die(err)
	}
	var out struct {
		Code      string `json:"code"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := c.Do("POST", "/api/v1/bootstrap/issue-code", body, &out); err != nil {
		die(err)
	}
	fmt.Printf("Code:        %s\nExpires:     %s\n", out.Code, out.ExpiresAt)
	fmt.Println()
	fmt.Println("Hand the code to the onsite operator. They type it on the bootstrap stick's prompt.")
	fmt.Println("Single-use; expires automatically.")
}
