package codes

import (
	"strings"
	"testing"
)

func TestGenerateBootToken_LengthAndAlphabet(t *testing.T) {
	t1, err := GenerateBootToken()
	if err != nil {
		t.Fatal(err)
	}
	// 32 bytes base64url-no-padding = ceil(32*4/3) = 43 chars.
	if len(t1) != 43 {
		t.Fatalf("expected 43 chars, got %d (%q)", len(t1), t1)
	}
	const allowed = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	for _, r := range t1 {
		if !strings.ContainsRune(allowed, r) {
			t.Fatalf("char %q not in URL-safe base64 alphabet", r)
		}
	}
}

func TestGenerateBootToken_Unique(t *testing.T) {
	seen := make(map[string]bool, 200)
	for i := 0; i < 200; i++ {
		tok, err := GenerateBootToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[tok] {
			t.Fatalf("duplicate token after %d", i)
		}
		seen[tok] = true
	}
}

func TestHashBootToken_Stable(t *testing.T) {
	pepper := []byte("deploy.example.com")
	h1 := HashBootToken("xyz", pepper)
	h2 := HashBootToken("xyz", pepper)
	if h1 != h2 {
		t.Fatal("hash not stable")
	}
	if !strings.HasPrefix(h1, "sha256:") {
		t.Fatalf("hash format: %q", h1)
	}
}

func TestHashBootToken_PepperedAndInputSensitive(t *testing.T) {
	if HashBootToken("a", []byte("p")) == HashBootToken("a", []byte("q")) {
		t.Fatal("hash insensitive to pepper")
	}
	if HashBootToken("a", []byte("p")) == HashBootToken("b", []byte("p")) {
		t.Fatal("hash insensitive to input")
	}
}
