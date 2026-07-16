// Package tools defines the rpeek subcommands: the read-only diagnostic tools plus the
// serve tool that stands up the server. Each is a self-contained type carrying its flag
// parsing and whichever execution halves it supports — Local (in the client), Remote (on
// the server), or Serve (the server process). The package supplies the Runner the server
// dispatches through, so it imports the server package rather than the reverse.
package tools

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"
)

// Tool is one read-only diagnostic operation's identity and argument parsing. Execution
// is a separate concern: a tool runs on the server if it implements RemoteTool, in the
// client if it implements LocalTool, and both if it implements both. Every tool
// implements at least one. Concrete tools are registered in All.
type Tool interface {
	// Name returns the subcommand name, e.g. "grep".
	Name() string

	// Summary returns a one-line description shown in help listings.
	Summary() string

	// Usage returns the argument synopsis, e.g. "grep <path> --pattern RE".
	Usage() string

	// ReadOnly reports whether the tool never mutates state. Every tool is read-only
	// today; the method is a seam a future --allow-write flag can gate write tools on.
	ReadOnly() bool

	// NewFlags returns a fresh flag set plus a builder that turns the parsed positional
	// arguments into this tool's wire argument value. The returned value is marshalled to
	// JSON and passed to Local or carried in the request to Remote.
	NewFlags() (*flag.FlagSet, func(pos []string) (any, error))
}

// RemoteTool is a Tool that executes on the server, reached over the wire. Most tools
// implement it: they read host state the client cannot see. The server decodes the raw
// wire arguments, runs Remote under the request context and environment, and returns the
// result to the client.
type RemoteTool interface {
	Tool

	// Remote executes the tool on the server and returns its result.
	Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error)
}

// LocalTool is a Tool that executes in the client process, with the same signature as
// RemoteTool.Remote. A tool implements it when it can answer without a server — either
// because the answer is the client binary's own (version) or purely static (help). Tools
// that read host state must not implement it: run locally they would silently inspect the
// operator's own machine instead of the target host.
type LocalTool interface {
	Tool

	// Local executes the tool in the calling process and returns its result. The Env it
	// receives is client-supplied; it carries no jail.
	Local(ctx context.Context, env Env, raw json.RawMessage) (Result, error)
}

// ServerMode is a Tool whose execution is the server process itself, rather than a
// one-shot Local/Remote result. Only serve implements it: it builds the jail and TLS
// listener from its arguments, prints a banner to stdout, and serves until ctx is
// cancelled. It is neither a LocalTool nor a RemoteTool.
type ServerMode interface {
	Tool

	// Serve runs the server until ctx is cancelled, decoding raw for its configuration
	// (bind address, token, roots, limits) and writing its startup banner to stdout.
	Serve(ctx context.Context, raw json.RawMessage, stdout io.Writer) error
}

// Env carries the dependencies a tool may draw on when it executes. On the server it is
// fully populated; a client-supplied Env for Local carries no jail. A tool ignores the
// fields it does not need.
type Env struct {
	// Jail is the set of roots that file-addressing tools may read within.
	Jail *JailSet

	// Limits bounds tool output size and run time.
	Limits Limits

	// Journalctl is the resolved journalctl path, or "" when it is unavailable.
	Journalctl string
}

// Result is the non-error outcome of a tool's execution.
type Result struct {
	// Output is the tool's text result, already formatted.
	Output string

	// Truncated reports whether the output was capped by a size, match, or entry limit.
	Truncated bool
}

// Limits bounds the work and output size of every tool.
type Limits struct {
	// MaxOutput is the maximum number of bytes a tool may return before its output is
	// truncated and Result.Truncated is set.
	MaxOutput int

	// Timeout is the per-tool wall-clock deadline, enforced through the context.
	Timeout time.Duration
}

// readOnly is embedded by every tool to supply ReadOnly without per-tool boilerplate.
type readOnly struct{}

// ReadOnly reports that the embedding tool never mutates state.
func (readOnly) ReadOnly() bool { return true }

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
