package server_test

import (
	"context"
	"crypto/tls"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"diag/internal/client"
	"diag/internal/server"
	"diag/internal/tlsutil"
	"diag/internal/tools"
)

// startServer spins up a diagd on an ephemeral loopback port with the given jail root
// and token, returning its address and a cancel function.
func startServer(t *testing.T, root, token string) (addr string, cancel func()) {
	t.Helper()
	jail, err := tools.NewJailSet([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	tlsCfg, err := tlsutil.ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	logger := log.New(io.Discard, "", 0)
	srv := server.NewServer(jail, token, tools.Limits{MaxOutput: 1 << 20, Timeout: 10 * time.Second}, "", logger)

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(ctx, ln)
		close(done)
	}()
	return ln.Addr().String(), func() {
		stop()
		<-done
	}
}

func TestRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hi there\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const token = "test-token-abc123"
	addr, cancel := startServer(t, root, token)
	defer cancel()

	t.Run("good token and valid tool", func(t *testing.T) {
		resp, err := client.Call(addr, token, "read", map[string]string{"path": filepath.Join(root, "hello.txt")})
		if err != nil {
			t.Fatal(err)
		}
		if !resp.OK {
			t.Fatalf("expected OK, got error %q", resp.Error)
		}
		if resp.Output != "hi there\n" {
			t.Errorf("output = %q", resp.Output)
		}
	})

	t.Run("bad token", func(t *testing.T) {
		resp, err := client.Call(addr, "wrong", "read", map[string]string{"path": filepath.Join(root, "hello.txt")})
		if err != nil {
			t.Fatal(err)
		}
		if resp.OK || resp.Error != "unauthorized" {
			t.Errorf("expected unauthorized, got OK=%v error=%q", resp.OK, resp.Error)
		}
	})

	t.Run("empty token", func(t *testing.T) {
		resp, err := client.Call(addr, "", "ps", nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.OK || resp.Error != "unauthorized" {
			t.Errorf("expected unauthorized for empty token, got OK=%v error=%q", resp.OK, resp.Error)
		}
	})

	t.Run("unknown tool", func(t *testing.T) {
		resp, err := client.Call(addr, token, "destroy", nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.OK {
			t.Error("unknown tool should not be OK")
		}
	})

	t.Run("path escape", func(t *testing.T) {
		resp, err := client.Call(addr, token, "read", map[string]string{"path": "/etc/passwd"})
		if err != nil {
			t.Fatal(err)
		}
		if resp.OK {
			t.Error("reading outside the jail should not be OK")
		}
	})

	t.Run("ps over the wire", func(t *testing.T) {
		resp, err := client.Call(addr, token, "ps", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !resp.OK || resp.Output == "" {
			t.Errorf("ps failed: OK=%v error=%q", resp.OK, resp.Error)
		}
	})
}

func TestServerShutdownStopsAccepting(t *testing.T) {
	root := t.TempDir()
	addr, cancel := startServer(t, root, "tok")
	cancel() // shut down immediately

	// After shutdown, a dial should fail (the listener is closed).
	_, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err == nil {
		t.Error("expected dial to fail after shutdown")
	}
}
