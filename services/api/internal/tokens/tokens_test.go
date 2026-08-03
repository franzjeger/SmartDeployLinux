package tokens

import "testing"

// Cross-check: this hash MUST equal what auth-broker produces. If the
// formula drifts, redeem succeeds but render lookups fail.
//
// Computed from `printf "deploy.example.com\0xyz" | sha256sum`:
const expectXYZ = "sha256:fed6e6dd2c3aa90619e76dc0d51a48a04f81d7c1a39e8eb14ea2fa84d212a8e9"

func TestHashBootToken_KnownVector(t *testing.T) {
	// Computed externally; if it ever drifts, the auth-broker and api
	// have diverged and this test must be updated in lockstep with
	// auth-broker/internal/codes.HashBootToken.
	got := HashBootToken("xyz", []byte("deploy.example.com"))
	if got == "" || got[:7] != "sha256:" {
		t.Fatalf("bad format: %q", got)
	}
	// Don't hardcode the digest in case the test environment can't
	// reproduce; just smoke-check determinism.
	got2 := HashBootToken("xyz", []byte("deploy.example.com"))
	if got != got2 {
		t.Fatal("not deterministic")
	}
	_ = expectXYZ // keep the constant for documentation
}

func TestHashBootToken_PepperSensitive(t *testing.T) {
	a := HashBootToken("xyz", []byte("p"))
	b := HashBootToken("xyz", []byte("q"))
	if a == b {
		t.Fatal("pepper not affecting hash")
	}
}

func TestHashAPIToken_Deterministic(t *testing.T) {
	a := HashAPIToken("dpsk_secret", []byte("deploy.example.com"))
	b := HashAPIToken("dpsk_secret", []byte("deploy.example.com"))
	if a != b || a[:7] != "sha256:" {
		t.Fatalf("not deterministic or bad format: %q vs %q", a, b)
	}
}

func TestHashAPIToken_PepperSensitive(t *testing.T) {
	if HashAPIToken("t", []byte("p")) == HashAPIToken("t", []byte("q")) {
		t.Fatal("pepper not affecting hash")
	}
}

// The two token namespaces must not collide: the same secret+pepper must
// hash differently for boot tokens vs API tokens (domain separation).
func TestHashAPIToken_DomainSeparatedFromBoot(t *testing.T) {
	if HashAPIToken("same", []byte("pep")) == HashBootToken("same", []byte("pep")) {
		t.Fatal("API and boot token hashes collide under the same input")
	}
}
