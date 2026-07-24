package tools

import (
	"container/heap"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// Remote lists the resolved directory, honoring the dotfile filter and entry cap.
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

	// Scan the whole directory in bounded batches, retaining only the
	// maxListEntries lexicographically smallest names in a bounded heap. This
	// never holds the full directory in memory yet, unlike stopping early in
	// filesystem order, yields the true alphabetical prefix when the cap is hit.
	kept := &nameHeap{}
	truncated := false
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
			switch {
			case kept.Len() < maxListEntries:
				heap.Push(kept, name)
			case name < (*kept)[0]:
				// This name sorts before the largest one kept so far; evict
				// the largest by overwriting the root and resifting.
				(*kept)[0] = name
				heap.Fix(kept, 0)
				truncated = true
			default:
				truncated = true
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return Result{}, readErr
		}
	}

	// The heap holds the selected names in heap order; sort them, then stat each
	// survivor individually so only one entry's metadata is resident at a time.
	names := []string(*kept)
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		fi, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%s %10d %s %s\n",
			fi.Mode().String(),
			fi.Size(),
			fi.ModTime().Format(time.RFC3339),
			name,
		)
	}

	out, capTrunc := capOutput(b.String(), env.Limits.MaxOutput)
	return Result{Output: out, Truncated: truncated || capTrunc}, nil
}

// nameHeap is a max-heap of directory entry names. Keeping a bounded heap of
// size maxListEntries while scanning retains the lexicographically smallest
// names seen, so a truncated listing is the true alphabetical prefix of the
// directory rather than an arbitrary subset in filesystem order.
type nameHeap []string

// Len reports the number of names currently in the heap.
func (h nameHeap) Len() int { return len(h) }

// Less orders the largest name to the root so it is the first to be evicted.
func (h nameHeap) Less(i, k int) bool { return h[i] > h[k] }

// Swap exchanges the names at indices i and k.
func (h nameHeap) Swap(i, k int) { h[i], h[k] = h[k], h[i] }

// Push appends a name to the heap backing slice.
func (h *nameHeap) Push(x any) { *h = append(*h, x.(string)) }

// Pop removes and returns the last name in the heap backing slice.
func (h *nameHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
