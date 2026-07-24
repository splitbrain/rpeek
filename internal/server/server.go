// Package server implements the rpeek serve transport: the accept loop, per-connection
// authentication, and the request/response envelope. It is agnostic to the tool set — it
// hands each authenticated request to a ToolRunner and relays the result — so it imports
// nothing from the tools package.
package server

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net"
	"runtime/debug"
	"sync"
	"time"

	"rpeek/internal/protocol"
)

// requestReadTimeout bounds how long the server waits for a connected client to complete
// its TLS handshake and send its request line. It is deliberately short and applies before
// authentication, so a client that connects but stalls is reaped quickly and cannot hold a
// concurrency slot pre-auth.
const requestReadTimeout = 10 * time.Second

// responseWriteTimeout bounds how long the server will spend writing its response, so a
// client that stops reading cannot hold a goroutine indefinitely.
const responseWriteTimeout = 30 * time.Second

// maxRequestLine bounds the size of a single request line the server will read.
const maxRequestLine = 1 << 20

// maxConcurrentConns is the default cap on connections served at once. It bounds the
// goroutines, file descriptors, and scanner buffers the server allocates, so no volume of
// concurrent (and as-yet unauthenticated) connections can exhaust those resources. rpeek
// serves a single client — a handful of connections even when several sub-agents query in
// parallel — so a small cap is ample.
const maxConcurrentConns = 16

// acceptBackoffInitial and acceptBackoffMax bound the capped exponential backoff the
// accept loop applies after a failed Accept. A persistent error — classically EMFILE or
// ENFILE once file descriptors are exhausted — otherwise returns immediately on every
// iteration, spinning the CPU at 100% and flooding the log. Backing off from a few
// milliseconds up to a second caps that cost and gives the transient condition time to
// clear, mirroring net/http's Serve loop.
const (
	acceptBackoffInitial = 5 * time.Millisecond
	acceptBackoffMax     = 1 * time.Second
)

// ToolRunner runs a named tool's server-side operation. It is the server's sole view of
// the tool set: given a tool name and its raw arguments it returns the tool's text output
// and truncation flag, or an error — unknown tool, no server-side operation, or a failure
// from the tool itself. The tools package supplies the implementation; the error text is
// relayed to the client verbatim.
type ToolRunner interface {
	RunRemote(ctx context.Context, name string, args json.RawMessage) (output string, truncated bool, err error)
}

// Server holds the runtime configuration for a rpeek serve instance.
type Server struct {
	// runner dispatches each request to the matching tool's server-side operation.
	runner ToolRunner
	// token is the shared secret every request must present.
	token string
	// logger writes one audit line per request to stderr.
	logger *log.Logger
	// maxConns caps the number of connections handled concurrently.
	maxConns int
}

// NewServer builds a Server from the tool runner, the shared auth token, and the logger.
func NewServer(runner ToolRunner, token string, logger *log.Logger) *Server {
	return &Server{runner: runner, token: token, logger: logger, maxConns: maxConcurrentConns}
}

// Serve accepts connections on ln until ctx is cancelled, handling each in its own
// goroutine. When ctx is cancelled it stops accepting, waits for in-flight handlers,
// and returns.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	// sem bounds concurrency: a slot is acquired before a connection is handed to a
	// goroutine and released when that goroutine returns. When all slots are taken the
	// accept loop blocks here, leaving further connections queued in the kernel backlog
	// (and ultimately refused) rather than each spawning a goroutine of its own.
	sem := make(chan struct{}, s.maxConns)
	var wg sync.WaitGroup
	// acceptDelay grows on consecutive Accept failures and resets to zero after a
	// successful accept, so a burst of transient errors backs off but normal operation
	// pays no cost.
	var acceptDelay time.Duration
acceptLoop:
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			if acceptDelay == 0 {
				acceptDelay = acceptBackoffInitial
			} else {
				acceptDelay *= 2
			}
			if acceptDelay > acceptBackoffMax {
				acceptDelay = acceptBackoffMax
			}
			s.logger.Printf("accept error (retrying in %s): %v", acceptDelay, err)
			select {
			case <-time.After(acceptDelay):
			case <-ctx.Done():
				break acceptLoop
			}
			continue
		}
		acceptDelay = 0
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			// Shutting down while at capacity: drop the connection instead of
			// waiting for a slot, so shutdown is not gated on in-flight work.
			conn.Close()
			break acceptLoop
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			s.handleConn(ctx, conn)
		}()
	}
	wg.Wait()
	return nil
}

// handleConn reads one request, authenticates it, hands it to the runner, writes one
// response, and closes the connection.
func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	start := time.Now()
	remote := conn.RemoteAddr().String()
	var tool string

	// Recover from any panic during request handling so a single pathological
	// request degrades to one failed response instead of terminating the whole
	// server process. The panic value and stack are logged server-side only; the
	// client receives a generic error carrying no internal detail.
	defer func() {
		if r := recover(); r != nil {
			s.logger.Printf("panic recovered: remote=%s tool=%s panic=%v\n%s",
				remote, tool, r, debug.Stack())
			s.writeResponse(conn, protocol.Response{OK: false, Error: "internal error"})
			s.logRequest(remote, tool, false, time.Since(start), 0)
		}
	}()

	_ = conn.SetReadDeadline(time.Now().Add(requestReadTimeout))

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), maxRequestLine)
	if !scanner.Scan() {
		// A request line exceeding maxRequestLine surfaces as ErrTooLong; answer it so
		// the client sees "request too large" rather than the generic "no response from
		// server". Any other failure (EOF, timeout) leaves nothing meaningful to send.
		if errors.Is(scanner.Err(), bufio.ErrTooLong) {
			s.writeResponse(conn, protocol.Response{OK: false, Error: "request too large"})
		}
		s.logRequest(remote, "", false, time.Since(start), 0)
		return
	}

	var req protocol.Request
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		s.writeResponse(conn, protocol.Response{OK: false, Error: "invalid request"})
		s.logRequest(remote, "", false, time.Since(start), 0)
		return
	}
	tool = req.Tool

	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(s.token)) != 1 {
		s.writeResponse(conn, protocol.Response{OK: false, Error: "unauthorized"})
		s.logRequest(remote, req.Tool, false, time.Since(start), 0)
		return
	}

	output, truncated, err := s.runner.RunRemote(ctx, req.Tool, req.Args)
	var resp protocol.Response
	if err != nil {
		resp = protocol.Response{OK: false, Error: err.Error()}
	} else {
		resp = protocol.Response{OK: true, Output: output, Truncated: truncated}
	}
	s.writeResponse(conn, resp)
	s.logRequest(remote, req.Tool, err == nil, time.Since(start), len(output))
}

// writeResponse marshals resp, appends a newline, and writes it under a deadline.
func (s *Server) writeResponse(conn net.Conn, resp protocol.Response) {
	_ = conn.SetWriteDeadline(time.Now().Add(responseWriteTimeout))
	data, err := json.Marshal(resp)
	if err != nil {
		data, _ = json.Marshal(protocol.Response{OK: false, Error: "internal error"})
	}
	data = append(data, '\n')
	_, _ = conn.Write(data)
}

// logRequest writes one audit line per request. It never logs the token or payloads,
// only metadata and byte counts.
func (s *Server) logRequest(remote, tool string, ok bool, dur time.Duration, bytes int) {
	status := "ok"
	if !ok {
		status = "err"
	}
	if tool == "" {
		tool = "-"
	}
	s.logger.Printf("remote=%s tool=%s status=%s dur=%s bytes=%d",
		remote, tool, status, dur.Round(time.Millisecond), bytes)
}
