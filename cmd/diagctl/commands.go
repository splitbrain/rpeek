package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"diag/internal/protocol"
)

// toolSpec describes one diagctl subcommand: its human-readable help and a factory that
// builds a fresh flag set plus a function turning the parsed positional arguments into
// the protocol argument value for the call.
type toolSpec struct {
	// summary is a one-line description shown in the tool list.
	summary string
	// usage is the argument synopsis, e.g. "read <path> [--max-bytes N]".
	usage string
	// newParser registers this tool's flags on a fresh flag set and returns it together
	// with a builder that consumes the parsed positional arguments and flag values.
	newParser func() (*flag.FlagSet, func(positionals []string) (any, error))
}

// toolOrder is the fixed display order of the tools.
var toolOrder = []string{"list", "read", "grep", "tail", "stat", "ps", "disk", "journal"}

// tools is the registry mapping a subcommand name to its specification. It is the single
// source of truth for the client's tools and their help.
var tools = map[string]toolSpec{
	"list": {
		summary: "list a directory (ls -l style); skips dotfiles unless --all",
		usage:   "list <path> [--all]",
		newParser: func() (*flag.FlagSet, func([]string) (any, error)) {
			fs := flag.NewFlagSet("list", flag.ContinueOnError)
			all := fs.Bool("all", false, "include dotfiles")
			return fs, func(pos []string) (any, error) {
				path, err := singlePath("list", pos)
				if err != nil {
					return nil, err
				}
				return protocol.ListArgs{Path: path, All: *all}, nil
			}
		},
	},
	"read": {
		summary: "read a regular file, byte-capped, with optional paging",
		usage:   "read <path> [--max-bytes N] [--offset N]",
		newParser: func() (*flag.FlagSet, func([]string) (any, error)) {
			fs := flag.NewFlagSet("read", flag.ContinueOnError)
			maxBytes := fs.Int("max-bytes", 0, "maximum bytes to read (default 65536, hard cap 1 MiB)")
			offset := fs.Int("offset", 0, "byte offset to start reading at")
			return fs, func(pos []string) (any, error) {
				path, err := singlePath("read", pos)
				if err != nil {
					return nil, err
				}
				return protocol.ReadArgs{Path: path, MaxBytes: *maxBytes, Offset: *offset}, nil
			}
		},
	},
	"grep": {
		summary: "search a file or directory tree for an RE2 pattern (grep -n style)",
		usage:   "grep <path> --pattern RE [--ignore-case] [--max-matches N]",
		newParser: func() (*flag.FlagSet, func([]string) (any, error)) {
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
				return protocol.GrepArgs{Path: path, Pattern: *pattern, IgnoreCase: *ignoreCase, MaxMatches: *maxMatches}, nil
			}
		},
	},
	"tail": {
		summary: "show the last N lines of a file",
		usage:   "tail <path> [--lines N]",
		newParser: func() (*flag.FlagSet, func([]string) (any, error)) {
			fs := flag.NewFlagSet("tail", flag.ContinueOnError)
			lines := fs.Int("lines", 0, "number of trailing lines (default 100, cap 10000)")
			return fs, func(pos []string) (any, error) {
				path, err := singlePath("tail", pos)
				if err != nil {
					return nil, err
				}
				return protocol.TailArgs{Path: path, Lines: *lines}, nil
			}
		},
	},
	"stat": {
		summary: "show path metadata (name, size, mode, modtime, uid, gid)",
		usage:   "stat <path>",
		newParser: func() (*flag.FlagSet, func([]string) (any, error)) {
			fs := flag.NewFlagSet("stat", flag.ContinueOnError)
			return fs, func(pos []string) (any, error) {
				path, err := singlePath("stat", pos)
				if err != nil {
					return nil, err
				}
				return protocol.StatArgs{Path: path}, nil
			}
		},
	},
	"ps": {
		summary: "process snapshot from /proc (PID PPID USER RSS CMD)",
		usage:   "ps",
		newParser: func() (*flag.FlagSet, func([]string) (any, error)) {
			fs := flag.NewFlagSet("ps", flag.ContinueOnError)
			return fs, func(pos []string) (any, error) {
				if err := noPositionals("ps", pos); err != nil {
					return nil, err
				}
				return protocol.PSArgs{}, nil
			}
		},
	},
	"disk": {
		summary: "filesystem usage (df style)",
		usage:   "disk",
		newParser: func() (*flag.FlagSet, func([]string) (any, error)) {
			fs := flag.NewFlagSet("disk", flag.ContinueOnError)
			return fs, func(pos []string) (any, error) {
				if err := noPositionals("disk", pos); err != nil {
					return nil, err
				}
				return protocol.DiskArgs{}, nil
			}
		},
	},
	"journal": {
		summary: "recent systemd journal lines, optionally for one unit",
		usage:   "journal [--unit NAME] [--lines N]",
		newParser: func() (*flag.FlagSet, func([]string) (any, error)) {
			fs := flag.NewFlagSet("journal", flag.ContinueOnError)
			unit := fs.String("unit", "", "systemd unit to filter by (validated allowlist)")
			lines := fs.Int("lines", 0, "number of trailing lines (default 100, cap 10000)")
			return fs, func(pos []string) (any, error) {
				if err := noPositionals("journal", pos); err != nil {
					return nil, err
				}
				return protocol.JournalArgs{Unit: *unit, Lines: *lines}, nil
			}
		},
	},
}

