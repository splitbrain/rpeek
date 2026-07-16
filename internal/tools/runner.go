package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// Runner dispatches server-side tool requests against the registry. It is what the server
// package's ToolRunner interface is satisfied by: the server hands it a tool name and raw
// arguments, and it runs the matching tool's Remote half under the configured Env,
// applying the per-tool timeout.
type Runner struct {
	// Env is the server-side environment (jail, limits, journalctl) passed to every tool.
	Env Env
}

// NewRunner returns a Runner that runs tools under env.
func NewRunner(env Env) Runner {
	return Runner{Env: env}
}

// RunRemote looks up the named tool, runs its Remote half under the Env and the per-tool
// timeout, and returns its output and truncation flag. It errors when no such tool exists
// or the tool has no server-side operation, or relays the tool's own error.
func (r Runner) RunRemote(ctx context.Context, name string, args json.RawMessage) (string, bool, error) {
	tool, ok := Lookup(name)
	if !ok {
		return "", false, fmt.Errorf("unknown tool: %q", name)
	}
	remote, ok := tool.(RemoteTool)
	if !ok {
		return "", false, fmt.Errorf("tool %q has no server-side operation", name)
	}

	if r.Env.Limits.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Env.Limits.Timeout)
		defer cancel()
	}

	res, err := remote.Remote(ctx, r.Env, args)
	if err != nil {
		return "", false, err
	}
	return res.Output, res.Truncated, nil
}
