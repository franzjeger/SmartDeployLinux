package main

import (
	"net/netip"
	"strings"
)

type netipAddr = netip.Addr

func parseIP(s string) netip.Addr {
	a, _ := netip.ParseAddr(strings.TrimSpace(s))
	return a
}
