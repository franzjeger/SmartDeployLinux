package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	sdk "github.com/your-org/deployserver/sdk"
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

	c, err := newSDK()
	if err != nil {
		die(err)
	}
	out, err := c.WakeMachine(context.Background(), id, sdk.WakeInput{At: *at, Site: *site})
	if err != nil {
		die(machineErr(id, err))
	}
	fmt.Printf("wake %s queued for site %q\n", out.WakeID, out.Site)
}

func machinesList(args []string) {
	_ = parseFlags(args, flag.NewFlagSet("machines list", flag.ExitOnError))
	c, err := newSDK()
	if err != nil {
		die(err)
	}
	out, err := c.ListMachines(context.Background())
	if err != nil {
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

	var in sdk.CreateMachineInput
	if *asset != "" {
		in.AssetTag = asset
	}
	if *mac != "" {
		in.MACPrimary = mac
	}
	if *vendor != "" {
		in.Vendor = vendor
	}
	if *model != "" {
		in.Model = model
	}
	if *defaultProfile != "" {
		in.DefaultProfileID = defaultProfile
	}
	c, err := newSDK()
	if err != nil {
		die(err)
	}
	out, err := c.CreateMachine(context.Background(), in)
	if err != nil {
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
	c, err := newSDK()
	if err != nil {
		die(err)
	}
	out, err := c.GetMachine(context.Background(), id)
	if err != nil {
		die(machineErr(id, err))
	}
	printJSON(out)
}

// machineErr turns the SDK's typed 404 into a friendlier message while
// leaving other errors untouched.
func machineErr(id string, err error) error {
	if sdk.IsNotFound(err) {
		return fmt.Errorf("machine %s not found", id)
	}
	return err
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
