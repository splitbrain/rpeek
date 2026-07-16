package tools

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
)

// helpTool documents the rpeek command surface. It runs only in the client — there is no
// server-side help — reading the compiled-in registry to list the available tools and,
// for a named one, its arguments. Because it reads this binary's registry, it describes
// the client's own tools.
type helpTool struct{ readOnly }

// Name returns the subcommand name.
func (helpTool) Name() string { return "help" }

// Summary returns the one-line help description.
func (helpTool) Summary() string { return "show this help; 'help <tool>' for one tool's arguments" }

// Usage returns the argument synopsis.
func (helpTool) Usage() string { return "help [tool]" }

// helpArgs are the wire arguments for the help tool: an optional topic to describe.
type helpArgs struct {
	// Topic names a tool (or "serve") to describe; empty lists everything.
	Topic string `json:"topic,omitempty"`
}

// NewFlags builds the help flag set and its argument builder. It accepts an optional
// positional naming the topic to describe.
func (helpTool) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("help", flag.ContinueOnError)
	return fs, func(pos []string) (any, error) {
		switch len(pos) {
		case 0:
			return helpArgs{}, nil
		case 1:
			return helpArgs{Topic: pos[0]}, nil
		default:
			return nil, fmt.Errorf("help takes at most one topic")
		}
	}
}

// Local renders the help text: the general listing when no topic is given, otherwise the
// named subcommand's usage (any registered tool, including serve).
func (helpTool) Local(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs[helpArgs](raw)
	if err != nil {
		return Result{}, err
	}
	if args.Topic == "" {
		return Result{Output: GeneralHelp()}, nil
	}
	tool, ok := Lookup(args.Topic)
	if !ok {
		return Result{}, fmt.Errorf("unknown tool %q (run 'rpeek help' for the list)", args.Topic)
	}
	return Result{Output: ToolHelp(tool)}, nil
}

// GeneralHelp returns the overview: the usage synopsis, the connection flags, the server
// command, and a one-line summary of every registered tool.
func GeneralHelp() string {
	var b strings.Builder
	b.WriteString("rpeek — read-only remote diagnostic tool (server + one-shot client)\n\n")
	b.WriteString("Usage:\n")
	b.WriteString("  rpeek [--host HOST[:PORT]] [--token TOKEN] serve [flags] [roots...]\n")
	b.WriteString("  rpeek [--host HOST[:PORT]] [--token TOKEN] <tool> [args]\n")
	b.WriteString("  rpeek help [tool]\n\n")
	b.WriteString("Connection flags may appear before the subcommand or after it (interleaved\n")
	b.WriteString("with its arguments in any order); an explicit flag overrides the RPEEK_HOST /\n")
	b.WriteString("RPEEK_TOKEN environment variables:\n")
	b.WriteString("  --host    server address as host or host:port (port defaults to 7017)\n")
	b.WriteString("  --token   authentication token\n\n")
	b.WriteString("Server:\n")
	for _, t := range All {
		if _, ok := t.(ServerMode); ok {
			fmt.Fprintf(&b, "  %-8s %s\n", t.Name(), t.Summary())
		}
	}
	b.WriteString("\nTools (all READ-ONLY):\n")
	for _, t := range All {
		if _, ok := t.(ServerMode); ok {
			continue
		}
		fmt.Fprintf(&b, "  %-8s %s\n", t.Name(), t.Summary())
	}
	b.WriteString("\nRun 'rpeek help <tool>' (or 'rpeek <tool> --help') for a tool's arguments.\n")
	return b.String()
}

// ToolHelp returns one tool's usage line, summary, and flags. The shared --host/--token
// flags are described once in the footer rather than in the flag list.
func ToolHelp(tool Tool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: rpeek [--host HOST[:PORT]] [--token TOKEN] %s\n\n  %s\n", tool.Usage(), tool.Summary())

	fs, _ := tool.NewFlags()
	hasFlags := false
	fs.VisitAll(func(*flag.Flag) { hasFlags = true })
	if hasFlags {
		b.WriteString("\nFlags:\n")
		fs.SetOutput(&b)
		fs.PrintDefaults()
	}
	if _, ok := tool.(ServerMode); ok {
		b.WriteString("\nRoots are the directories file tools may read within; with none given,\n")
		b.WriteString("serve jails to the current working directory.\n")
	} else {
		b.WriteString("\nGlobal: --host, --token (or RPEEK_HOST, RPEEK_TOKEN) may appear before or after\n")
		b.WriteString("the tool name. Paths are the host's real paths and must fall within a jail\n")
		b.WriteString("root the server granted.\n")
	}
	return b.String()
}
