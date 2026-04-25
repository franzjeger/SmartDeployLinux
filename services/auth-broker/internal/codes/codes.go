// Code generation, formatting, and hashing.
//
// Codes are 6 characters in two groups of 3, drawn from a 31-character
// alphabet that excludes the confusable glyphs 0/1/I/L/O. With 31^6 ≈
// 8.9e8 possible codes, combined with a 24h TTL and a 5-attempts-per-code
// lock, brute-forcing a single code is infeasible. Brute-forcing the
// *space* is rate-limited per source IP.

package codes

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// 31 chars: A–Z minus I,L,O and 0–9 minus 0,1. 31^6 ≈ 887,503,681 codes.
const Alphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

const Groups = 2
const PerGroup = 3

func Generate() (string, error) {
	out := make([]byte, Groups*PerGroup)
	for i := range out {
		var b [1]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		// Rejection sampling so the distribution is uniform across the
		// alphabet. A modulo bias here would be slight (256 mod 31 == 8)
		// but we don't accept it for a security primitive.
		for {
			if int(b[0]) < (256/len(Alphabet))*len(Alphabet) {
				out[i] = Alphabet[int(b[0])%len(Alphabet)]
				break
			}
			if _, err := rand.Read(b[:]); err != nil {
				return "", err
			}
		}
	}
	return fmt.Sprintf("%s-%s", out[:PerGroup], out[PerGroup:]), nil
}

// Normalize uppercases, strips dashes/whitespace, and rejects anything
// outside the alphabet. Returns the canonical form `XXX-XXX` if valid.
func Normalize(input string) (string, error) {
	s := strings.ToUpper(strings.TrimSpace(input))
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	if len(s) != Groups*PerGroup {
		return "", fmt.Errorf("expected %d characters, got %d", Groups*PerGroup, len(s))
	}
	for _, r := range s {
		if !strings.ContainsRune(Alphabet, r) {
			return "", fmt.Errorf("character %q not in alphabet", r)
		}
	}
	return fmt.Sprintf("%s-%s", s[:PerGroup], s[PerGroup:]), nil
}

// Hash returns the argon2id digest of a normalized code, hex-encoded with
// salt prefix. Salt is fixed across the table (the code itself is the
// secret); we accept the loss of per-row salting because the alphabet is
// small enough that uniform salting would not help against a database
// dump — an attacker who has the table can still trivially compute the
// hash for every possible code. The defense-in-depth is the 5-attempts
// lock and per-IP rate limit, not hash-uncrackability.
//
// Argon2id parameters: memory=64MiB, time=3, parallelism=2. Tuned for ~80ms
// on a modern x86_64; this runs once per redeem attempt.
func Hash(normalized string, pepper []byte) string {
	digest := argon2.IDKey(
		[]byte(normalized),
		pepper,
		3, 64*1024, 2, 32,
	)
	return fmt.Sprintf("argon2id$v=19$m=65536,t=3,p=2$%x", digest)
}

// Equal compares two hash strings in constant time.
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

var ErrInvalidFormat = errors.New("invalid code format")

// --- boot tokens ------------------------------------------------------
//
// Boot tokens are NOT operator-typed. They are 32-byte random URL-safe
// strings that the deploy server mints at redeem time and includes in
// the chainload URL. The bootstrap stick uses them only as opaque
// strings; the user never sees them.
//
// We do NOT argon2id-hash boot tokens — they're high-entropy and the
// per-fetch lookup cost matters. SHA-256 with the deploy FQDN as a
// pepper is sufficient.

// GenerateBootToken returns a 32-byte (256-bit) URL-safe random string.
func GenerateBootToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// HashBootToken returns the storage form of a boot token.
// Format: "sha256:<hex>"
func HashBootToken(token string, pepper []byte) string {
	h := sha256.New()
	h.Write(pepper)
	h.Write([]byte{0})
	h.Write([]byte(token))
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
