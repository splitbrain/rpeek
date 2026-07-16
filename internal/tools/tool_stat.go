package tools

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// stat reports metadata for a path as key: value lines.
type stat struct{ readOnly }

func init() { register(stat{}) }

// Name returns the subcommand name.
func (stat) Name() string { return "stat" }

// Summary returns the one-line help description.
func (stat) Summary() string { return "show path metadata (name, size, mode, modtime, uid, gid)" }

// Usage returns the argument synopsis.
func (stat) Usage() string { return "stat <path>" }

// statArgs are the wire arguments for the stat tool.
type statArgs struct {
	// Path is the real filesystem path to stat.
	Path string `json:"path"`
}

// NewFlags builds the stat flag set and its argument builder.
func (stat) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("stat", flag.ContinueOnError)
	return fs, func(pos []string) (any, error) {
		path, err := singlePath("stat", pos)
		if err != nil {
			return nil, err
		}
		return statArgs{Path: path}, nil
	}
}

// Run reports metadata for the resolved path. Because the jail resolves symlinks, the
// reported metadata is that of the resolved target.
func (stat) Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs[statArgs](raw)
	if err != nil {
		return Result{}, err
	}
	real, err := env.Jail.Resolve(args.Path)
	if err != nil {
		return Result{}, err
	}
	info, err := os.Stat(real)
	if err != nil {
		return Result{}, err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "name:    %s\n", filepath.Base(real))
	fmt.Fprintf(&b, "path:    %s\n", real)
	fmt.Fprintf(&b, "size:    %d\n", info.Size())
	fmt.Fprintf(&b, "mode:    %s\n", info.Mode())
	fmt.Fprintf(&b, "modtime: %s\n", info.ModTime().Format(time.RFC3339))
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		fmt.Fprintf(&b, "uid:     %d\n", st.Uid)
		fmt.Fprintf(&b, "gid:     %d\n", st.Gid)
	}

	out, capTrunc := capOutput(b.String(), env.Limits.MaxOutput)
	return Result{Output: out, Truncated: capTrunc}, nil
}
