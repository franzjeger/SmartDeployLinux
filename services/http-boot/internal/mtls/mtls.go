// Mirror of services/api/internal/mtls/mtls.go. Same contract; copied
// rather than shared because each service is its own Go module and we
// don't want a workspace.

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

func (b *Bundle) ServerConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{b.Cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    b.CAPool,
		MinVersion:   tls.VersionTLS12,
	}
}

func (b *Bundle) ClientConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{b.Cert},
		RootCAs:      b.CAPool,
		MinVersion:   tls.VersionTLS12,
	}
}
