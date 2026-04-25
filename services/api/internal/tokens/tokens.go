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
