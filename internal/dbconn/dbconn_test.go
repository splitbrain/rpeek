package dbconn

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rpeek/internal/sqlq"

	_ "modernc.org/sqlite"
)

// TestResolveDSN checks engine inference, driver selection, host extraction, and the
// per-engine hardening applied at open time.
func TestResolveDSN(t *testing.T) {
	cases := []struct {
		dsn        string
		wantEngine sqlq.Engine
		wantDriver string
		wantHost   string
		openHas    string // substring the open DSN must contain
		openHasNot string // substring the open DSN must not contain
	}{
		{"postgres://u:p@db.host:5432/app", sqlq.EnginePostgres, "pgx", "db.host:5432", "", ""},
		{"mysql://u:p@db.host:3306/app", sqlq.EngineMySQL, "mysql", "db.host:3306", "@tcp(db.host:3306)", "multiStatements=true"},
		{"sqlite:///var/lib/app.db", sqlq.EngineSQLite, "sqlite", "/var/lib/app.db", "_pragma=query_only(1)&_pragma=case_sensitive_like(1)", ""},
		{"sqlite://app.db", sqlq.EngineSQLite, "sqlite", "app.db", "_pragma=query_only(1)&_pragma=case_sensitive_like(1)", ""},
		{"sqlite:///var/lib/app.db?cache=shared", sqlq.EngineSQLite, "sqlite", "/var/lib/app.db", "_pragma=query_only(1)&_pragma=case_sensitive_like(1)", ""},
	}
	for _, c := range cases {
		engine, driver, openDSN, host, err := resolveDSN(c.dsn)
		if err != nil {
			t.Errorf("resolveDSN(%q): %v", c.dsn, err)
			continue
		}
		if engine != c.wantEngine {
			t.Errorf("resolveDSN(%q) engine = %v, want %v", c.dsn, engine, c.wantEngine)
		}
		if driver != c.wantDriver {
			t.Errorf("resolveDSN(%q) driver = %q, want %q", c.dsn, driver, c.wantDriver)
		}
		if host != c.wantHost {
			t.Errorf("resolveDSN(%q) host = %q, want %q", c.dsn, host, c.wantHost)
		}
		if c.openHas != "" && !strings.Contains(openDSN, c.openHas) {
			t.Errorf("resolveDSN(%q) openDSN = %q, want to contain %q", c.dsn, openDSN, c.openHas)
		}
		if c.openHasNot != "" && strings.Contains(openDSN, c.openHasNot) {
			t.Errorf("resolveDSN(%q) openDSN = %q, must not contain %q", c.dsn, openDSN, c.openHasNot)
		}
	}
}

// TestHardenMySQLStripsUnsafeParams checks that multiStatements and LOCAL INFILE file
// access cannot be re-enabled through DSN query parameters. A value left in the driver's
// generic parameter map survives FormatDSN and is parsed back into its config field on
// open, so stripping it is what makes the hardening hold.
func TestHardenMySQLStripsUnsafeParams(t *testing.T) {
	dsn := "mysql://u:p@db.host:3306/app?multiStatements=true&allowAllFiles=true"
	_, _, openDSN, _, err := resolveDSN(dsn)
	if err != nil {
		t.Fatalf("resolveDSN(%q): %v", dsn, err)
	}
	for _, forbidden := range []string{"multiStatements=true", "allowAllFiles=true"} {
		if strings.Contains(openDSN, forbidden) {
			t.Errorf("resolveDSN(%q) openDSN = %q, must not contain %q", dsn, openDSN, forbidden)
		}
	}
}

// TestResolveDSNRejects checks that anything but the three accepted schemes fails loudly at
// startup rather than being misread. The accepted forms are covered by TestResolveDSN.
func TestResolveDSNRejects(t *testing.T) {
	bad := []string{
		"",                             // empty
		"postgresql://u:p@db.host/app", // only postgres:// is accepted
		"postgre://u:p@db.host/app",    // typo'd postgres://
		"redis://localhost:6379",
		"mongodb://db.host/app",
		"u:p@tcp(db.host:3306)/app", // native mysql DSN is not accepted
		"file:/var/lib/app.db",      // only sqlite:// is accepted
		"sqlite:/var/lib/app.db",    // single-colon form is not accepted
		"sqlite3:///var/lib/app.db",
		"/var/lib/app.db", // a bare path is not accepted
		"app.db",
		":memory:",
	}
	for _, dsn := range bad {
		if _, _, _, _, err := resolveDSN(dsn); err == nil {
			t.Errorf("resolveDSN(%q) = nil error, want rejection", dsn)
		}
	}
}

