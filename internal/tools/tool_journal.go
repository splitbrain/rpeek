package tools

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// journal returns recent systemd journal lines by invoking journalctl with a fixed,
// validated argument vector.
type journal struct{ readOnly }

func init() { register(journal{}) }

// Name returns the subcommand name.
func (journal) Name() string { return "journal" }

// Summary returns the one-line help description.
func (journal) Summary() string { return "recent systemd journal lines, optionally for one unit" }

// Usage returns the argument synopsis.
func (journal) Usage() string { return "journal [--unit NAME] [--lines N]" }

// journalArgs are the wire arguments for the journal tool.
type journalArgs struct {
	// Unit optionally filters output to a single systemd unit. It is validated against a
	// strict allowlist before use.
	Unit string `json:"unit,omitempty"`

	// Lines is the number of trailing journal lines to return. Zero selects the default;
	// the server clamps it to a maximum.
	Lines int `json:"lines,omitempty"`
}

const (
	// journalDefaultLines is the default line count.
	journalDefaultLines = 100
	// journalMaxLines is the hard cap on lines.
	journalMaxLines = 10000
)

// unitPattern validates a systemd unit name before it is passed to journalctl.
var unitPattern = regexp.MustCompile(`^[a-zA-Z0-9@._-]+$`)

// NewFlags builds the journal flag set and its argument builder.
func (journal) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("journal", flag.ContinueOnError)
	unit := fs.String("unit", "", "systemd unit to filter by (validated allowlist)")
	lines := fs.Int("lines", 0, "number of trailing lines (default 100, cap 10000)")
	return fs, func(pos []string) (any, error) {
		if err := noPositionals("journal", pos); err != nil {
			return nil, err
		}
		return journalArgs{Unit: *unit, Lines: *lines}, nil
	}
}

// Remote returns recent journal lines. It never constructs a shell command string; the
// argument vector is always passed as discrete arguments.
func (journal) Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs[journalArgs](raw)
	if err != nil {
		return Result{}, err
	}
	if env.Journalctl == "" {
		return Result{}, fmt.Errorf("journalctl is not available on this host")
	}

	lines := clampLimit(args.Lines, journalDefaultLines, journalMaxLines)

	argv := []string{"--no-pager", "-n", strconv.Itoa(lines), "-o", "short-iso"}
	if args.Unit != "" {
		if !unitPattern.MatchString(args.Unit) {
			return Result{}, fmt.Errorf("invalid unit name")
		}
		argv = append(argv, "-u", args.Unit)
	}

	out, dropped, err := runJournalctl(ctx, env.Journalctl, argv, env.Limits.MaxOutput)
	if err != nil {
		return Result{}, err
	}

	capped, trunc := capOutput(string(out), env.Limits.MaxOutput)
	return Result{Output: capped, Truncated: dropped || trunc}, nil
}

// journalStderrCap bounds how much of journalctl's stderr is retained for error
// reporting. Diagnostic messages are short; the cap only guards against a runaway child.
const journalStderrCap = 64 << 10

// runJournalctl executes journalctl at path with the given fixed argument vector under
// ctx and returns its captured stdout together with whether that stdout was truncated at
// the output cap. The argument vector is always passed as discrete arguments, never
// interpolated into a shell string. Stdout collection is bounded near maxOutput (plus a
// UTF-8 rune of headroom so the eventual capOutput can trim on a rune boundary) so a
// host with very large journal messages cannot make the server buffer the entire stream
// before it is trimmed; a negative maxOutput leaves collection unbounded. On failure it
// returns the context error if the deadline fired, otherwise a message built from
// journalctl's stderr.
func runJournalctl(ctx context.Context, path string, argv []string, maxOutput int) ([]byte, bool, error) {
	stdoutCap := maxOutput
	if stdoutCap >= 0 {
		stdoutCap += utf8.UTFMax
	}
	cmd := exec.CommandContext(ctx, path, argv...)
	stdout := &capWriter{cap: stdoutCap}
	stderr := &capWriter{cap: journalStderrCap}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		if msg := strings.TrimSpace(stderr.buf.String()); msg != "" {
			return nil, false, fmt.Errorf("journalctl: %s", msg)
		}
		return nil, false, fmt.Errorf("journalctl: %w", err)
	}
	return stdout.buf.Bytes(), stdout.dropped, nil
}
