package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"rpeek/internal/client"
	"rpeek/internal/tools"
)

// runTool parses a tool subcommand's arguments — the tool's own flags plus the shared
// --host/--token — dials the server, runs the tool, and prints the result. gHost and
// gToken are the values of any --host/--token given before the tool name; they seed the
// corresponding flags so an explicit --host/--token after the tool name overrides them.
// It returns the process exit code.
func runTool(tool tools.Tool, args []string, gHost, gToken string) int {
	fs, build := tool.NewFlags()
	fs.SetOutput(io.Discard) // suppress the flag package's own output; we render our own
	host := fs.String("host", gHost, "server address host or host:port (or RPEEK_HOST)")
	token := fs.String("token", gToken, "auth token (or RPEEK_TOKEN)")

	positionals, err := parseFlagsAnyOrder(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		printToolHelp(os.Stdout, tool)
		return exitOK
	}
	if err != nil {
		return usageErr("%v", err)
	}
	params, err := build(positionals)
	if err != nil {
		return usageErr("%v", err)
	}

	hostAddr, tok, code := resolveHostToken(*host, *token)
	if code != exitOK {
		return code
	}

	resp, err := client.Call(hostAddr, tok, tool.Name(), params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rpeek: %v\n", err)
		return exitError
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "rpeek: %s\n", resp.Error)
		return exitServer
	}

	fmt.Print(resp.Output)
	if resp.Truncated {
		fmt.Fprintln(os.Stderr, "... (truncated)")
	}
	return exitOK
}

// runHelp handles "rpeek help [name]": with no argument it lists everything, otherwise it
// prints one subcommand's usage. Help is a requested output, so it goes to stdout and
// exits successfully.
func runHelp(args []string) int {
	if len(args) == 0 {
		printGeneralHelp(os.Stdout)
		return exitOK
	}
	name := args[0]
	if name == "serve" {
		return runServe([]string{"--help"}, "", "")
	}
	tool, ok := tools.Lookup(name)
	if !ok {
		return usageErr("unknown tool %q (run 'rpeek help' for the list)", name)
	}
	printToolHelp(os.Stdout, tool)
	return exitOK
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

// printGeneralHelp writes the overview, connection flags, and the tool list to w.
func printGeneralHelp(w io.Writer) {
	var b strings.Builder
	b.WriteString("rpeek — read-only remote diagnostic tool (server + one-shot client)\n\n")
	b.WriteString("Usage:\n")
	b.WriteString("  rpeek [--host HOST[:PORT]] [--token TOKEN] serve [flags] [roots...]\n")
	b.WriteString("  rpeek [--host HOST[:PORT]] [--token TOKEN] <tool> [args]\n")
	b.WriteString("  rpeek help [serve|tool]\n\n")
	b.WriteString("Connection flags may appear before the subcommand or after it (interleaved\n")
	b.WriteString("with its arguments in any order); an explicit flag overrides the RPEEK_HOST /\n")
	b.WriteString("RPEEK_TOKEN environment variables:\n")
	b.WriteString("  --host    server address as host or host:port (port defaults to 7017)\n")
	b.WriteString("  --token   authentication token\n\n")
	b.WriteString("Server:\n")
	b.WriteString("  serve     run the diagnostic server (see 'rpeek help serve')\n\n")
	b.WriteString("Tools (all READ-ONLY):\n")
	for _, t := range tools.All {
		fmt.Fprintf(&b, "  %-8s %s\n", t.Name(), t.Summary())
	}
	b.WriteString("\nRun 'rpeek help <tool>' (or 'rpeek <tool> --help') for a tool's arguments.\n")
	fmt.Fprint(w, b.String())
}

// printToolHelp writes one tool's usage line, summary, and flags to w. The shared
// --host/--token flags are described once in the footer rather than in the flag list.
func printToolHelp(w io.Writer, tool tools.Tool) {
	fmt.Fprintf(w, "Usage: rpeek [--host HOST[:PORT]] [--token TOKEN] %s\n\n  %s\n", tool.Usage(), tool.Summary())

	fs, _ := tool.NewFlags()
	hasFlags := false
	fs.VisitAll(func(*flag.Flag) { hasFlags = true })
	if hasFlags {
		fmt.Fprintln(w, "\nFlags:")
		fs.SetOutput(w)
		fs.PrintDefaults()
	}
	fmt.Fprintln(w, "\nGlobal: --host, --token (or RPEEK_HOST, RPEEK_TOKEN) may appear before or after")
	fmt.Fprintln(w, "the tool name. Paths are the host's real paths and must fall within a jail")
	fmt.Fprintln(w, "root the server granted.")
}
