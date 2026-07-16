package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"rpeek/internal/client"
	"rpeek/internal/netutil"
	"rpeek/internal/tools"
)

// runTool parses a tool subcommand's arguments — the tool's own flags plus the shared
// --host/--token — then runs it according to the halves it implements: a client-only tool
// runs here, a server-only tool dials the server, and a tool with both runs locally and,
// when a server is addressed, also queries it. gHost and gToken are the values of any
// --host/--token given before the tool name; they seed the corresponding flags so an
// explicit --host/--token after the tool name overrides them. It returns the process exit
// code.
func runTool(tool tools.Tool, args []string, gHost, gToken string) int {
	fs, build := tool.NewFlags()
	fs.SetOutput(io.Discard) // suppress the flag package's own output; we render our own
	host := fs.String("host", gHost, "server address host or host:port (or RPEEK_HOST)")
	token := fs.String("token", gToken, "auth token (or RPEEK_TOKEN)")

	positionals, err := parseFlagsAnyOrder(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Print(tools.ToolHelp(tool))
		return exitOK
	}
	if err != nil {
		return usageErr("%v", err)
	}
	params, err := build(positionals)
	if err != nil {
		return usageErr("%v", err)
	}
	// The built arguments feed both faces: passed to Local in-process, or carried to
	// Remote over the wire (client.Call marshals a json.RawMessage verbatim).
	raw, err := json.Marshal(params)
	if err != nil {
		return fatalf("%v", err)
	}

	local, canLocal := tool.(tools.LocalTool)
	remote, canRemote := tool.(tools.RemoteTool)
	switch {
	case canLocal && canRemote:
		return runLocalRemote(local, remote, raw, *host, *token)
	case canLocal:
		return runLocalOnly(local, raw)
	case canRemote:
		return runRemote(remote, raw, *host, *token)
	default:
		return fatalf("tool %q has no local or remote implementation", tool.Name())
	}
}

// runLocalOnly runs a client-only tool in this process and prints its result.
func runLocalOnly(local tools.LocalTool, raw json.RawMessage) int {
	res, err := local.Local(context.Background(), localEnv(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rpeek: %v\n", err)
		return exitError
	}
	fmt.Print(res.Output)
	if res.Truncated {
		fmt.Fprintln(os.Stderr, "... (truncated)")
	}
	return exitOK
}

// runRemote runs a server-only tool: it requires a host and token, dials the server, and
// prints the result.
func runRemote(remote tools.RemoteTool, raw json.RawMessage, hostFlag, tokenFlag string) int {
	hostAddr, tok, code := resolveHostToken(hostFlag, tokenFlag)
	if code != exitOK {
		return code
	}
	resp, err := client.Call(hostAddr, tok, remote.Name(), raw)
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

// runLocalRemote runs a tool that has both halves. It always runs the local half; when a
// host and token resolve (from --host/--token or RPEEK_HOST/RPEEK_TOKEN) it also queries
// the server and prints the two results in labelled blocks, otherwise the local one
// alone. It returns the process exit code.
func runLocalRemote(local tools.LocalTool, remote tools.RemoteTool, raw json.RawMessage, hostFlag, tokenFlag string) int {
	localRes, err := local.Local(context.Background(), localEnv(), raw)
	if err != nil {
		return fatalf("%v", err)
	}

	host, token := hostToken(hostFlag, tokenFlag)
	if host == "" || token == "" {
		fmt.Print(localRes.Output)
		if localRes.Truncated {
			fmt.Fprintln(os.Stderr, "... (truncated)")
		}
		return exitOK
	}

	addr, err := netutil.NormalizeAddr(host)
	if err != nil {
		return usageErr("%v", err)
	}
	resp, err := client.Call(addr, token, remote.Name(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rpeek: %v\n", err)
		return exitError
	}
	if !resp.OK {
		fmt.Fprintf(os.Stderr, "rpeek: %s\n", resp.Error)
		return exitServer
	}

	fmt.Printf("local:\n%s\n\nremote (%s):\n%s\n",
		strings.TrimRight(localRes.Output, "\n"), addr, strings.TrimRight(resp.Output, "\n"))
	if resp.Truncated {
		fmt.Fprintln(os.Stderr, "... (truncated)")
	}
	return exitOK
}

// localEnv returns the Env for a tool's Local execution: no jail — a local-capable tool
// never reads host paths — and a generous output cap, since client-side output is not
// bounded by a server limit.
func localEnv() tools.Env {
	return tools.Env{Limits: tools.Limits{MaxOutput: 1 << 20}}
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
