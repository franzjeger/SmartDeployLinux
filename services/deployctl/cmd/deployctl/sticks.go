package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/your-org/deployserver/deployctl/internal/client"
)

func sticksMain(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "bootstrap-sticks: subcommand required (register|list)")
		os.Exit(2)
	}
	switch args[0] {
	case "register":
		sticksRegister(args[1:])
	case "list":
		sticksList(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "bootstrap-sticks: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

func sticksRegister(args []string) {
	fs := flag.NewFlagSet("bootstrap-sticks register", flag.ExitOnError)
	imageSHA := fs.String("image-sha", "", "sha256 of the flashed image (required)")
	tailnet := fs.String("tailnet", "", "tailnet name baked into the stick (required)")
	deployURL := fs.String("deploy-url", "", "deploy URL baked into the stick (required)")
	caFP := fs.String("ca-fingerprint", "", "fingerprint of CA cert pinned on the stick (required)")
	label := fs.String("label", "", "free-text label")
	parseFlags(args, fs)

	for k, v := range map[string]string{
		"image-sha":      *imageSHA,
		"tailnet":        *tailnet,
		"deploy-url":     *deployURL,
		"ca-fingerprint": *caFP,
	} {
		if v == "" {
			fmt.Fprintf(os.Stderr, "--%s required\n", k)
			os.Exit(2)
		}
	}

	body := map[string]any{
		"image_sha256":   *imageSHA,
		"tailnet":        *tailnet,
		"deploy_url":     *deployURL,
		"ca_fingerprint": *caFP,
	}
	if *label != "" {
		body["label"] = *label
	}
	c, err := client.New()
	if err != nil {
		die(err)
	}
	var out map[string]any
	if err := c.Do("POST", "/api/v1/bootstrap-sticks", body, &out); err != nil {
		die(err)
	}
	printJSON(out)
}

func sticksList(args []string) {
	fs := flag.NewFlagSet("bootstrap-sticks list", flag.ExitOnError)
	caFP := fs.String("ca-fingerprint", "", "filter to sticks pinned to this CA")
	parseFlags(args, fs)

	path := "/api/v1/bootstrap-sticks"
	if *caFP != "" {
		path += "?ca_fingerprint=" + *caFP
	}
	c, err := client.New()
	if err != nil {
		die(err)
	}
	var out []map[string]any
	if err := c.Do("GET", path, nil, &out); err != nil {
		die(err)
	}
	printJSON(out)
}
