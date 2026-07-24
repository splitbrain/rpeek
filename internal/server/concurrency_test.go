package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"testing"
	"time"

	"rpeek/internal/client"
	"rpeek/internal/tlsutil"
)

// blockingRunner is a ToolRunner that signals when a request reaches it and then blocks
// until released or the context is cancelled, so a test can hold connections open and
// observe how many are served concurrently.
type blockingRunner struct {
	// started receives one value each time RunRemote is entered.
	started chan struct{}
	// release unblocks one waiting RunRemote per value sent.
	release chan struct{}
}

// RunRemote reports that it started, then blocks until released or the context is cancelled.
func (b *blockingRunner) RunRemote(ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	b.started <- struct{}{}
	select {
	case <-b.release:
		return "ok", false, nil
	case <-ctx.Done():
		return "", false, ctx.Err()
	}
}

func TestServeBoundsConcurrentConnections(t *testing.T) {
	const token = "tok"
	runner := &blockingRunner{started: make(chan struct{}, 16), release: make(chan struct{})}

	tlsCfg, err := tlsutil.ServerTLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(runner, token, log.New(io.Discard, "", 0))
	srv.maxConns = 2

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(ctx, ln)
		close(done)
	}()
	t.Cleanup(func() {
		stop()
		<-done
	})
	addr := ln.Addr().String()

	// Launch three concurrent clients against a two-slot server.
	for i := 0; i < 3; i++ {
		go func() { _, _ = client.Call(addr, token, "read", nil) }()
	}

	// Exactly two handlers may run at once.
	for i := 0; i < 2; i++ {
		select {
		case <-runner.started:
		case <-time.After(2 * time.Second):
			t.Fatalf("expected 2 handlers to start, only %d did", i)
		}
	}

	// The third connection must be blocked at the concurrency gate: its request is
	// not even read, so RunRemote is never reached while both slots are held.
	select {
	case <-runner.started:
		t.Fatal("third connection was served despite the concurrency limit")
	case <-time.After(300 * time.Millisecond):
	}

	// Freeing one slot lets the third connection through.
	runner.release <- struct{}{}
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("third connection was not served after a slot freed")
	}
}
