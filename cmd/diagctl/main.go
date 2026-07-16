// Command diagctl is the one-shot client for the diagd diagnostic server. Each
// invocation dials the server, authenticates, runs one tool, prints the result, and
// exits. It does no result formatting: the server produces CLI-style text and the
// client relays it verbatim.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"diag/internal/client"
	"diag/internal/netutil"
)

// Exit codes.
const (
	exitOK        = 0 // success
	exitTransport = 1 // protocol or transport error
	exitServer    = 2 // server returned a tool error
	exitUsage     = 3 // usage error (bad flags, missing host/token)
)

func main() {
	os.Exit(run())
}

// run parses arguments, dispatches help or a tool call, prints the result, and returns
// the process exit code.
func run() int {
	gfs := flag.NewFlagSet("diagctl", flag.ContinueOnError)
	gfs.SetOutput(os.Stderr)
	gfs.Usage = func() { printGeneralHelp(os.Stderr) }
	host := gfs.String("host", "", "server address host or host:port (or DIAG_HOST)")
	token := gfs.String("token", "", "auth token (or DIAG_TOKEN)")
	if err := gfs.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printGeneralHelp(os.Stdout)
			return exitOK
		}
		return exitUsage
	}

	rest := gfs.Args()
	if len(rest) == 0 {
		printGeneralHelp(os.Stderr)
		return exitUsage
	}
	tool := rest[0]
	toolArgs := rest[1:]

	// The help command needs no connection details.
	if tool == "help" {
		return runHelp(toolArgs)
	}

	spec, ok := tools[tool]
	if !ok {
		return usageErr("unknown tool %q (run 'diagctl help' for the list)", tool)
	}

	params, err := buildToolArgs(spec, toolArgs)
	if errors.Is(err, flag.ErrHelp) {
		printToolHelp(os.Stdout, tool, spec)
		return exitOK
	}
	if err != nil {
		return usageErr("%v", err)
	}

	if *host == "" {
		*host = os.Getenv("DIAG_HOST")
	}
	if *token == "" {
		*token = os.Getenv("DIAG_TOKEN")
	}
	if *host == "" {
		return usageErr("no server address: set --host or DIAG_HOST")
	}
	if *token == "" {
		return usageErr("no token: set --token or DIAG_TOKEN")
	}
	hostAddr, err := netutil.NormalizeAddr(*host)
	if err != nil {
		return usageErr("%v", err)
	}

	resp, err := client.Call(hostAddr, *token, tool, params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "diagctl: %v\n", err)
		return exitTransport
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "diagctl: %s\n", resp.Error)
		return exitServer
	}

	fmt.Print(resp.Output)
	if resp.Truncated {
		fmt.Fprintln(os.Stderr, "... (truncated)")
	}
	return exitOK
}

// runHelp handles "diagctl help [tool]": with no argument it lists all tools, otherwise
// it prints one tool's usage and flags. Help is a requested output, so it goes to
// stdout and exits successfully.
func runHelp(args []string) int {
	if len(args) == 0 {
		printGeneralHelp(os.Stdout)
		return exitOK
	}
	name := args[0]
	spec, ok := tools[name]
	if !ok {
		return usageErr("unknown tool %q (run 'diagctl help' for the list)", name)
	}
	printToolHelp(os.Stdout, name, spec)
	return exitOK
}

// usageErr prints a formatted usage error to stderr and returns the usage exit code.
func usageErr(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "diagctl: "+format+"\n", args...)
	return exitUsage
}
