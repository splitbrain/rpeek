// Package protocol defines the newline-delimited JSON wire contract shared by the diag
// server and client. A client sends exactly one Request and reads exactly one Response
// per connection. The tool-specific argument types live with their tools; the wire
// carries them as opaque raw JSON.
package protocol

import "encoding/json"

// Request is the single message a client sends per connection. It carries the
// authentication token, the name of the tool to run, and the tool-specific arguments.
type Request struct {
	// Token is the shared secret authenticating the caller. It must match the token
	// the server generated or was configured with.
	Token string `json:"token"`

	// Tool names the read-only diagnostic tool to invoke, e.g. "grep" or "ps".
	Tool string `json:"tool"`

	// Args holds the tool-specific arguments as raw JSON, decoded server-side into
	// the matching argument type. Empty for tools that take no arguments.
	Args json.RawMessage `json:"args,omitempty"`
}

// Response is the single message the server sends back per connection. Every tool
// produces plain text in Output formatted the way the equivalent command-line tool
// would print it; the client relays Output verbatim.
type Response struct {
	// OK reports whether the tool ran successfully. When false, Error explains why.
	OK bool `json:"ok"`

	// Output is the tool's text result, already formatted server-side.
	Output string `json:"output,omitempty"`

	// Truncated is true when the output was capped by a size, match, or entry limit.
	Truncated bool `json:"truncated,omitempty"`

	// Error is a human-readable message set when OK is false.
	Error string `json:"error,omitempty"`
}
