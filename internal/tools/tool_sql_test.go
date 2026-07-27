package tools

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rpeek/internal/dbconn"

	_ "modernc.org/sqlite"
)

// seedDB creates a SQLite database with users and orders, returns a registry exposing it
// under the alias "app", and the file path for out-of-band verification.
func seedDB(t *testing.T) (*dbconn.Registry, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, status TEXT, age INTEGER)`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, uid INTEGER, amount REAL)`,
		`INSERT INTO users (id, name, status, age) VALUES
			(1, 'alice', 'active', 30),
			(2, 'bob', 'inactive', 45),
			(3, 'carol', 'active', 25)`,
		`INSERT INTO orders (id, uid, amount) VALUES (1, 1, 9.99), (2, 1, 5.00), (3, 3, 20.00)`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	reg, err := dbconn.New(map[string]string{"app": "sqlite://" + path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reg.Close() })
	return reg, path
}

// dbEnv returns an Env carrying the given registry and generous limits.
func dbEnv(reg *dbconn.Registry) Env {
	return Env{DB: reg, Limits: Limits{MaxOutput: 1 << 20, Timeout: 10 * time.Second}}
}

// userCount opens the database file directly and returns the number of user rows, to verify
// out-of-band that adversarial queries changed nothing.
func userCount(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSQLToolSelect(t *testing.T) {
	reg, _ := seedDB(t)
	res, err := sqlTool{}.Remote(context.Background(), dbEnv(reg),
		mustRaw(t, sqlArgs{DB: "app", Query: `SELECT name, age FROM users WHERE status = 'active' ORDER BY age`}))
	if err != nil {
		t.Fatal(err)
	}
	// Header plus the two active users in ascending age order.
	if !strings.Contains(res.Output, "name") || !strings.Contains(res.Output, "age") {
		t.Errorf("missing header:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "carol") || !strings.Contains(res.Output, "alice") {
		t.Errorf("missing expected rows:\n%s", res.Output)
	}
	if strings.Contains(res.Output, "bob") {
		t.Errorf("inactive user leaked into result:\n%s", res.Output)
	}
	if i := strings.Index(res.Output, "carol"); i < 0 || i > strings.Index(res.Output, "alice") {
		t.Errorf("rows not ordered by age ascending:\n%s", res.Output)
	}
}

func TestSQLToolJoinAggregate(t *testing.T) {
	reg, _ := seedDB(t)
	res, err := sqlTool{}.Remote(context.Background(), dbEnv(reg),
		mustRaw(t, sqlArgs{DB: "app", Query: `SELECT u.name, SUM(o.amount) AS total FROM users u INNER JOIN orders o ON o.uid = u.id GROUP BY u.name ORDER BY total DESC`}))
	if err != nil {
		t.Fatal(err)
	}
	// alice: 9.99 + 5.00 = 14.99, carol: 20.00. carol first (DESC).
	if !strings.Contains(res.Output, "total") {
		t.Errorf("missing aggregate alias header:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "carol") || !strings.Contains(res.Output, "alice") {
		t.Errorf("missing expected grouped rows:\n%s", res.Output)
	}
	if strings.Index(res.Output, "carol") > strings.Index(res.Output, "alice") {
		t.Errorf("rows not ordered by total descending:\n%s", res.Output)
	}
}

// TestSQLToolLikeCaseSensitivity is the end-to-end proof that LIKE is case-sensitive and
// ILIKE is case-insensitive through the whole pipeline against a live database: LIKE only
// matches the exact case, while ILIKE matches regardless of case. It runs on SQLite, whose
// built-in LIKE is case-insensitive by default — the case the earlier implementation got
// wrong — so a green result here means the connection pragma and the translation are both
// doing their jobs.
func TestSQLToolLikeCaseSensitivity(t *testing.T) {
	reg, _ := seedDB(t)
	env := dbEnv(reg)

	run := func(query string) string {
		t.Helper()
		res, err := sqlTool{}.Remote(context.Background(), env, mustRaw(t, sqlArgs{DB: "app", Query: query}))
		if err != nil {
			t.Fatalf("query %q failed: %v", query, err)
		}
		return res.Output
	}
	has := func(out, name string) bool { return strings.Contains(out, name) }

	// LIKE is case-sensitive: the lower-case pattern matches 'alice', the upper-case one
	// matches nothing.
	if out := run(`SELECT name FROM users WHERE name LIKE 'alice'`); !has(out, "alice") {
		t.Errorf("LIKE 'alice' should match alice:\n%s", out)
	}
	if out := run(`SELECT name FROM users WHERE name LIKE 'ALICE'`); has(out, "alice") {
		t.Errorf("LIKE 'ALICE' must not match alice (LIKE is case-sensitive):\n%s", out)
	}
	if out := run(`SELECT name FROM users WHERE name LIKE 'A%'`); has(out, "alice") {
		t.Errorf("LIKE 'A%%' must not match alice (LIKE is case-sensitive):\n%s", out)
	}

	// ILIKE is case-insensitive: every casing of the pattern matches 'alice'.
	for _, pat := range []string{"alice", "ALICE", "Alice", "A%", "a%"} {
		if out := run(`SELECT name FROM users WHERE name ILIKE '` + pat + `'`); !has(out, "alice") {
			t.Errorf("ILIKE '%s' should match alice (ILIKE is case-insensitive):\n%s", pat, out)
		}
	}
	// NOT ILIKE is the exact complement: 'ALICE' excludes alice.
	if out := run(`SELECT name FROM users WHERE NOT name ILIKE 'ALICE'`); has(out, "alice") {
		t.Errorf("NOT ILIKE 'ALICE' must exclude alice:\n%s", out)
	}
}

// TestSQLToolAdversarialWrites is the end-to-end security test: every write attempt is
// rejected as a grammar error and the data is unchanged, on a superuser-equivalent
// connection with no privilege check in the way.
func TestSQLToolAdversarialWrites(t *testing.T) {
	reg, path := seedDB(t)
	before := userCount(t, path)

	attacks := []string{
		`DELETE FROM users`,
		`UPDATE users SET name = 'x'`,
		`DROP TABLE users`,
		`INSERT INTO users (id, name) VALUES (9, 'mallory')`,
		`TRUNCATE users`,
		`SELECT id FROM users; DROP TABLE users`,
		`SELECT id FROM users WHERE name = 'x'; DELETE FROM users`,
		`SELECT id FROM users -- ' OR 1=1`,
		`SELECT id FROM users WHERE name = 'a'/**/OR/**/1=1`,
	}
	for _, a := range attacks {
		if _, err := (sqlTool{}).Remote(context.Background(), dbEnv(reg),
			mustRaw(t, sqlArgs{DB: "app", Query: a})); err == nil {
			t.Errorf("attack query did not error: %q", a)
		}
	}

	if after := userCount(t, path); after != before {
		t.Errorf("user count changed from %d to %d; a write got through", before, after)
	}
}

func TestSQLToolInjectionViaValue(t *testing.T) {
	reg, path := seedDB(t)
	before := userCount(t, path)
	// A classic injection payload as a bound string value must be treated as data.
	res, err := sqlTool{}.Remote(context.Background(), dbEnv(reg),
		mustRaw(t, sqlArgs{DB: "app", Query: `SELECT id FROM users WHERE name = 'x''; DROP TABLE users; --'`}))
	if err != nil {
		t.Fatalf("value-position query should run harmlessly: %v", err)
	}
	// No user is named that, so no rows; and nothing was dropped.
	if strings.Count(res.Output, "\n") > 2 {
		t.Errorf("unexpected rows for injection payload:\n%s", res.Output)
	}
	if after := userCount(t, path); after != before {
		t.Errorf("user count changed from %d to %d", before, after)
	}
}

func TestSQLToolErrors(t *testing.T) {
	reg, _ := seedDB(t)
	env := dbEnv(reg)
	cases := []struct{ query, want string }{
		{`SELECT * FROM nonexistent`, "unknown table"},
		{`SELECT nope FROM users`, "unknown column"},
		{`SELECT id FROM users WHERE`, "expected"},
	}
	for _, c := range cases {
		_, err := sqlTool{}.Remote(context.Background(), env, mustRaw(t, sqlArgs{DB: "app", Query: c.query}))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Remote(%q) err = %v, want to contain %q", c.query, err, c.want)
		}
	}

	// Unknown alias and unconfigured server both fail closed.
	if _, err := (sqlTool{}).Remote(context.Background(), env, mustRaw(t, sqlArgs{DB: "missing", Query: `SELECT id FROM users`})); err == nil {
		t.Error("unknown alias should error")
	}
	if _, err := (sqlTool{}).Remote(context.Background(), Env{Limits: Limits{MaxOutput: 1 << 20}}, mustRaw(t, sqlArgs{DB: "app", Query: `SELECT id FROM users`})); err == nil {
		t.Error("no configured databases should error")
	}
}

func TestDBListTool(t *testing.T) {
	reg, _ := seedDB(t)
	res, err := dbList{}.Remote(context.Background(), dbEnv(reg), mustRaw(t, dbListArgs{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "app") || !strings.Contains(res.Output, "sqlite") {
		t.Errorf("db-list missing alias or engine:\n%s", res.Output)
	}
}

func TestDBTablesTool(t *testing.T) {
	reg, _ := seedDB(t)
	res, err := dbTables{}.Remote(context.Background(), dbEnv(reg), mustRaw(t, dbTablesArgs{DB: "app"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "users") || !strings.Contains(res.Output, "orders") {
		t.Errorf("db-tables missing tables:\n%s", res.Output)
	}
}

func TestDBSchemaTool(t *testing.T) {
	reg, _ := seedDB(t)
	res, err := dbSchema{}.Remote(context.Background(), dbEnv(reg), mustRaw(t, dbSchemaArgs{DB: "app", Table: "users"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"id", "name", "status", "age"} {
		if !strings.Contains(res.Output, col) {
			t.Errorf("db-schema missing column %q:\n%s", col, res.Output)
		}
	}
	// The unknown-table case fails closed.
	if _, err := (dbSchema{}).Remote(context.Background(), dbEnv(reg), mustRaw(t, dbSchemaArgs{DB: "app", Table: "nope"})); err == nil {
		t.Error("db-schema on unknown table should error")
	}
}
