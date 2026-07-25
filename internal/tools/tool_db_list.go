package tools

import (
	"context"
	"encoding/json"
	"flag"
)

// dbList lists the databases the server is configured to query, by alias, with each one's
// engine and host. It is the client's entry point for database work: the serve banner names
// the aliases too, but only on the remote host's stdout, which the client never sees. No DSN
// or password is ever returned.
type dbList struct{ readOnly }

func init() { register(dbList{}) }

// Name returns the subcommand name.
func (dbList) Name() string { return "db-list" }

// Summary returns the one-line help description.
func (dbList) Summary() string { return "list the databases configured for querying" }

// Usage returns the argument synopsis.
func (dbList) Usage() string { return "db-list" }

// dbListArgs are the wire arguments for the db-list tool. It takes none.
type dbListArgs struct{}

// NewFlags builds the db-list flag set and its argument builder.
func (dbList) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("db-list", flag.ContinueOnError)
	return fs, func(pos []string) (any, error) {
		if err := noPositionals("db-list", pos); err != nil {
			return nil, err
		}
		return dbListArgs{}, nil
	}
}

// Remote returns the configured aliases with their engine and host as a compact table.
func (dbList) Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	if _, err := decodeArgs[dbListArgs](raw); err != nil {
		return Result{}, err
	}
	if env.DB == nil || len(env.DB.Aliases()) == 0 {
		return Result{Output: "no databases are configured on this server\n"}, nil
	}

	rows := make([][]string, 0, len(env.DB.Aliases()))
	for _, alias := range env.DB.Aliases() {
		conn, ok := env.DB.Lookup(alias)
		if !ok {
			continue
		}
		rows = append(rows, []string{alias, conn.Engine().String(), conn.Host()})
	}

	out, capTrunc := capOutput(renderTable([]string{"ALIAS", "ENGINE", "HOST"}, rows), env.Limits.MaxOutput)
	return Result{Output: out, Truncated: capTrunc}, nil
}
