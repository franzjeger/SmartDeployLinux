package broker

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/your-org/deployserver/auth-broker/internal/config"
)

func secretBroker(secret string) *Broker {
	return &Broker{cfg: &config.Config{IssueSharedSecret: secret}}
}

func callWithSecret(b *Broker, header string) int {
	h := b.requireInternalSecret(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest("POST", "/api/v1/bootstrap/issue-code", nil)
	if header != "" {
		r.Header.Set("X-Internal-Auth", header)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

func TestRequireInternalSecret(t *testing.T) {
	b := secretBroker("s3cret")
	if got := callWithSecret(b, "s3cret"); got != http.StatusOK {
		t.Fatalf("correct secret rejected: %d", got)
	}
	// Wrong or missing secret → 404, indistinguishable from no route.
	if got := callWithSecret(b, "wrong"); got != http.StatusNotFound {
		t.Fatalf("wrong secret: got %d want 404", got)
	}
	if got := callWithSecret(b, ""); got != http.StatusNotFound {
		t.Fatalf("missing secret: got %d want 404", got)
	}
}

func TestRequireInternalSecretDevMode(t *testing.T) {
	// No secret configured: allowed (dev-mode escape hatch).
	if got := callWithSecret(secretBroker(""), ""); got != http.StatusOK {
		t.Fatalf("dev-mode call rejected: %d", got)
	}
}
