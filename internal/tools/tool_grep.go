package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// grep searches a file, or a directory tree, for lines matching an RE2 pattern.
type grep struct{ readOnly }

func init() { register(grep{}) }

// Name returns the subcommand name.
func (grep) Name() string { return "grep" }

// Summary returns the one-line help description.
func (grep) Summary() string {
	return "search a file or directory tree for an RE2 pattern (grep -n style)"
}

// Usage returns the argument synopsis.
func (grep) Usage() string { return "grep <path> --pattern RE [--ignore-case] [--max-matches N]" }

// grepArgs are the wire arguments for the grep tool.
type grepArgs struct {
	// Path is a file or directory to search. Directories are searched recursively.
	Path string `json:"path"`

	// Pattern is an RE2 (Go regexp) pattern, not a shell glob.
	Pattern string `json:"pattern"`

	// IgnoreCase makes the pattern case-insensitive when true.
	IgnoreCase bool `json:"ignore_case,omitempty"`

	// MaxMatches caps the number of matching lines returned. Zero selects the default.
	MaxMatches int `json:"max_matches,omitempty"`
}

// grepDefaultMatches is the default cap on matching lines.
const grepDefaultMatches = 1000

// NewFlags builds the grep flag set and its argument builder.
func (grep) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("grep", flag.ContinueOnError)
	pattern := fs.String("pattern", "", "RE2 (Go regexp) pattern to search for; required")
	ignoreCase := fs.Bool("ignore-case", false, "case-insensitive match")
	maxMatches := fs.Int("max-matches", 0, "maximum matching lines (default 1000)")
	return fs, func(pos []string) (any, error) {
		path, err := singlePath("grep", pos)
		if err != nil {
			return nil, err
		}
		if *pattern == "" {
			return nil, fmt.Errorf("grep requires --pattern")
		}
		return grepArgs{Path: path, Pattern: *pattern, IgnoreCase: *ignoreCase, MaxMatches: *maxMatches}, nil
	}
}

// Run searches the resolved target and returns matches in grep -n style:
// "path:line: text".
func (grep) Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs[grepArgs](raw)
	if err != nil {
		return Result{}, err
	}
	if args.Pattern == "" {
		return Result{}, fmt.Errorf("pattern must not be empty")
	}
	pattern := args.Pattern
	if args.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return Result{}, fmt.Errorf("invalid pattern: %w", err)
	}

	maxMatches := args.MaxMatches
	if maxMatches <= 0 {
		maxMatches = grepDefaultMatches
	}

	target, err := env.Jail.Resolve(args.Path)
	if err != nil {
		return Result{}, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return Result{}, err
	}

	var b strings.Builder
	matches := 0
	truncated := false

	// errStopScan is a sentinel that unwinds a directory walk once the match cap or the
	// deadline is reached.
	errStopScan := fmt.Errorf("stop scan")

	scanFile := func(path string) error {
		f, err := os.Open(path)
		if err != nil {
			return nil // skip unreadable files
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), scanLineCap)
		line := 0
		for sc.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			line++
			text := sc.Text()
			if re.MatchString(text) {
				if matches >= maxMatches {
					truncated = true
					return errStopScan
				}
				fmt.Fprintf(&b, "%s:%d: %s\n", path, line, text)
				matches++
			}
		}
		return nil
	}

	if info.IsDir() {
		err = filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable subtrees
			}
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return nil // skip symlinks, devices, sockets, FIFOs
			}
			return scanFile(path)
		})
	} else {
		if !info.Mode().IsRegular() {
			return Result{}, fmt.Errorf("%s: not a regular file", args.Path)
		}
		err = scanFile(target)
	}
	if err != nil && err != errStopScan {
		return Result{}, err
	}

	out, capTrunc := capOutput(b.String(), env.Limits.MaxOutput)
	return Result{Output: out, Truncated: truncated || capTrunc}, nil
}
