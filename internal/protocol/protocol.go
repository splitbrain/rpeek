// Package protocol defines the newline-delimited JSON wire contract shared by the
// diagd server and the diagctl client. A client sends exactly one Request and reads
// exactly one Response per connection.
package protocol

import "encoding/json"

// Request is the single message a client sends per connection. It carries the
// authentication token, the name of the tool to run, and the tool-specific arguments.
type Request struct {
	// Token is the shared secret authenticating the caller. It must match the token
	// the server generated or was configured with.
	Token string `json:"token"`

	// Tool names the read-only diagnostic tool to invoke. One of:
	// "list", "read", "grep", "tail", "stat", "ps", "disk", "journal".
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

// ListArgs are the arguments for the "list" tool.
type ListArgs struct {
	// Path is the real filesystem path of the directory to list. It must resolve
	// within an allowed jail root.
	Path string `json:"path"`

	// All includes dotfiles in the listing when true.
	All bool `json:"all,omitempty"`
}

// ReadArgs are the arguments for the "read" tool.
type ReadArgs struct {
	// Path is the real filesystem path of the regular file to read.
	Path string `json:"path"`

	// MaxBytes caps how many bytes are returned. Zero selects the server default;
	// the server clamps it to a hard maximum.
	MaxBytes int `json:"max_bytes,omitempty"`

	// Offset is the byte offset to start reading at, for simple paging.
	Offset int `json:"offset,omitempty"`
}

// GrepArgs are the arguments for the "grep" tool.
type GrepArgs struct {
	// Path is a file or directory to search. Directories are searched recursively.
	Path string `json:"path"`

	// Pattern is an RE2 (Go regexp) pattern, not a shell glob.
	Pattern string `json:"pattern"`

	// IgnoreCase makes the pattern case-insensitive when true.
	IgnoreCase bool `json:"ignore_case,omitempty"`

	// MaxMatches caps the number of matching lines returned. Zero selects the
	// server default.
	MaxMatches int `json:"max_matches,omitempty"`
}

// TailArgs are the arguments for the "tail" tool.
type TailArgs struct {
	// Path is the real filesystem path of the regular file to tail.
	Path string `json:"path"`

	// Lines is the number of trailing lines to return. Zero selects the default of
	// 100; the server clamps it to a maximum.
	Lines int `json:"lines,omitempty"`
}

// StatArgs are the arguments for the "stat" tool.
type StatArgs struct {
	// Path is the real filesystem path to stat.
	Path string `json:"path"`
}

// PSArgs are the arguments for the "ps" tool. It takes no arguments in the MVP.
type PSArgs struct{}

// DiskArgs are the arguments for the "disk" tool. It takes no arguments in the MVP.
type DiskArgs struct{}

// JournalArgs are the arguments for the "journal" tool.
type JournalArgs struct {
	// Unit optionally filters output to a single systemd unit. It is validated
	// against a strict allowlist before use.
	Unit string `json:"unit,omitempty"`

	// Lines is the number of trailing journal lines to return. Zero selects the
	// default of 100; the server clamps it to [1, 10000].
	Lines int `json:"lines,omitempty"`
}
