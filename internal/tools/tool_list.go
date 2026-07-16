package tools

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// list lists a directory in an ls -l style, one entry per line.
type list struct{ readOnly }

func init() { register(list{}) }

// Name returns the subcommand name.
func (list) Name() string { return "list" }

// Summary returns the one-line help description.
func (list) Summary() string { return "list a directory (ls -l style); skips dotfiles unless --all" }

// Usage returns the argument synopsis.
func (list) Usage() string { return "list <path> [--all]" }

// listArgs are the wire arguments for the list tool.
type listArgs struct {
	// Path is the real filesystem path of the directory to list. It must resolve within
	// an allowed jail root.
	Path string `json:"path"`

	// All includes dotfiles in the listing when true.
	All bool `json:"all,omitempty"`
}

// maxListEntries bounds how many directory entries list returns.
const maxListEntries = 10000

// NewFlags builds the list flag set and its argument builder.
func (list) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	all := fs.Bool("all", false, "include dotfiles")
	return fs, func(pos []string) (any, error) {
		path, err := singlePath("list", pos)
		if err != nil {
			return nil, err
		}
		return listArgs{Path: path, All: *all}, nil
	}
}

// Run lists the resolved directory, honoring the dotfile filter and entry cap.
func (list) Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs[listArgs](raw)
	if err != nil {
		return Result{}, err
	}
	dir, err := env.Jail.Resolve(args.Path)
	if err != nil {
		return Result{}, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return Result{}, err
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("%s: not a directory", args.Path)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return Result{}, err
	}

	var b strings.Builder
	truncated := false
	count := 0
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		name := e.Name()
		if !args.All && strings.HasPrefix(name, ".") {
			continue
		}
		if count >= maxListEntries {
			truncated = true
			break
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%s %10d %s %s\n",
			fi.Mode().String(),
			fi.Size(),
			fi.ModTime().Format(time.RFC3339),
			name,
		)
		count++
	}

	out, capTrunc := capOutput(b.String(), env.Limits.MaxOutput)
	return Result{Output: out, Truncated: truncated || capTrunc}, nil
}
