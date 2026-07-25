package tools

import (
	"context"
	"encoding/json"
	"flag"
	"strings"

	"rpeek/internal/sqlq"
)

// dbTables lists the tables and views visible in a configured database, so the client can
// discover what it may query before running db-schema or sql.
type dbTables struct{ readOnly }

func init() { register(dbTables{}) }

// Name returns the subcommand name.
func (dbTables) Name() string { return "db-tables" }

// Summary returns the one-line help description.
func (dbTables) Summary() string { return "list the tables in a configured database" }

// Usage returns the argument synopsis.
func (dbTables) Usage() string { return "db-tables --db ALIAS" }

// dbTablesArgs are the wire arguments for the db-tables tool.
type dbTablesArgs struct {
	// DB is the database alias to introspect.
	DB string `json:"db"`
}

// NewFlags builds the db-tables flag set and its argument builder.
func (dbTables) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("db-tables", flag.ContinueOnError)
	db := fs.String("db", "", "database alias to introspect (required)")
	return fs, func(pos []string) (any, error) {
		if err := noPositionals("db-tables", pos); err != nil {
			return nil, err
		}
		return dbTablesArgs{DB: *db}, nil
	}
}

// Remote returns the table and view names, one per line.
func (dbTables) Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs[dbTablesArgs](raw)
	if err != nil {
		return Result{}, err
	}
	conn, err := dbLookup(env, args.DB)
	if err != nil {
		return Result{}, err
	}

	tx, err := conn.BeginReadOnly(ctx, env.Limits.Timeout)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()

	tables, err := sqlq.ListTables(ctx, tx, conn.Engine())
	if err != nil {
		return Result{}, err
	}

	out := strings.Join(tables, "\n")
	if out != "" {
		out += "\n"
	}
	capped, capTrunc := capOutput(out, env.Limits.MaxOutput)
	return Result{Output: capped, Truncated: capTrunc}, nil
}
