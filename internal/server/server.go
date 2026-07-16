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
	"log"
	"net"
	"sync"
	"time"

	"rpeek/internal/protocol"
)

// connReadTimeout bounds how long a connected client has to send its request and
// receive its response, so a client that connects but stalls cannot hold a goroutine.
const connReadTimeout = 30 * time.Second

// maxRequestLine bounds the size of a single request line the server will read.
const maxRequestLine = 1 << 20

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
}

// NewServer builds a Server from the tool runner, the shared auth token, and the logger.
func NewServer(runner ToolRunner, token string, logger *log.Logger) *Server {
	return &Server{runner: runner, token: token, logger: logger}
}

// Serve accepts connections on ln until ctx is cancelled, handling each in its own
// goroutine. When ctx is cancelled it stops accepting, waits for in-flight handlers,
// and returns.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			s.logger.Printf("accept error: %v", err)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
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
	_ = conn.SetReadDeadline(time.Now().Add(connReadTimeout))

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), maxRequestLine)
	if !scanner.Scan() {
		// Nothing readable; no meaningful response can be sent.
		s.logRequest(remote, "", false, time.Since(start), 0)
		return
	}

	var req protocol.Request
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		s.writeResponse(conn, protocol.Response{OK: false, Error: "invalid request"})
		s.logRequest(remote, "", false, time.Since(start), 0)
		return
	}

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
	_ = conn.SetWriteDeadline(time.Now().Add(connReadTimeout))
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
