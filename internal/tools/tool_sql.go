package tools

import (
	"context"
	"encoding/json"
	"flag"

	"rpeek/internal/sqlq"
)

// sqlTool runs a read-only query against a configured database. The client's query is
// written in rpeek's own restricted grammar, which has no write production, so a write is a
// syntax error rather than a permission that must be checked. The query is parsed, its
// identifiers resolved against the live catalog, and translated to a real SELECT whose
// values are all bound parameters, then run inside a read-only transaction.
type sqlTool struct{ readOnly }

func init() { register(sqlTool{}) }

// Name returns the subcommand name.
func (sqlTool) Name() string { return "sql" }

// Summary returns the one-line help description.
func (sqlTool) Summary() string { return "run a read-only query against a configured database" }

// Usage returns the argument synopsis.
func (sqlTool) Usage() string { return "sql --db ALIAS \"SELECT ... FROM ... [WHERE ...]\"" }

// Help returns the grammar reference for the restricted query language.
func (sqlTool) Help() string {
	return `The query is written in rpeek's own restricted, SELECT-only grammar — it looks like
SQL but is not SQL. There is no write, no subquery, no comment, no statement
stacking, and no function beyond the five aggregates, so a write is a syntax error
rather than a denied operation. Run db-list, db-tables, and db-schema first to
discover what you may query.

Grammar:
  SELECT * | select_item {, select_item}
  FROM table [alias] {join}
  [WHERE condition]
  [GROUP BY column {, column}]
  [ORDER BY column [ASC|DESC] {, ...}]
  [LIMIT n [OFFSET n]]

  select_item : column | AGG( * | column ) [AS alias]
  AGG         : COUNT | SUM | AVG | MIN | MAX
  join        : [INNER | LEFT [OUTER] | RIGHT [OUTER]] JOIN table [alias] ON condition
  condition   : predicate, combined with AND / OR / NOT and parentheses
  predicate   : column OP (value | column)
              | column IN (value {, value})
              | column LIKE 'pattern'          (case-sensitive match)
              | column ILIKE 'pattern'         (case-insensitive match)
              | column IS [NOT] NULL
              | column BETWEEN value AND value
  OP          : = | != | <> | < | <= | > | >=
  column      : name | table.name          (identifiers, resolved against the schema)
  value       : 'string' | number | TRUE | FALSE | NULL   (always bound as a parameter)

Examples:
  sql --db app "SELECT status, COUNT(*) AS n FROM orders GROUP BY status ORDER BY n DESC"
  sql --db app "SELECT u.name, o.amount FROM users u JOIN orders o ON o.uid = u.id WHERE o.state = 'failed'"

LIKE is case-sensitive and ILIKE is case-insensitive on every engine, so the
choice of predicate — not the database's default collation — decides case
sensitivity. In a pattern, % matches any run of characters and _ matches one.

A missing LIMIT applies a server default; results are capped and may be truncated.`
}

// sqlArgs are the wire arguments for the sql tool.
type sqlArgs struct {
	// DB is the database alias to query, configured on the server.
	DB string `json:"db"`

	// Query is the query in rpeek's restricted grammar. See "rpeek help sql".
	Query string `json:"query"`
}

const (
	// sqlDefaultLimit is the row limit applied when the query names none.
	sqlDefaultLimit = 1000
	// sqlMaxLimit is the hard cap on the row limit regardless of the query's own LIMIT.
	sqlMaxLimit = 100000
)

// NewFlags builds the sql flag set and its argument builder. The query is a single
// positional argument, so it must be quoted as one shell word.
func (sqlTool) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("sql", flag.ContinueOnError)
	db := fs.String("db", "", "database alias to query (required)")
	return fs, func(pos []string) (any, error) {
		query, err := oneArg("sql", "quoted query", pos)
		if err != nil {
			return nil, err
		}
		return sqlArgs{DB: *db, Query: query}, nil
	}
}

// Remote parses, resolves, translates, and runs the query, returning the rows as a compact
// text table.
func (sqlTool) Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	args, err := decodeArgs[sqlArgs](raw)
	if err != nil {
		return Result{}, err
	}
	conn, err := dbLookup(env, args.DB)
	if err != nil {
		return Result{}, err
	}

	q, err := sqlq.Parse(args.Query)
	if err != nil {
		return Result{}, err
	}
	effLimit, capped := applyRowLimit(q)

	tx, err := conn.BeginReadOnly(ctx, env.Limits.Timeout)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()

	if err := sqlq.Resolve(ctx, sqlq.NewCatalog(tx, conn.Engine()), q); err != nil {
		return Result{}, err
	}
	sqlStr, sqlParams, err := sqlq.Translate(q, conn.Engine())
	if err != nil {
		return Result{}, err
	}

	rows, err := tx.QueryContext(ctx, sqlStr, sqlParams...)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()

	cols, data, err := scanRows(rows)
	if err != nil {
		return Result{}, err
	}

	// When a returned count reaches a limit we imposed — the default, or the hard cap on a
	// larger requested limit — more rows may exist, so report truncation.
	rowTrunc := capped && len(data) >= effLimit

	out, capTrunc := capOutput(renderTable(cols, data), env.Limits.MaxOutput)
	return Result{Output: out, Truncated: rowTrunc || capTrunc}, nil
}

// applyRowLimit sets the query's effective row limit and reports it together with whether
// the limit was imposed by the server (a default when the query gave none, or the hard cap
// applied to a larger requested limit) rather than freely chosen by the client.
func applyRowLimit(q *sqlq.Query) (limit int, imposed bool) {
	if q.Limit == nil {
		l := sqlDefaultLimit
		q.Limit = &l
		return l, true
	}
	if *q.Limit > sqlMaxLimit {
		*q.Limit = sqlMaxLimit
		return sqlMaxLimit, true
	}
	return *q.Limit, false
}
