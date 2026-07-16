package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"rpeek/internal/tools"
)

// runServer drives a ServerMode tool (serve): it parses the tool's flags and runs the
// server until it is signalled or its TTL elapses. gHost and gToken are any --host/--token
// given before the subcommand; they seed serve's own --host/--token (bind address and
// fixed token) so a flag before the subcommand still applies, while one after it
// overrides. It returns the process exit code.
func runServer(sm tools.ServerMode, args []string, gHost, gToken string) int {
	fs, build := sm.NewFlags()
	fs.SetOutput(io.Discard)
	if gHost != "" {
		_ = fs.Set("host", gHost)
	}
	if gToken != "" {
		_ = fs.Set("token", gToken)
	}

	positionals, err := parseFlagsAnyOrder(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Print(tools.ToolHelp(sm))
		return exitOK
	}
	if err != nil {
		return usageErr("%v", err)
	}
	params, err := build(positionals)
	if err != nil {
		return usageErr("%v", err)
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return fatalf("%v", err)
	}

	if err := sm.Serve(context.Background(), raw, os.Stdout); err != nil {
		return fatalf("%v", err)
	}
	return exitOK
}
