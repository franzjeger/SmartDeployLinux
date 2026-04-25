package mtls

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// genTestCA produces a CA + a single leaf usable for both server and
// client. Returns the paths.
func genTestCA(t *testing.T) (caPath, certPath, keyPath string) {
	t.Helper()
	dir := t.TempDir()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caTpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-ca"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	leafTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{"localhost"},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTpl, caTpl, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	caPath = filepath.Join(dir, "ca.pem")
	certPath = filepath.Join(dir, "leaf.pem")
	keyPath = filepath.Join(dir, "leaf-key.pem")
	writePEM(t, caPath, "CERTIFICATE", caDER)
	writePEM(t, certPath, "CERTIFICATE", leafDER)
	writePrivPEM(t, keyPath, leafKey)
	return
}

func writePEM(t *testing.T, path, typ string, der []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: typ, Bytes: der}); err != nil {
		t.Fatal(err)
	}
}

func writePrivPEM(t *testing.T, path string, k *rsa.PrivateKey) {
	t.Helper()
	der := x509.MarshalPKCS1PrivateKey(k)
	writePEM(t, path, "RSA PRIVATE KEY", der)
}

func TestLoad_ErrorsOnMissingPath(t *testing.T) {
	if _, err := Load("", "", ""); err == nil {
		t.Fatal("expected error for empty paths")
	}
	if _, err := Load("/nope/ca", "/nope/c", "/nope/k"); err == nil {
		t.Fatal("expected error for nonexistent files")
	}
}

func TestLoad_HappyPath(t *testing.T) {
	caP, cP, kP := genTestCA(t)
	b, err := Load(caP, cP, kP)
	if err != nil {
		t.Fatal(err)
	}
	if b.CAPool == nil || len(b.Cert.Certificate) == 0 {
		t.Fatal("missing fields after Load")
	}
}

// End-to-end mTLS handshake: server requires client cert; valid client
// connects, no-cert client is rejected, foreign-CA client is rejected.
func TestMTLS_Handshake(t *testing.T) {
	caP, cP, kP := genTestCA(t)
	b, err := Load(caP, cP, kP)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.TLS = b.ServerConfig()
	srv.StartTLS()
	defer srv.Close()

	// Valid client: same bundle.
	tr := &http.Transport{TLSClientConfig: b.ClientConfig()}
	tr.TLSClientConfig.ServerName = "localhost"
	cli := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	resp, err := cli.Get(srv.URL)
	if err != nil {
		t.Fatalf("valid mTLS call failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body: %q", body)
	}

	// No-cert client: should be rejected at handshake.
	tr2 := &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs:    b.CAPool,
		ServerName: "localhost",
	}}
	cli2 := &http.Client{Transport: tr2, Timeout: 5 * time.Second}
	if _, err := cli2.Get(srv.URL); err == nil {
		t.Fatal("expected no-cert client to be rejected")
	}

	// Client with a cert from a DIFFERENT CA: should be rejected.
	caP2, cP2, kP2 := genTestCA(t)
	b2, err := Load(caP2, cP2, kP2)
	if err != nil {
		t.Fatal(err)
	}
	tr3 := &http.Transport{TLSClientConfig: &tls.Config{
		Certificates: []tls.Certificate{b2.Cert},
		RootCAs:      b.CAPool, // trusts our server but presents their cert
		ServerName:   "localhost",
	}}
	cli3 := &http.Client{Transport: tr3, Timeout: 5 * time.Second}
	if _, err := cli3.Get(srv.URL); err == nil {
		t.Fatal("expected foreign-CA client to be rejected")
	}
}
