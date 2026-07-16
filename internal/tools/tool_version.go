package tools

import (
	"context"
	"encoding/json"
	"flag"

	"rpeek/internal/version"
)

// versionTool reports an rpeek binary's build version. It reads no files and takes no
// arguments. As a LocalTool it answers from the caller's own binary, so "rpeek version"
// works with no server; addressed to a server it reports that server's build too, which
// is how an operator confirms which build is deployed on a host.
type versionTool struct{ readOnly }

func init() { register(versionTool{}) }

// Name returns the subcommand name.
func (versionTool) Name() string { return "version" }

// Summary returns the one-line help description.
func (versionTool) Summary() string {
	return "rpeek build version; local, plus the server's when connected"
}

// Usage returns the argument synopsis.
func (versionTool) Usage() string { return "version" }

// versionArgs are the wire arguments for the version tool. It takes none.
type versionArgs struct{}

// NewFlags builds the version flag set and its argument builder.
func (versionTool) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	return fs, func(pos []string) (any, error) {
		if err := noPositionals("version", pos); err != nil {
			return nil, err
		}
		return versionArgs{}, nil
	}
}

// Local returns the client binary's build version. It is the same computation as Remote,
// just run in this process, so it reads this binary's version rather than the server's.
func (v versionTool) Local(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	return v.Remote(ctx, env, raw)
}

// Remote returns the server binary's build version followed by a newline.
func (versionTool) Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	if _, err := decodeArgs[versionArgs](raw); err != nil {
		return Result{}, err
	}
	out, capTrunc := capOutput(version.Version+"\n", env.Limits.MaxOutput)
	return Result{Output: out, Truncated: capTrunc}, nil
}