// buildToolArgs parses a tool's arguments (flags and a positional path may appear in any
// order) and builds its protocol argument value. It returns flag.ErrHelp when the caller
// requested -h/--help.
func buildToolArgs(spec toolSpec, args []string) (any, error) {
	fs, build := spec.newParser()
	fs.SetOutput(io.Discard) // suppress the flag package's own -h output; we render our own
	positionals, err := parseFlagsAnyOrder(fs, args)
	if err != nil {
		return nil, err
	}
	return build(positionals)
}

// parseFlagsAnyOrder parses args against fs, allowing flags and positional arguments to
// be interleaved in any order, and returns the positional arguments in the order seen.
// It stops and returns flag.ErrHelp if -h/--help is encountered.
func parseFlagsAnyOrder(fs *flag.FlagSet, args []string) ([]string, error) {
	var positionals []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rem := fs.Args()
		if len(rem) == 0 {
			return positionals, nil
		}
		positionals = append(positionals, rem[0])
		args = rem[1:]
	}
}

// singlePath requires exactly one positional argument and returns it.
func singlePath(tool string, pos []string) (string, error) {
	switch len(pos) {
	case 0:
		return "", fmt.Errorf("%s requires a path", tool)
	case 1:
		return pos[0], nil
	default:
		return "", fmt.Errorf("%s takes a single path, got %d arguments", tool, len(pos))
	}
}

// noPositionals requires that no positional arguments were given.
func noPositionals(tool string, pos []string) error {
	if len(pos) > 0 {
		return fmt.Errorf("%s takes no positional arguments", tool)
	}
	return nil
}

// printGeneralHelp writes the overview and the tool list to w.
func printGeneralHelp(w io.Writer) {
	var b strings.Builder
	b.WriteString("diagctl — one-shot client for the diagd read-only diagnostic server\n\n")
	b.WriteString("Usage:\n")
	b.WriteString("  diagctl [--host HOST[:PORT]] [--token TOKEN] <tool> [args]\n")
	b.WriteString("  diagctl help [tool]\n\n")
	b.WriteString("Connection (flags override the DIAG_HOST / DIAG_TOKEN environment variables):\n")
	b.WriteString("  --host    server address as host or host:port (port defaults to 7017)\n")
	b.WriteString("  --token   authentication token\n\n")
	b.WriteString("Tools (all READ-ONLY):\n")
	for _, name := range toolOrder {
		fmt.Fprintf(&b, "  %-8s %s\n", name, tools[name].summary)
	}
	b.WriteString("\nRun 'diagctl help <tool>' (or 'diagctl <tool> --help') for a tool's arguments.\n")
	fmt.Fprint(w, b.String())
}

// printToolHelp writes one tool's usage line, summary, and flags to w.
func printToolHelp(w io.Writer, name string, spec toolSpec) {
	fmt.Fprintf(w, "Usage: diagctl %s\n\n  %s\n", spec.usage, spec.summary)

	fs, _ := spec.newParser()
	hasFlags := false
	fs.VisitAll(func(*flag.Flag) { hasFlags = true })
	if hasFlags {
		fmt.Fprintln(w, "\nFlags:")
		fs.SetOutput(w)
		fs.PrintDefaults()
	}
	fmt.Fprintln(w, "\nGlobal: --host, --token (or DIAG_HOST, DIAG_TOKEN). Paths are the host's")
	fmt.Fprintln(w, "real paths and must fall within a jail root the server granted.")
}
