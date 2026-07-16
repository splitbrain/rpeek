// Command diag is the read-only remote diagnostic tool. With the "serve" subcommand it
// runs the diagnostic server, copied onto a remote host and run there. With a tool
// subcommand it acts as a one-shot client that dials a server, runs one tool, prints
// the result, and exits.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"diag/internal/netutil"
	"diag/internal/tools"
)

// Process exit codes.
const (
	exitOK     = 0 // success
	exitError  = 1 // transport, protocol, or server-runtime error
	exitServer = 2 // server returned a tool error
	exitUsage  = 3 // usage error (bad flags, missing host/token)
)

func main() {
	os.Exit(run(os.Args[1:]))
}

// run dispatches on the subcommand: "serve" runs the server, "help" prints help, and
// any registered tool name runs that tool as a client. The connection flags --host and
// --token may precede the subcommand; they are parsed here and passed down as defaults
// so a matching flag placed after the subcommand overrides them. It returns the process
// exit code.
func run(args []string) int {
	// The flag package stops parsing at the first non-flag argument, which is the
	// subcommand, so any leading --host/--token are consumed here and the rest is left
	// for the subcommand to parse.
	gfs := flag.NewFlagSet("diag", flag.ContinueOnError)
	gfs.SetOutput(io.Discard)
	gHost := gfs.String("host", "", "server address (or DIAG_HOST)")
	gToken := gfs.String("token", "", "auth token (or DIAG_TOKEN)")
	if err := gfs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printGeneralHelp(os.Stdout)
			return exitOK
		}
		return usageErr("%v", err)
	}

	rest := gfs.Args()
	if len(rest) == 0 {
		printGeneralHelp(os.Stderr)
		return exitUsage
	}

	switch rest[0] {
	case "serve":
		return runServe(rest[1:], *gHost, *gToken)
	case "help":
		return runHelp(rest[1:])
	default:
		tool, ok := tools.Lookup(rest[0])
		if !ok {
			return usageErr("unknown command %q (run 'diag help' for the list)", rest[0])
		}
		return runTool(tool, rest[1:], *gHost, *gToken)
	}
}

// resolveHostToken applies the precedence flag > env for the client's server address
// and token, normalizes the address, and returns the resolved values. On a missing or
// invalid setting it prints a usage error and returns a non-OK exit code in code.
func resolveHostToken(hostFlag, tokenFlag string) (host, token string, code int) {
	host = hostFlag
	if host == "" {
		host = os.Getenv("DIAG_HOST")
	}
	token = tokenFlag
	if token == "" {
		token = os.Getenv("DIAG_TOKEN")
	}
	if host == "" {
		return "", "", usageErr("no server address: set --host or DIAG_HOST")
	}
	if token == "" {
		return "", "", usageErr("no token: set --token or DIAG_TOKEN")
	}
	addr, err := netutil.NormalizeAddr(host)
	if err != nil {
		return "", "", usageErr("%v", err)
	}
	return addr, token, exitOK
}

// usageErr prints a formatted usage error to stderr and returns the usage exit code.
func usageErr(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "diag: "+format+"\n", args...)
	return exitUsage
}

// fatalf prints a formatted runtime error to stderr and returns the general error code.
func fatalf(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "diag: "+format+"\n", args...)
	return exitError
}
