package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
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

// Run returns recent journal lines. It never constructs a shell command string; the
// argument vector is always passed as discrete arguments.
func (journal) Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs[journalArgs](raw)
	if err != nil {
		return Result{}, err
	}
	if env.Journalctl == "" {
		return Result{}, fmt.Errorf("journalctl is not available on this host")
	}

	lines := args.Lines
	if lines <= 0 {
		lines = journalDefaultLines
	}
	if lines > journalMaxLines {
		lines = journalMaxLines
	}

	argv := []string{"--no-pager", "-n", strconv.Itoa(lines), "-o", "short-iso"}
	if args.Unit != "" {
		if !unitPattern.MatchString(args.Unit) {
			return Result{}, fmt.Errorf("invalid unit name")
		}
		argv = append(argv, "-u", args.Unit)
	}

	out, err := runJournalctl(ctx, env.Journalctl, argv)
	if err != nil {
		return Result{}, err
	}

	capped, trunc := capOutput(string(out), env.Limits.MaxOutput)
	return Result{Output: capped, Truncated: trunc}, nil
}

// runJournalctl executes journalctl at path with the given fixed argument vector under
// ctx and returns its captured stdout. The argument vector is always passed as discrete
// arguments, never interpolated into a shell string. On failure it returns the context
// error if the deadline fired, otherwise a message built from journalctl's stderr.
func runJournalctl(ctx context.Context, path string, argv []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, argv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("journalctl: %s", msg)
		}
		return nil, fmt.Errorf("journalctl: %w", err)
	}
	return stdout.Bytes(), nil
}
