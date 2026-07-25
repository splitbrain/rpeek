package client_test

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"rpeek/internal/client"
	"rpeek/internal/protocol"
	"rpeek/internal/tlsutil"
)

// fakeServer starts a TLS server that, per connection, reads a single request line, decodes
// it, and hands it to respond. respond returns the bytes to write back (a newline is
// appended) and whether to write at all; write=false closes the connection with no response,
// modelling a silent server. The listener runs until the test ends.
func fakeServer(t *testing.T, respond func(req protocol.Request) (reply []byte, write bool)) string {
	t.Helper()
	cfg, err := tlsutil.ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				sc := bufio.NewScanner(conn)
				sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
				if !sc.Scan() {
					return
				}
				var req protocol.Request
				_ = json.Unmarshal(sc.Bytes(), &req)
				reply, write := respond(req)
				if !write {
					return
				}
				_, _ = conn.Write(append(reply, '\n'))
			}()
		}
	}()
	return ln.Addr().String()
}

// TestCallSuccess checks that Call marshals the request faithfully and returns the server's
// successful response.
func TestCallSuccess(t *testing.T) {
	gotReq := make(chan protocol.Request, 1)
	addr := fakeServer(t, func(req protocol.Request) ([]byte, bool) {
		gotReq <- req
		b, _ := json.Marshal(protocol.Response{OK: true, Output: "result\n"})
		return b, true
	})

	resp, err := client.Call(addr, "tok", "grep", map[string]string{"pattern": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Output != "result\n" {
		t.Errorf("resp = %+v, want OK with output", resp)
	}

	req := <-gotReq
	if req.Token != "tok" || req.Tool != "grep" {
		t.Errorf("server saw token=%q tool=%q, want tok/grep", req.Token, req.Tool)
	}
	var args map[string]string
	if err := json.Unmarshal(req.Args, &args); err != nil {
		t.Fatalf("args did not round-trip: %v", err)
	}
	if args["pattern"] != "x" {
		t.Errorf("args = %v, want pattern=x", args)
	}
}

// TestCallNilArgs checks that a tool taking no arguments sends no args, which the server
// sees as empty.
func TestCallNilArgs(t *testing.T) {
	gotReq := make(chan protocol.Request, 1)
	addr := fakeServer(t, func(req protocol.Request) ([]byte, bool) {
		gotReq <- req
		b, _ := json.Marshal(protocol.Response{OK: true})
		return b, true
	})
	if _, err := client.Call(addr, "tok", "ps", nil); err != nil {
		t.Fatal(err)
	}
	if req := <-gotReq; len(req.Args) != 0 {
		t.Errorf("args = %s, want empty for a no-arg tool", req.Args)
	}
}

// TestCallToolError checks that a tool failure reported by the server surfaces as a Response
// with OK false and no transport error, so callers can tell the two apart.
func TestCallToolError(t *testing.T) {
	addr := fakeServer(t, func(req protocol.Request) ([]byte, bool) {
		b, _ := json.Marshal(protocol.Response{OK: false, Error: "not found"})
		return b, true
	})
	resp, err := client.Call(addr, "tok", "read", map[string]string{"path": "/x"})
	if err != nil {
		t.Fatalf("a tool error must not be a transport error: %v", err)
	}
	if resp.OK || resp.Error != "not found" {
		t.Errorf("resp = %+v, want OK=false error=not found", resp)
	}
}

// TestCallNoResponse checks that a server closing the connection without replying yields a
// clear error rather than a nil response.
func TestCallNoResponse(t *testing.T) {
	addr := fakeServer(t, func(req protocol.Request) ([]byte, bool) {
		return nil, false // close without writing
	})
	if _, err := client.Call(addr, "tok", "ps", nil); err == nil {
		t.Fatal("expected an error when the server sends no response")
	}
}

// TestCallInvalidJSON checks that a malformed response line is reported as an error.
func TestCallInvalidJSON(t *testing.T) {
	addr := fakeServer(t, func(req protocol.Request) ([]byte, bool) {
		return []byte("not json"), true
	})
	if _, err := client.Call(addr, "tok", "ps", nil); err == nil || !strings.Contains(err.Error(), "invalid response") {
		t.Fatalf("err = %v, want an invalid-response error", err)
	}
}

// TestCallTransportError checks that a failure to reach the server is returned as an error.
func TestCallTransportError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // nothing is listening on addr now

	if _, err := client.Call(addr, "tok", "ps", nil); err == nil {
		t.Fatal("expected a transport error dialing an unused address")
	}
}

// TestCallUnmarshalableArgs checks that args which cannot be JSON-encoded fail before any
// network activity.
func TestCallUnmarshalableArgs(t *testing.T) {
	// A channel cannot be marshalled to JSON; the address is never dialled.
	if _, err := client.Call("127.0.0.1:0", "tok", "ps", make(chan int)); err == nil {
		t.Fatal("expected a marshal error for un-encodable args")
	}
}
