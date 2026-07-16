// Package tlsutil provides the shared TLS configuration for diagd and diagctl. The
// server uses an in-memory ad-hoc self-signed certificate; the client skips
// verification. The session token authenticates the caller, while TLS provides
// confidentiality against passive eavesdropping.
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"time"
)

// certTTL is the validity window of the ad-hoc server certificate.
const certTTL = 24 * time.Hour

// ServerTLSConfig generates an in-memory, self-signed ECDSA P-256 certificate and
// returns a TLS configuration that presents it. The certificate is never written to
// disk and is valid for a short window. It returns an error if key or certificate
// generation fails.
func ServerTLSConfig() (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "diagd"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(certTTL),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"diagd"},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{der},
			PrivateKey:  key,
		}},
		MinVersion: tls.VersionTLS12,
	}, nil
}

// ClientTLSConfig returns a TLS client configuration that skips certificate
// verification. The token authenticates the session; TLS only encrypts the transport.
// This does not defend against an active man-in-the-middle, by design.
func ClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	}
}
