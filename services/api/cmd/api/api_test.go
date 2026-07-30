package main

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"text/template"
)

func TestBearerToken(t *testing.T) {
	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("Authorization", "Bearer abc123")
	if got := bearerToken(r); got != "abc123" {
		t.Fatalf("bearer = %q", got)
	}

	// The query-string fallback must be gone: tokens in query strings
	// leak into access logs (SECURITY.md §4 #2).
	r2 := httptest.NewRequest("GET", "/x?token=leaky", nil)
	if got := bearerToken(r2); got != "" {
		t.Fatalf("query-string token accepted: %q", got)
	}
}

func TestReadSourceIP(t *testing.T) {
	cases := map[string]string{
		"10.1.2.3:5555":       "10.1.2.3",
		"[2001:db8::1]:443":   "2001:db8::1",
		"192.168.1.1":         "192.168.1.1",
	}
	for remote, want := range cases {
		r := httptest.NewRequest("GET", "/x", nil)
		r.RemoteAddr = remote
		a, _ := readSourceIP(r)
		if !a.IsValid() {
			t.Fatalf("%s: invalid addr", remote)
		}
		if a.String() != want {
			t.Fatalf("%s: got %s want %s", remote, a, want)
		}
	}
}

func TestMapPhaseToState(t *testing.T) {
	for phase, want := range map[string]string{
		"imaging": "imaging", "post_install": "post_install",
		"completed": "completed", "failed": "failed", "bogus": "",
	} {
		if got := mapPhaseToState(phase); got != want {
			t.Fatalf("%s: got %q want %q", phase, got, want)
		}
	}
}

// The fallback user-data template must parse as a Go template and
// render the one-shot token into the phone-home Authorization header.
func TestUbuntuUserDataTemplate(t *testing.T) {
	tpl, err := template.New("t").Option("missingkey=zero").Parse(ubuntuUserDataTpl)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var sb strings.Builder
	ctx := userDataContext{
		Hostname: "host1", PublicURL: "https://deploy.example.com",
		JobID: "j-1", Token: "tok-abcdef",
	}
	if err := tpl.Execute(&sb, ctx); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, "Bearer tok-abcdef") {
		t.Fatalf("token not rendered:\n%s", out)
	}
	if strings.Contains(out, "REPLACEME") || strings.Contains(out, "ONESHOT") {
		t.Fatalf("placeholder leaked:\n%s", out)
	}
	// Without a password hash var, the account must be locked.
	if !strings.Contains(out, `password: "!"`) {
		t.Fatalf("expected locked password:\n%s", out)
	}
}

// The embedded deploy.cmd must be the real script, not a stub, and must
// stay in sync with the canonical copy in winpe/scripts/deploy.cmd.
func TestDeployCmdMatchesCanonical(t *testing.T) {
	if strings.Contains(deployCmdBody, "not yet baked") || len(deployCmdBody) < 1000 {
		t.Fatalf("deploy.cmd looks like a stub (%d bytes)", len(deployCmdBody))
	}
	canonical, err := os.ReadFile("../../../../winpe/scripts/deploy.cmd")
	if err != nil {
		t.Skipf("canonical deploy.cmd not present: %v", err)
	}
	if string(canonical) != deployCmdBody {
		t.Fatal("services/api/cmd/api/deploy.cmd out of sync with winpe/scripts/deploy.cmd")
	}
}

func TestCaptureCmdMatchesCanonical(t *testing.T) {
	if len(captureCmdBody) < 1000 {
		t.Fatalf("capture.cmd looks like a stub (%d bytes)", len(captureCmdBody))
	}
	for _, want := range []string{
		"capture-upload", "capture-complete", "Capture-Image",
		"Authorization: Bearer",
	} {
		if !strings.Contains(captureCmdBody, want) {
			t.Fatalf("capture.cmd missing %q", want)
		}
	}
	if strings.Contains(captureCmdBody, "?token=") {
		t.Fatal("capture.cmd must not use query-string tokens")
	}
	canonical, err := os.ReadFile("../../../../winpe/scripts/capture.cmd")
	if err != nil {
		t.Skipf("canonical capture.cmd not present: %v", err)
	}
	if string(canonical) != captureCmdBody {
		t.Fatal("services/api/cmd/api/capture.cmd out of sync with winpe/scripts/capture.cmd")
	}
}

func TestLowerHex(t *testing.T) {
	if got := lowerHex("AB12cdEF"); got != "ab12cdef" {
		t.Fatalf("lowerHex = %q", got)
	}
}
