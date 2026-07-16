package tools

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

// read returns up to a capped number of bytes of a regular file, optionally from a byte
// offset.
type read struct{ readOnly }

// Name returns the subcommand name.
func (read) Name() string { return "read" }

// Summary returns the one-line help description.
func (read) Summary() string { return "read a regular file, byte-capped, with optional paging" }

// Usage returns the argument synopsis.
func (read) Usage() string { return "read <path> [--max-bytes N] [--offset N]" }

// readArgs are the wire arguments for the read tool.
type readArgs struct {
	// Path is the real filesystem path of the regular file to read.
	Path string `json:"path"`

	// MaxBytes caps how many bytes are returned. Zero selects the server default; the
	// server clamps it to a hard maximum.
	MaxBytes int `json:"max_bytes,omitempty"`

	// Offset is the byte offset to start reading at, for simple paging.
	Offset int `json:"offset,omitempty"`
}

const (
	// readDefaultBytes is the default byte cap when the client asks for none.
	readDefaultBytes = 65536
	// readHardBytes is the hard byte cap regardless of the client request.
	readHardBytes = 1 << 20
)

// NewFlags builds the read flag set and its argument builder.
func (read) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	maxBytes := fs.Int("max-bytes", 0, "maximum bytes to read (default 65536, hard cap 1 MiB)")
	offset := fs.Int("offset", 0, "byte offset to start reading at")
	return fs, func(pos []string) (any, error) {
		path, err := singlePath("read", pos)
		if err != nil {
			return nil, err
		}
		return readArgs{Path: path, MaxBytes: *maxBytes, Offset: *offset}, nil
	}
}

// Run reads the resolved file. The byte cap is enforced with an io.LimitReader so that
// streaming pseudo-files cannot produce unbounded output.
func (read) Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs[readArgs](raw)
	if err != nil {
		return Result{}, err
	}
	path, err := env.Jail.ResolveFile(args.Path)
	if err != nil {
		return Result{}, err
	}
	if args.Offset < 0 {
		return Result{}, fmt.Errorf("offset must not be negative")
	}

	maxBytes := args.MaxBytes
	if maxBytes <= 0 {
		maxBytes = readDefaultBytes
	}
	if maxBytes > readHardBytes {
		maxBytes = readHardBytes
	}
	if maxBytes > env.Limits.MaxOutput {
		maxBytes = env.Limits.MaxOutput
	}

	f, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()

	if args.Offset > 0 {
		if _, err := f.Seek(int64(args.Offset), io.SeekStart); err != nil {
			return Result{}, err
		}
	}

	data, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)))
	if err != nil {
		return Result{}, err
	}

	// A successful single-byte read past the cap means more data remains.
	var probe [1]byte
	n, _ := f.Read(probe[:])
	return Result{Output: string(data), Truncated: n > 0}, nil
}
