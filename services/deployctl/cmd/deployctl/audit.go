package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/your-org/deployserver/deployctl/internal/client"
)

func auditMain(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "audit: subcommand required (query)")
		os.Exit(2)
	}
	switch args[0] {
	case "query":
		auditQuery(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "audit: unknown subcommand %q\n", args[0])
		os.Exit(2)
	}
}

func auditQuery(args []string) {
	fs := flag.NewFlagSet("audit query", flag.ExitOnError)
	since := fs.Duration("since", 24*time.Hour, "lookback window (24h, 7d, etc.)")
	action := fs.String("action", "", "filter by action prefix, e.g. auth_code")
	machine := fs.String("machine", "", "filter by machine ID")
	parseFlags(args, fs)

	q := url.Values{}
	q.Set("since", since.String())
	if *action != "" {
		q.Set("action", *action)
	}
	if *machine != "" {
		q.Set("machine", *machine)
	}

	c, err := client.New()
	if err != nil {
		die(err)
	}
	var events []map[string]any
	if err := c.Do("GET", "/api/v1/audit?"+q.Encode(), nil, &events); err != nil {
		die(err)
	}
	printJSON(events)
}
