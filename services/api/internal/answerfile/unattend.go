// Render Windows unattend.xml from a profile.
//
// We use Go's text/template with a small set of helpers. The template
// itself is stored per-profile in answer_file_templates(kind='unattend').
// At render time we inject:
//   - .Machine                 — DB row
//   - .Profile                 — DB row including jsonb vars
//   - .Image                   — image metadata (locale, edition)
//   - .DomainJoinCred          — pulled from one_shot_token at apply
//                                time, blanked in API responses unless
//                                token is presented
//   - .LocalAdminPasswordHash  — operator-supplied at issuance time

package answerfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"text/template"
	"time"
)

type UnattendInput struct {
	Machine struct {
		ID       string
		AssetTag string
		MAC      string
	}
	Profile struct {
		Name string
		Vars map[string]any
	}
	Image struct {
		OSEdition string // e.g. "Windows 11 Pro"
		Locale    string // e.g. "en-US"
		Timezone  string // e.g. "Pacific Standard Time"
		Arch      string
	}
	DomainJoin *DomainJoin
	LocalAdmin LocalAdmin

	Now time.Time
}

type DomainJoin struct {
	Domain   string
	OU       string // optional: "OU=Workstations,DC=acme,DC=corp"
	Username string
	Password string
}

type LocalAdmin struct {
	Username string
	// PasswordPlain is plain text — Windows accepts a base64'd UTF-16LE
	// password in unattend; we encode at template time. Avoid logging.
	PasswordPlain string
}

func Render(tplBody string, in UnattendInput) ([]byte, error) {
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}
	if in.Image.Locale == "" {
		in.Image.Locale = "en-US"
	}
	if in.Image.Timezone == "" {
		in.Image.Timezone = "UTC"
	}
	if in.LocalAdmin.Username == "" {
		in.LocalAdmin.Username = "Administrator"
	}

	t, err := template.New("unattend").Funcs(funcs()).Parse(tplBody)
	if err != nil {
		return nil, fmt.Errorf("parse unattend tpl: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, in); err != nil {
		return nil, fmt.Errorf("execute unattend tpl: %w", err)
	}
	return buf.Bytes(), nil
}

func funcs() template.FuncMap {
	return template.FuncMap{
		// Encodes plain-text passwords for unattend's Password element.
		// Windows accepts base64(UTF-16LE(password + "AdministratorPassword"))
		// for AutoLogon and base64(UTF-16LE(password + "Password")) for
		// LocalAccount Password — we just emit the raw, the operator's
		// template controls whether it goes inside <PlainText>true</PlainText>.
		// For real deployments use the encoded form; see WINDOWS.md.
		"adminPassword": func(s string) string { return encodeUTF16LE(s + "AdministratorPassword") },
		"accountPassword": func(s string) string { return encodeUTF16LE(s + "Password") },

		"sha256":  sha256hex,
		"upper":   strings.ToUpper,
		"lower":   strings.ToLower,
		"join":    strings.Join,
		"default": func(def, v string) string { if v == "" { return def }; return v },
	}
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// encodeUTF16LE encodes the input as UTF-16LE then base64 — the format
// Windows expects for unattend.xml password fields when not using
// PlainText.
func encodeUTF16LE(s string) string {
	// Each rune to its UTF-16LE bytes. We don't import x/text just for
	// this; for the BMP characters we expect in passwords plus the
	// fixed suffix, two-byte little-endian encoding is enough.
	var out []byte
	for _, r := range s {
		if r < 0x10000 {
			out = append(out, byte(r), byte(r>>8))
		} else {
			// surrogate pair
			r -= 0x10000
			hi := 0xD800 + (r >> 10)
			lo := 0xDC00 + (r & 0x3FF)
			out = append(out, byte(hi), byte(hi>>8), byte(lo), byte(lo>>8))
		}
	}
	return base64Encode(out)
}

// stdlib base64 without bringing in encoding/base64 to keep the dep
// graph tight... actually let's just use stdlib.
func base64Encode(b []byte) string { return b64.EncodeToString(b) }
