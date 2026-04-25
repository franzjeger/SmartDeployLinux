// mtls loads the internal-CA and per-service cert+key, and returns
// tls.Configs for server and client roles.
//
// Server role: RequireAndVerifyClientCert with CA as the only trusted
// root. No public-CA chains accepted; only certs issued by our internal
// CA can connect to internal endpoints.
//
// Client role: verifies the server's cert against the internal CA, and
// presents our cert as the client identity.
//
// Same cert/key pair is used for both roles — `extendedKeyUsage` on the
// cert lists both serverAuth and clientAuth, so a single file works.

package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

type Bundle struct {
	Cert       tls.Certificate
	CAPool     *x509.CertPool
	CACertPath string
	CertPath   string
	KeyPath    string
}

// Load reads the three files. caPath is the issuer (truststore); certPath
// + keyPath are the service's identity. Used by both server and client
// constructors below.
func Load(caPath, certPath, keyPath string) (*Bundle, error) {
	if caPath == "" || certPath == "" || keyPath == "" {
		return nil, errors.New("mtls: ca/cert/key paths must all be set")
	}

	caBytes, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("ca pem at %s did not parse", caPath)
	}

	cert, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("load cert+key: %w", err)
	}

	return &Bundle{
		Cert:       cert,
		CAPool:     pool,
		CACertPath: caPath,
		CertPath:   certPath,
		KeyPath:    keyPath,
	}, nil
}

// ServerConfig returns a tls.Config for use on a Server.
// RequireAndVerifyClientCert means: caller MUST present a cert that
// chains to our internal CA. Any other connection is rejected at TLS
// handshake time.
func (b *Bundle) ServerConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{b.Cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    b.CAPool,
		MinVersion:   tls.VersionTLS12,
	}
}

// ClientConfig returns a tls.Config for use as an HTTP client.
// Presents our cert; verifies the server's cert against the internal CA.
func (b *Bundle) ClientConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{b.Cert},
		RootCAs:      b.CAPool,
		MinVersion:   tls.VersionTLS12,
	}
}
