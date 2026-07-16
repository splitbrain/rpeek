package tools

import (
	"context"
	"encoding/json"
	"flag"
	"os"
)

// hostname returns the server's kernel hostname. It reads no files and takes no
// arguments, so it is the cheapest way to confirm a client can reach the server,
// complete the TLS handshake, and authenticate: a successful reply proves the whole
// request path works, and its output names which host answered.
type hostname struct{ readOnly }

func init() { register(hostname{}) }

// Name returns the subcommand name.
func (hostname) Name() string { return "hostname" }

// Summary returns the one-line help description.
func (hostname) Summary() string { return "server hostname; a cheap connectivity and auth check" }

// Usage returns the argument synopsis.
func (hostname) Usage() string { return "hostname" }

// hostnameArgs are the wire arguments for the hostname tool. It takes none.
type hostnameArgs struct{}

// NewFlags builds the hostname flag set and its argument builder.
func (hostname) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("hostname", flag.ContinueOnError)
	return fs, func(pos []string) (any, error) {
		if err := noPositionals("hostname", pos); err != nil {
			return nil, err
		}
		return hostnameArgs{}, nil
	}
}

// Run returns the server's hostname followed by a newline.
func (hostname) Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	if _, err := decodeArgs[hostnameArgs](raw); err != nil {
		return Result{}, err
	}
	name, err := os.Hostname()
	if err != nil {
		return Result{}, err
	}
	out, capTrunc := capOutput(name+"\n", env.Limits.MaxOutput)
	return Result{Output: out, Truncated: capTrunc}, nil
}
