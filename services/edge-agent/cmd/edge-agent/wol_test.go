package main

import (
	"bytes"
	"testing"
)

func TestBuildMagicPacket(t *testing.T) {
	pkt, err := buildMagicPacket("aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkt) != 102 {
		t.Fatalf("packet length %d, want 102", len(pkt))
	}
	if !bytes.Equal(pkt[:6], bytes.Repeat([]byte{0xFF}, 6)) {
		t.Fatalf("bad sync stream: % x", pkt[:6])
	}
	mac := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}
	for i := 0; i < 16; i++ {
		got := pkt[6+i*6 : 6+(i+1)*6]
		if !bytes.Equal(got, mac) {
			t.Fatalf("repetition %d wrong: % x", i, got)
		}
	}
}

func TestBuildMagicPacketFormats(t *testing.T) {
	// Dash-separated and mixed-case must work; garbage and non-48-bit
	// (EUI-64) MACs must not.
	if _, err := buildMagicPacket("AA-BB-CC-DD-EE-FF"); err != nil {
		t.Fatalf("dash format rejected: %v", err)
	}
	if _, err := buildMagicPacket("not-a-mac"); err == nil {
		t.Fatal("garbage accepted")
	}
	if _, err := buildMagicPacket("01:23:45:67:89:ab:cd:ef"); err == nil {
		t.Fatal("EUI-64 accepted")
	}
}
