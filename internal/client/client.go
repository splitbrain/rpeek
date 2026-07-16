// Package client implements the single request/response round trip that the rpeek client
// performs against an rpeek server over TLS.
package client

import (
	"bufio"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"rpeek/internal/protocol"
	"rpeek/internal/tlsutil"
)

// callTimeout bounds the whole dial/write/read round trip.
const callTimeout = 60 * time.Second

// maxResponseLine bounds the size of the single response line the client will read.
// It exceeds the server's default output cap to leave headroom for JSON escaping.
const maxResponseLine = 16 << 20

// Call dials the rpeek server at host over TLS, sends one request for the named tool
// with the given token and arguments, reads one response, and returns it. Args is
// marshalled to JSON; pass nil for tools that take no arguments. The returned error is
// non-nil only for transport or protocol failures — a tool error reported by the
// server is carried in the Response with OK set to false.
func Call(host, token, tool string, args any) (*protocol.Response, error) {
	var raw json.RawMessage
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	req := protocol.Request{Token: token, Tool: tool, Args: raw}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	payload = append(payload, '\n')

	conn, err := tls.Dial("tcp", host, tlsutil.ClientTLSConfig())
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(callTimeout))

	if _, err := conn.Write(payload); err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), maxResponseLine)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("no response from server")
	}

	var resp protocol.Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	return &resp, nil
}
