package server_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rpeek/internal/client"
	"rpeek/internal/protocol"
	"rpeek/internal/server"
	"rpeek/internal/tlsutil"
	"rpeek/internal/tools"
	"rpeek/internal/version"
)

// startServer spins up an rpeek server on an ephemeral loopback port with the given jail root
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
	runner := tools.NewRunner(tools.Env{Jail: jail, Limits: tools.Limits{MaxOutput: 1 << 20, Timeout: 10 * time.Second}})
	srv := server.NewServer(runner, token, logger)

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

	t.Run("version over the wire", func(t *testing.T) {
		resp, err := client.Call(addr, token, "version", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !resp.OK {
			t.Fatalf("version failed: error=%q", resp.Error)
		}
		if got := strings.TrimSpace(resp.Output); got != version.Version {
			t.Errorf("version = %q, want %q", got, version.Version)
		}
	})

	t.Run("version requires auth", func(t *testing.T) {
		resp, err := client.Call(addr, "wrong", "version", nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.OK || resp.Error != "unauthorized" {
			t.Errorf("expected unauthorized, got OK=%v error=%q", resp.OK, resp.Error)
		}
	})

	t.Run("client-only tool rejected over the wire", func(t *testing.T) {
		resp, err := client.Call(addr, token, "help", nil)
		if err != nil {
			t.Fatal(err)
		}
		if resp.OK {
			t.Error("help has no server-side operation and should be rejected")
		}
	})
}

// panicRunner is a ToolRunner whose RunRemote always panics, used to verify the
// server recovers from a panicking tool instead of crashing the whole process.
type panicRunner struct{}

// RunRemote always panics.
func (panicRunner) RunRemote(ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	panic("boom")
}

func TestHandleConnRecoversFromPanic(t *testing.T) {
	const token = "tok"
	tlsCfg, err := tlsutil.ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := server.NewServer(panicRunner{}, token, log.New(io.Discard, "", 0))

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(ctx, ln)
		close(done)
	}()
	defer func() {
		stop()
		<-done
	}()
	addr := ln.Addr().String()

	// A panicking tool must degrade to a generic error response, not crash the server.
	resp, err := client.Call(addr, token, "boom", nil)
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if resp.OK {
		t.Error("panicking tool should not return OK")
	}
	if resp.Error != "internal error" {
		t.Errorf("error = %q, want %q", resp.Error, "internal error")
	}

	// The server must still be serving: a second request also gets a response,
	// proving the panic did not terminate the process.
	resp2, err := client.Call(addr, token, "boom", nil)
	if err != nil {
		t.Fatalf("second call failed (server may have crashed): %v", err)
	}
	if resp2.OK {
		t.Error("second panicking tool should not return OK")
	}
}

func TestOversizedRequestLine(t *testing.T) {
	root := t.TempDir()
	const token = "tok"
	addr, cancel := startServer(t, root, token)
	defer cancel()

	conn, err := tls.Dial("tcp", addr, tlsutil.ClientTLSConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	// A request line larger than maxRequestLine (1 MiB) with no terminating newline
	// forces the server's scanner to report ErrTooLong. The write runs in a goroutine
	// because the server answers and closes as soon as it detects the overflow, which
	// may break the write before the whole payload is flushed.
	go func() {
		_, _ = conn.Write(bytes.Repeat([]byte("A"), 2<<20))
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 16<<20)
	if !scanner.Scan() {
		t.Fatalf("expected a response line, got none: %v", scanner.Err())
	}
	var resp protocol.Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("invalid response: %v", err)
	}
	if resp.OK || resp.Error != "request too large" {
		t.Errorf("expected request too large, got OK=%v error=%q", resp.OK, resp.Error)
	}
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
