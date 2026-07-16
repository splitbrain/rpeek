// Package server implements the diagd read-only diagnostic server: the accept loop,
// per-connection authentication and dispatch, the path jail, and the tools.
package server

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os/exec"
	"sync"
	"time"

	"diag/internal/protocol"
)

// connReadTimeout bounds how long a connected client has to send its request and
// receive its response, so a client that connects but stalls cannot hold a goroutine.
const connReadTimeout = 30 * time.Second

// maxRequestLine bounds the size of a single request line the server will read.
const maxRequestLine = 1 << 20

// Server holds the runtime configuration for a diagd instance.
type Server struct {
	// jail is the set of roots that file-addressing tools may read within.
	jail *JailSet
	// token is the shared secret every request must present.
	token string
	// limits bounds tool output size and run time.
	limits Limits
	// journalctlPath is the resolved path to journalctl, or "" if unavailable.
	journalctlPath string
	// logger writes one audit line per request to stderr.
	logger *log.Logger
}

// NewServer builds a Server. It resolves journalctl once at construction; if it is not
// present the journal tool reports a clean error at call time.
func NewServer(jail *JailSet, token string, limits Limits, logger *log.Logger) *Server {
	path, _ := exec.LookPath("journalctl")
	return &Server{
		jail:           jail,
		token:          token,
		limits:         limits,
		journalctlPath: path,
		logger:         logger,
	}
}

// dispatchEntry describes one dispatchable tool.
type dispatchEntry struct {
	// ReadOnly marks the tool as non-mutating. Every MVP tool is read-only; the field
	// is a seam so a future --allow-write flag can gate write tools without a rewrite.
	ReadOnly bool
	// run decodes the raw arguments and executes the tool, returning its text output,
	// whether the output was truncated, and any error.
	run func(ctx context.Context, s *Server, raw json.RawMessage) (string, bool, error)
}

// decodeArgs unmarshals raw tool arguments into a value of type T. Empty raw arguments
// yield the zero value of T.
func decodeArgs[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, fmt.Errorf("invalid arguments: %w", err)
	}
	return v, nil
}

// dispatch maps a tool name to its handler. Adding a tool is a single new entry.
var dispatch = map[string]dispatchEntry{
	"list": {ReadOnly: true, run: func(ctx context.Context, s *Server, raw json.RawMessage) (string, bool, error) {
		args, err := decodeArgs[protocol.ListArgs](raw)
		if err != nil {
			return "", false, err
		}
		return toolList(ctx, s.jail, s.limits, args)
	}},
	"read": {ReadOnly: true, run: func(ctx context.Context, s *Server, raw json.RawMessage) (string, bool, error) {
		args, err := decodeArgs[protocol.ReadArgs](raw)
		if err != nil {
			return "", false, err
		}
		return toolRead(ctx, s.jail, s.limits, args)
	}},
	"grep": {ReadOnly: true, run: func(ctx context.Context, s *Server, raw json.RawMessage) (string, bool, error) {
		args, err := decodeArgs[protocol.GrepArgs](raw)
		if err != nil {
			return "", false, err
		}
		return toolGrep(ctx, s.jail, s.limits, args)
	}},
	"tail": {ReadOnly: true, run: func(ctx context.Context, s *Server, raw json.RawMessage) (string, bool, error) {
		args, err := decodeArgs[protocol.TailArgs](raw)
		if err != nil {
			return "", false, err
		}
		return toolTail(ctx, s.jail, s.limits, args)
	}},
	"stat": {ReadOnly: true, run: func(ctx context.Context, s *Server, raw json.RawMessage) (string, bool, error) {
		args, err := decodeArgs[protocol.StatArgs](raw)
		if err != nil {
			return "", false, err
		}
		return toolStat(ctx, s.jail, s.limits, args)
	}},
	"ps": {ReadOnly: true, run: func(ctx context.Context, s *Server, raw json.RawMessage) (string, bool, error) {
		if _, err := decodeArgs[protocol.PSArgs](raw); err != nil {
			return "", false, err
		}
		return toolPS(ctx, s.limits)
	}},
	"disk": {ReadOnly: true, run: func(ctx context.Context, s *Server, raw json.RawMessage) (string, bool, error) {
		if _, err := decodeArgs[protocol.DiskArgs](raw); err != nil {
			return "", false, err
		}
		return toolDisk(ctx, s.limits)
	}},
	"journal": {ReadOnly: true, run: func(ctx context.Context, s *Server, raw json.RawMessage) (string, bool, error) {
		args, err := decodeArgs[protocol.JournalArgs](raw)
		if err != nil {
			return "", false, err
		}
		return toolJournal(ctx, s.limits, s.journalctlPath, args)
	}},
}

// ToolNames returns the dispatchable tool names in a fixed display order.
func ToolNames() []string {
	return []string{"list", "read", "grep", "tail", "stat", "ps", "disk", "journal"}
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

	entry, ok := dispatch[req.Tool]
	if !ok {
		s.writeResponse(conn, protocol.Response{OK: false, Error: fmt.Sprintf("unknown tool: %q", req.Tool)})
		s.logRequest(remote, req.Tool, false, time.Since(start), 0)
		return
	}

	tctx, cancel := context.WithTimeout(ctx, s.limits.Timeout)
	defer cancel()

	output, truncated, err := entry.run(tctx, s, req.Args)
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
