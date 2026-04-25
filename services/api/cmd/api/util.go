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

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
