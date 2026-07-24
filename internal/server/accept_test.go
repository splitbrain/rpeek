package server

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// errFakeAccept is the persistent error the fake listener reports, standing in for an
// EMFILE/ENFILE-class condition that makes Accept fail immediately on every call.
var errFakeAccept = errors.New("fake accept error")

// errListener is a net.Listener whose Accept always fails, counting how often it is
// called so a test can distinguish a backed-off loop from a busy-spin without needing to
// exhaust real file descriptors. Close unblocks nothing because Accept never blocks.
type errListener struct {
	// calls counts the number of Accept invocations.
	calls int64
}

// Accept records the call and always returns the persistent error.
func (l *errListener) Accept() (net.Conn, error) {
	atomic.AddInt64(&l.calls, 1)
	return nil, errFakeAccept
}

// Close satisfies net.Listener and does nothing.
func (l *errListener) Close() error { return nil }

// Addr satisfies net.Listener with a placeholder address.
func (l *errListener) Addr() net.Addr { return &net.TCPAddr{} }

// TestServeBacksOffOnAcceptErrors verifies that a persistent Accept failure makes the
// accept loop back off rather than spin at 100% CPU, and that a cancellation during the
// backoff sleep returns promptly.
func TestServeBacksOffOnAcceptErrors(t *testing.T) {
	ln := &errListener{}
	srv := NewServer(nil, "tok", log.New(io.Discard, "", 0))

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(ctx, ln)
		close(done)
	}()

	// Let the loop fail repeatedly for a short window, then cancel.
	time.Sleep(200 * time.Millisecond)
	stop()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return promptly after cancellation during backoff")
	}

	// With capped exponential backoff (5ms, 10ms, 20ms, ...) only a handful of Accept
	// calls fit in 200ms; a busy-spin would produce many thousands.
	n := atomic.LoadInt64(&ln.calls)
	if n == 0 {
		t.Fatal("expected at least one Accept call")
	}
	if n > 50 {
		t.Fatalf("accept loop appears to busy-spin: %d Accept calls in 200ms", n)
	}
}
