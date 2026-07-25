package tools

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"rpeek/internal/sqlq"
)

// dbSchema describes one table's columns — name, type, and nullability — so the client can
// learn a table's shape before querying it.
type dbSchema struct{ readOnly }

func init() { register(dbSchema{}) }

// Name returns the subcommand name.
func (dbSchema) Name() string { return "db-schema" }

// Summary returns the one-line help description.
func (dbSchema) Summary() string { return "describe a table's columns in a configured database" }

// Usage returns the argument synopsis.
func (dbSchema) Usage() string { return "db-schema --db ALIAS <table>" }

// dbSchemaArgs are the wire arguments for the db-schema tool.
type dbSchemaArgs struct {
	// DB is the database alias to introspect.
	DB string `json:"db"`

	// Table is the table whose columns to describe.
	Table string `json:"table"`
}

// NewFlags builds the db-schema flag set and its argument builder.
func (dbSchema) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("db-schema", flag.ContinueOnError)
	db := fs.String("db", "", "database alias to introspect (required)")
	return fs, func(pos []string) (any, error) {
		table, err := oneArg("db-schema", "table name", pos)
		if err != nil {
			return nil, err
		}
		return dbSchemaArgs{DB: *db, Table: table}, nil
	}
}

// Remote returns the table's columns as a compact table of name, type, and nullability.
func (dbSchema) Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs[dbSchemaArgs](raw)
	if err != nil {
		return Result{}, err
	}
	conn, err := dbLookup(env, args.DB)
	if err != nil {
		return Result{}, err
	}
	if !sqlq.ValidIdent(args.Table) {
		return Result{}, fmt.Errorf("invalid table name %q", args.Table)
	}

	tx, err := conn.BeginReadOnly(ctx, env.Limits.Timeout)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()

	cols, err := sqlq.DescribeTable(ctx, tx, conn.Engine(), args.Table)
	if err != nil {
		return Result{}, err
	}
	if len(cols) == 0 {
		return Result{}, fmt.Errorf("unknown table %q", args.Table)
	}

	rows := make([][]string, len(cols))
	for i, c := range cols {
		nullable := "NOT NULL"
		if c.Nullable {
			nullable = "NULL"
		}
		rows[i] = []string{c.Name, c.Type, nullable}
	}

	out, capTrunc := capOutput(renderTable([]string{"COLUMN", "TYPE", "NULLABLE"}, rows), env.Limits.MaxOutput)
	return Result{Output: out, Truncated: capTrunc}, nil
}