// TestAliasPattern checks alias validation.
func TestAliasPattern(t *testing.T) {
	valid := []string{"app", "analytics", "db1", "_x", "App_2"}
	invalid := []string{"", "1db", "with-hyphen", "with space", "a.b", "x=y"}
	for _, a := range valid {
		if !aliasPattern(a) {
			t.Errorf("aliasPattern(%q) = false, want true", a)
		}
	}
	for _, a := range invalid {
		if aliasPattern(a) {
			t.Errorf("aliasPattern(%q) = true, want false", a)
		}
	}
}

// seedSQLite creates a writable SQLite database file with a couple of rows and returns its
// path.
func seedSQLite(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`INSERT INTO users (id, name) VALUES (1, 'alice'), (2, 'bob')`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// TestRegistry checks alias lookup, listing, and engine/host reporting.
func TestRegistry(t *testing.T) {
	path := seedSQLite(t)
	reg, err := New(map[string]string{"app": "sqlite://" + path})
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	if got := reg.Aliases(); len(got) != 1 || got[0] != "app" {
		t.Errorf("Aliases = %v, want [app]", got)
	}
	conn, ok := reg.Lookup("app")
	if !ok {
		t.Fatal("Lookup(app) not found")
	}
	if conn.Engine() != sqlq.EngineSQLite {
		t.Errorf("Engine = %v, want sqlite", conn.Engine())
	}
	if conn.Host() != path {
		t.Errorf("Host = %q, want %q", conn.Host(), path)
	}
	if _, ok := reg.Lookup("nope"); ok {
		t.Error("Lookup(nope) should not be found")
	}
}

// TestNewRejectsBadAlias checks that an invalid alias fails registry construction.
func TestNewRejectsBadAlias(t *testing.T) {
	if _, err := New(map[string]string{"bad-alias": "sqlite://x.db"}); err == nil {
		t.Error("New with invalid alias should fail")
	}
}

// TestCaseSensitiveLikePragma checks that the SQLite connection is opened with
// case_sensitive_like enabled, so a plain LIKE — the SQL the translator emits for the LIKE
// predicate — matches case-sensitively, the same as it does on PostgreSQL and MySQL. Without
// the pragma SQLite's LIKE folds ASCII case and 'ALICE%' would match 'alice'.
func TestCaseSensitiveLikePragma(t *testing.T) {
	path := seedSQLite(t)
	reg, err := New(map[string]string{"app": "sqlite://" + path})
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	conn, _ := reg.Lookup("app")

	ctx := context.Background()
	tx, err := conn.BeginReadOnly(ctx, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	count := func(where string) int {
		t.Helper()
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE `+where).Scan(&n); err != nil {
			t.Fatalf("query %q failed: %v", where, err)
		}
		return n
	}

	// Case-sensitive LIKE: the lower-case pattern matches, the upper-case one does not.
	if n := count(`name LIKE 'alice%'`); n != 1 {
		t.Errorf("LIKE 'alice%%' matched %d rows, want 1", n)
	}
	if n := count(`name LIKE 'ALICE%'`); n != 0 {
		t.Errorf("LIKE 'ALICE%%' matched %d rows, want 0 (LIKE must be case-sensitive)", n)
	}
	// Case-insensitive form, as the translator renders ILIKE on SQLite once the pragma is on.
	if n := count(`LOWER(name) LIKE LOWER('ALICE%')`); n != 1 {
		t.Errorf("LOWER(name) LIKE LOWER('ALICE%%') matched %d rows, want 1", n)
	}
}

// TestReadOnlyEnforced checks that the SQLite connection is opened read-only: a write fails
// even though the grammar would never emit one. This is the defence-in-depth layer.
func TestReadOnlyEnforced(t *testing.T) {
	path := seedSQLite(t)
	reg, err := New(map[string]string{"app": "sqlite://" + path})
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	conn, _ := reg.Lookup("app")

	ctx := context.Background()
	tx, err := conn.BeginReadOnly(ctx, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	// A read works.
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2", n)
	}

	// A write is rejected by the connection, independent of the grammar.
	if _, err := conn.db.ExecContext(ctx, `INSERT INTO users (id, name) VALUES (3, 'mallory')`); err == nil {
		t.Error("write succeeded; query_only not enforced")
	}
}
