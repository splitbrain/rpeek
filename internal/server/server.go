// Package server implements the diag serve accept loop: the listener, per-connection
// authentication, the request/response envelope, and dispatch to the tools registry.
// The tools themselves and the path jail live in the tools package.
package server

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"diag/internal/protocol"
	"diag/internal/tools"
)

// connReadTimeout bounds how long a connected client has to send its request and
// receive its response, so a client that connects but stalls cannot hold a goroutine.
const connReadTimeout = 30 * time.Second

// maxRequestLine bounds the size of a single request line the server will read.
const maxRequestLine = 1 << 20

// Server holds the runtime configuration for a diag serve instance.
type Server struct {
	// env carries the shared tool dependencies (jail, limits, journalctl path) reused
	// for every request.
	env tools.Env
	// token is the shared secret every request must present.
	token string
	// logger writes one audit line per request to stderr.
	logger *log.Logger
}

// NewServer builds a Server from the jail, token, limits, resolved journalctl path, and
// logger, assembling the tools.Env reused for every request. An empty journalctlPath
// makes the journal tool report a clean error at call time.
func NewServer(jail *tools.JailSet, token string, limits tools.Limits, journalctlPath string, logger *log.Logger) *Server {
	return &Server{
		env:    tools.Env{Jail: jail, Limits: limits, Journalctl: journalctlPath},
		token:  token,
		logger: logger,
	}
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

// handleConn reads one request, authenticates it, dispatches the tool under a timeout,
// writes one response, and closes the connection.
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

	tool, ok := tools.Lookup(req.Tool)
	if !ok {
		s.writeResponse(conn, protocol.Response{OK: false, Error: fmt.Sprintf("unknown tool: %q", req.Tool)})
		s.logRequest(remote, req.Tool, false, time.Since(start), 0)
		return
	}

	// ReadOnly seam: every tool is read-only today, so no gating is applied. A future
	// --allow-write flag would reject a non-read-only tool here.
	_ = tool.ReadOnly()

	tctx, cancel := context.WithTimeout(ctx, s.env.Limits.Timeout)
	defer cancel()

	res, err := tool.Run(tctx, s.env, req.Args)
	var resp protocol.Response
	if err != nil {
		resp = protocol.Response{OK: false, Error: err.Error()}
	} else {
		resp = protocol.Response{OK: true, Output: res.Output, Truncated: res.Truncated}
	}
	s.writeResponse(conn, resp)
	s.logRequest(remote, req.Tool, err == nil, time.Since(start), len(res.Output))
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
