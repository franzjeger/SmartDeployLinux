package codes

import (
	"strings"
	"testing"
)

func TestGenerate_FormatAndAlphabet(t *testing.T) {
	for i := 0; i < 200; i++ {
		c, err := Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if len(c) != Groups*PerGroup+1 { // +1 for the dash
			t.Fatalf("unexpected length %d for %q", len(c), c)
		}
		if c[PerGroup] != '-' {
			t.Fatalf("expected dash at position %d in %q", PerGroup, c)
		}
		// Every char in the two groups is in the alphabet.
		flat := strings.ReplaceAll(c, "-", "")
		for _, r := range flat {
			if !strings.ContainsRune(Alphabet, r) {
				t.Fatalf("char %q in %q not in alphabet", r, c)
			}
		}
	}
}

func TestGenerate_Uniqueness(t *testing.T) {
	// Coarse: in 1000 generations we should not see a duplicate, given
	// 31^6 ~ 887M space. Birthday paradox says collisions become likely
	// only above ~2^16 samples.
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		c, err := Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if seen[c] {
			t.Fatalf("duplicate code %q after %d iterations", c, i)
		}
		seen[c] = true
	}
}

func TestNormalize_HappyPaths(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"4A7-K2P", "4A7-K2P"},
		{"4a7-k2p", "4A7-K2P"},
		{"4 a 7 k 2 p", "4A7-K2P"},
		{"4a7k2p", "4A7-K2P"},
		{"  4A7-K2P  ", "4A7-K2P"},
		{"4-A-7-K-2-P", "4A7-K2P"},
	}
	for _, c := range cases {
		got, err := Normalize(c.in)
		if err != nil {
			t.Errorf("Normalize(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalize_Rejects(t *testing.T) {
	bad := []string{
		"",            // empty
		"4A7K2",       // too short
		"4A7-K2PX",    // too long
		"4A7-K2!",     // non-alphabet char
		"OII-LLL",     // confusable letters not in alphabet
		"100-100",     // 0 and 1 not in alphabet
	}
	for _, in := range bad {
		if _, err := Normalize(in); err == nil {
			t.Errorf("Normalize(%q): expected error, got nil", in)
		}
	}
}

func TestHash_Determinism(t *testing.T) {
	pepper := []byte("deploy.example.com")
	h1 := Hash("4A7-K2P", pepper)
	h2 := Hash("4A7-K2P", pepper)
	if h1 != h2 {
		t.Fatalf("Hash not deterministic: %q vs %q", h1, h2)
	}
}

func TestHash_DifferentInputsDiffer(t *testing.T) {
	pepper := []byte("deploy.example.com")
	h1 := Hash("4A7-K2P", pepper)
	h2 := Hash("4A7-K2Q", pepper) // off by one char
	if h1 == h2 {
		t.Fatalf("Hash collision for distinct inputs")
	}
}

func TestHash_DifferentPeppersDiffer(t *testing.T) {
	h1 := Hash("4A7-K2P", []byte("deploy-a.example.com"))
	h2 := Hash("4A7-K2P", []byte("deploy-b.example.com"))
	if h1 == h2 {
		t.Fatalf("Hash insensitive to pepper")
	}
}

func TestEqual_ConstantTime(t *testing.T) {
	// Smoke: Equal returns true for equal inputs, false for unequal.
	if !Equal("abc", "abc") {
		t.Fatal("Equal(abc,abc) should be true")
	}
	if Equal("abc", "abd") {
		t.Fatal("Equal(abc,abd) should be false")
	}
	// Different lengths should also return false (subtle.ConstantTimeCompare
	// returns 0 for mismatched lengths).
	if Equal("abc", "abcd") {
		t.Fatal("Equal across different lengths should be false")
	}
}

func TestNormalizeThenHash_Idempotent(t *testing.T) {
	pepper := []byte("p")
	a, _ := Normalize("4a7-k2p")
	b, _ := Normalize("4A7K2P")
	if a != b {
		t.Fatal("Normalize forms differ for same logical code")
	}
	if Hash(a, pepper) != Hash(b, pepper) {
		t.Fatal("Normalized forms hash differently")
	}
}
