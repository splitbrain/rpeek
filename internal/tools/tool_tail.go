package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"strings"
)

// tail returns the last N lines of a regular file.
type tail struct{ readOnly }

// Name returns the subcommand name.
func (tail) Name() string { return "tail" }

// Summary returns the one-line help description.
func (tail) Summary() string { return "show the last N lines of a file" }

// Usage returns the argument synopsis.
func (tail) Usage() string { return "tail <path> [--lines N]" }

// tailArgs are the wire arguments for the tail tool.
type tailArgs struct {
	// Path is the real filesystem path of the regular file to tail.
	Path string `json:"path"`

	// Lines is the number of trailing lines to return. Zero selects the default; the
	// server clamps it to a maximum.
	Lines int `json:"lines,omitempty"`
}

const (
	// tailDefaultLines is the default number of trailing lines.
	tailDefaultLines = 100
	// tailMaxLines is the hard cap on trailing lines.
	tailMaxLines = 10000
	// tailMaxScan bounds how many bytes tail reads from the end of a file, which also
	// protects against zero-length-but-streaming files under /proc.
	tailMaxScan = 8 << 20
)

// NewFlags builds the tail flag set and its argument builder.
func (tail) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("tail", flag.ContinueOnError)
	lines := fs.Int("lines", 0, "number of trailing lines (default 100, cap 10000)")
	return fs, func(pos []string) (any, error) {
		path, err := singlePath("tail", pos)
		if err != nil {
			return nil, err
		}
		return tailArgs{Path: path, Lines: *lines}, nil
	}
}

// Run returns the trailing lines of the resolved file, reading only the trailing window
// so that huge files do not force unbounded work.
func (tail) Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs[tailArgs](raw)
	if err != nil {
		return Result{}, err
	}
	path, err := env.Jail.ResolveFile(args.Path)
	if err != nil {
		return Result{}, err
	}

	lines := args.Lines
	if lines <= 0 {
		lines = tailDefaultLines
	}
	if lines > tailMaxLines {
		lines = tailMaxLines
	}

	f, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()

	// Read at most the last tailMaxScan bytes. For a file larger than that we skip the
	// first (likely partial) line to avoid emitting a fragment.
	var start int64
	if fi, err := f.Stat(); err == nil && fi.Size() > tailMaxScan {
		start = fi.Size() - tailMaxScan
	}
	dropFirst := start > 0
	if start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return Result{}, err
		}
	}

	sc := bufio.NewScanner(io.LimitReader(f, tailMaxScan))
	sc.Buffer(make([]byte, 0, 64*1024), scanLineCap)

	ring := make([]string, lines)
	count := 0
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if dropFirst {
			dropFirst = false
			continue
		}
		ring[count%lines] = sc.Text()
		count++
	}
	if err := sc.Err(); err != nil {
		return Result{}, err
	}

	n := lines
	if count < lines {
		n = count
	}
	first := count - n
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(ring[(first+i)%lines])
		b.WriteByte('\n')
	}

	out, capTrunc := capOutput(b.String(), env.Limits.MaxOutput)
	// start > 0 means older lines were skipped, which for tail is expected; only the
	// output-size cap constitutes truncation of the requested lines.
	return Result{Output: out, Truncated: capTrunc}, nil
}
