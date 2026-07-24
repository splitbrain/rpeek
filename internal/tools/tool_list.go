package tools

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
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

// listBatchSize bounds how many entries are read from the directory per syscall,
// so a directory with an enormous number of entries is never fully resident.
const listBatchSize = 1024

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

	f, err := os.Open(dir)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()

	// entry pairs a directory entry name with its resolved metadata for sorting.
	type entry struct {
		name string
		info os.FileInfo
	}

	// Read the directory in bounded batches so an enormous directory is never held
	// in memory all at once; stop as soon as the entry cap is reached.
	collected := make([]entry, 0, maxListEntries)
	truncated := false
readLoop:
	for {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		batch, readErr := f.ReadDir(listBatchSize)
		for _, e := range batch {
			name := e.Name()
			if !args.All && strings.HasPrefix(name, ".") {
				continue
			}
			if len(collected) >= maxListEntries {
				truncated = true
				break readLoop
			}
			fi, err := e.Info()
			if err != nil {
				continue
			}
			collected = append(collected, entry{name: name, info: fi})
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return Result{}, readErr
		}
	}

	// Batched reads arrive in directory order; sort for a stable, ls-style listing.
	sort.Slice(collected, func(i, k int) bool { return collected[i].name < collected[k].name })

	var b strings.Builder
	for _, e := range collected {
		fmt.Fprintf(&b, "%s %10d %s %s\n",
			e.info.Mode().String(),
			e.info.Size(),
			e.info.ModTime().Format(time.RFC3339),
			e.name,
		)
	}

	out, capTrunc := capOutput(b.String(), env.Limits.MaxOutput)
	return Result{Output: out, Truncated: truncated || capTrunc}, nil
}
