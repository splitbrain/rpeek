// Package tools defines the read-only diagnostic tools shared by the rpeek server and
// the rpeek client. Each tool is a self-contained type that owns both faces of its
// operation: the client-side CLI flags that build its wire arguments, and the
// server-side execution that produces its output.
package tools

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"time"
)

// Tool is one read-only diagnostic operation. A single implementation owns both faces:
// the client-side flag parsing that builds its wire arguments and the server-side
// execution that produces its output. Concrete tools are registered in All.
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

	// NewFlags is the client face: it returns a fresh flag set plus a builder that turns
	// the parsed positional arguments into this tool's wire argument value. The returned
	// value is marshalled to JSON and carried in the request.
	NewFlags() (*flag.FlagSet, func(pos []string) (any, error))

	// Run is the server face: it decodes the raw wire arguments, executes the tool under
	// the context and environment, and returns the result.
	Run(ctx context.Context, env Env, raw json.RawMessage) (Result, error)
}

// LocalTool is an optional capability a Tool implements when it can also produce its
// result in the client process, without contacting a server. The client runs Local for
// the local answer and, when a server is addressed, additionally calls Run over the wire
// for the remote one. Only tools reporting the binary's own identity should implement it;
// a host-state tool run locally would silently inspect the operator's machine instead of
// the target host, so those must remain server-only.
type LocalTool interface {
	Tool

	// Local produces the tool's result in the calling process, using no server, jail, or
	// arguments.
	Local() (Result, error)
}

// Env carries the server-side dependencies a tool may draw on. A tool ignores the
// fields it does not need.
type Env struct {
	// Jail is the set of roots that file-addressing tools may read within.
	Jail *JailSet

	// Limits bounds tool output size and run time.
	Limits Limits

	// Journalctl is the resolved journalctl path, or "" when it is unavailable.
	Journalctl string
}

// Result is the non-error outcome of Run.
type Result struct {
	// Output is the tool's text result, already formatted server-side.
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
