// Hash boot tokens. The format must match the auth-broker's
// codes.HashBootToken — both services use this same scheme.
package tokens

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashBootToken returns the storage form of a boot token,
// matching auth-broker/internal/codes.HashBootToken.
func HashBootToken(token string, pepper []byte) string {
	h := sha256.New()
	h.Write(pepper)
	h.Write([]byte{0})
	h.Write([]byte(token))
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// APITokenPrefix is the fixed marker every long-lived API token carries.
// The auth middleware uses it to route a bearer to API-token verification
// instead of OIDC.
const APITokenPrefix = "dpsk_"

// HashAPIToken returns the storage form of a long-lived API token. It is
// domain-separated from HashBootToken (a distinct tag in the digest) so
// the two token namespaces can never collide even under the same pepper.
func HashAPIToken(token string, pepper []byte) string {
	h := sha256.New()
	h.Write(pepper)
	h.Write([]byte("apitoken\x00"))
	h.Write([]byte(token))
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
