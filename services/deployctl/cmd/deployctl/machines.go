package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/your-org/deployserver/deployctl/internal/client"
)

func machinesMain(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "machines: subcommand required (list|create|get)")
		os.Exit(2)
	}
	switch args[0] {
	case "list":
		machinesList(args[1:])
	case "create":
		machinesCreate(args[1:])
	case "get":
		machinesGet(args[1:])
	case "wake":
		machinesWake(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "machines: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

// machinesWake queues a wake-on-LAN request for a machine.
// Usage: deployctl machines wake <machine-id> [--at RFC3339] [--site S]
func machinesWake(args []string) {
	fs := flag.NewFlagSet("machines wake", flag.ExitOnError)
	at := fs.String("at", "", "schedule time (RFC3339); empty = next agent poll")
	site := fs.String("site", "", "site override (default: machine's site attribute)")
	if len(args) == 0 || args[0] == "" || args[0][0] == '-' {
		fmt.Fprintln(os.Stderr, "usage: deployctl machines wake <machine-id> [--at RFC3339] [--site S]")
		os.Exit(2)
	}
	id := args[0]
	_ = fs.Parse(args[1:])

	c, err := client.New()
	if err != nil {
		die(err)
	}
	body := map[string]any{}
	if *at != "" {
		body["at"] = *at
	}
	if *site != "" {
		body["site"] = *site
	}
	var out struct {
		WakeID string `json:"wake_id"`
		Site   string `json:"site"`
	}
	if err := c.Do("POST", "/api/v1/machines/"+id+"/wake", body, &out); err != nil {
		die(err)
	}
	fmt.Printf("wake %s queued for site %q\n", out.WakeID, out.Site)
}

func machinesList(args []string) {
	_ = parseFlags(args, flag.NewFlagSet("machines list", flag.ExitOnError))
	c, err := client.New()
	if err != nil {
		die(err)
	}
	var out []map[string]any
	if err := c.Do("GET", "/api/v1/machines", nil, &out); err != nil {
		die(err)
	}
	printJSON(out)
}

func machinesCreate(args []string) {
	fs := flag.NewFlagSet("machines create", flag.ExitOnError)
	asset := fs.String("asset-tag", "", "asset tag (UNIQUE)")
	mac := fs.String("mac", "", "primary MAC address")
	vendor := fs.String("vendor", "", "DMI vendor")
	model := fs.String("model", "", "DMI model")
	defaultProfile := fs.String("default-profile", "", "default deployment profile UUID")
	parseFlags(args, fs)

	body := map[string]any{}
	if *asset != "" {
		body["asset_tag"] = *asset
	}
	if *mac != "" {
		body["mac_primary"] = *mac
	}
	if *vendor != "" {
		body["vendor"] = *vendor
	}
	if *model != "" {
		body["model"] = *model
	}
	if *defaultProfile != "" {
		body["default_profile_id"] = *defaultProfile
	}
	c, err := client.New()
	if err != nil {
		die(err)
	}
	var out map[string]any
	if err := c.Do("POST", "/api/v1/machines", body, &out); err != nil {
		die(err)
	}
	printJSON(out)
}

func machinesGet(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "machines get: <id> required")
		os.Exit(2)
	}
	id := args[0]
	c, err := client.New()
	if err != nil {
		die(err)
	}
	var out map[string]any
	if err := c.Do("GET", "/api/v1/machines/"+id, nil, &out); err != nil {
		die(err)
	}
	printJSON(out)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
