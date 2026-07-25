package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"testing"
	"time"
)

// TestServerTLSConfig checks that the generated server configuration presents a single
// self-signed certificate valid for the expected short window, restricted to TLS 1.2+ and
// server authentication.
func TestServerTLSConfig(t *testing.T) {
	cfg, err := ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %#x, want TLS 1.2 (%#x)", cfg.MinVersion, tls.VersionTLS12)
	}
	if len(cfg.Certificates) != 1 || len(cfg.Certificates[0].Certificate) != 1 {
		t.Fatalf("want exactly one certificate, got %d", len(cfg.Certificates))
	}

	leaf, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if leaf.Subject.CommonName != "rpeek" {
		t.Errorf("CommonName = %q, want %q", leaf.Subject.CommonName, "rpeek")
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "rpeek" {
		t.Errorf("DNSNames = %v, want [rpeek]", leaf.DNSNames)
	}
	if got := leaf.NotAfter.Sub(leaf.NotBefore); got != certTTL+time.Minute {
		t.Errorf("validity window = %s, want %s", got, certTTL+time.Minute)
	}
	if now := time.Now(); now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		t.Errorf("certificate not valid now: window is [%s, %s]", leaf.NotBefore, leaf.NotAfter)
	}

	serverAuth := false
	for _, u := range leaf.ExtKeyUsage {
		if u == x509.ExtKeyUsageServerAuth {
			serverAuth = true
		}
	}
	if !serverAuth {
		t.Error("certificate is missing ExtKeyUsageServerAuth")
	}
}

// TestServerTLSConfigIsFresh checks that each call mints a new certificate, so a restart
// does not reuse a key or serial number.
func TestServerTLSConfigIsFresh(t *testing.T) {
	a, err := ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	b, err := ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(a.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	cb, err := x509.ParseCertificate(b.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if ca.SerialNumber.Cmp(cb.SerialNumber) == 0 {
		t.Error("two configs share a serial number; certificate is not freshly generated")
	}
}

// TestClientTLSConfig checks that the client configuration encrypts with TLS 1.2+ but skips
// verification, matching the token-authenticated, non-MITM-resistant security model.
func TestClientTLSConfig(t *testing.T) {
	cfg := ClientTLSConfig()
	if !cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify = false, want true")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %#x, want TLS 1.2 (%#x)", cfg.MinVersion, tls.VersionTLS12)
	}
}

// TestConfigsInteroperate checks that a client using ClientTLSConfig completes a handshake
// against a server using ServerTLSConfig, negotiates TLS 1.2 or better, sees the ad-hoc
// "rpeek" certificate, and can exchange bytes.
func TestConfigsInteroperate(t *testing.T) {
	serverCfg, err := ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, "hello")
	}()

	conn, err := tls.Dial("tcp", ln.Addr().String(), ClientTLSConfig())
	if err != nil {
		t.Fatalf("handshake failed: %v", err)
	}
	defer conn.Close()

	if v := conn.ConnectionState().Version; v < tls.VersionTLS12 {
		t.Errorf("negotiated version %#x, want >= TLS 1.2", v)
	}
	if certs := conn.ConnectionState().PeerCertificates; len(certs) == 0 || certs[0].Subject.CommonName != "rpeek" {
		t.Errorf("peer certificate = %v, want CommonName rpeek", certs)
	}

	buf := make([]byte, 5)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read over TLS: %v", err)
	}
	if string(buf) != "hello" {
		t.Errorf("read %q, want %q", buf, "hello")
	}
}

// TestServerCertificateIsUnverifiable checks that a verifying client rejects the ad-hoc
// certificate, documenting why the client must skip verification rather than trust a chain.
func TestServerCertificateIsUnverifiable(t *testing.T) {
	serverCfg, err := ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		if tc, ok := conn.(*tls.Conn); ok {
			_ = tc.Handshake()
		}
		conn.Close()
	}()

	// A default, verifying client cannot build a chain to the self-signed certificate.
	if _, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{MinVersion: tls.VersionTLS12}); err == nil {
		t.Error("verifying client accepted a self-signed certificate; expected failure")
	}
}
