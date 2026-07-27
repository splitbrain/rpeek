// Package dbconn manages the databases rpeek may query. Connections are configured at
// serve time as alias→DSN pairs; the client selects one by alias and never supplies or
// sees a DSN. That closes SSRF and pivoting (the client cannot aim rpeek at an arbitrary
// host), malicious-server attacks such as MySQL's LOCAL INFILE, and credential custody
// (the password never enters the untrusted client's context). The engine is inferred from
// the DSN scheme, and every connection is opened hardened and read-only.
package dbconn

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"rpeek/internal/sqlq"

	"github.com/go-sql-driver/mysql"
	// Database drivers register themselves on import. All three are pure Go, so the static
	// binary and cross-compilation model is preserved.
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Pool limits kept deliberately small: a diagnostic server issues occasional queries, not
// sustained load, and modest limits bound the footprint on the target database.
const (
	// maxOpenConns caps concurrently open connections per alias.
	maxOpenConns = 4
	// maxIdleConns caps idle connections retained per alias.
	maxIdleConns = 2
	// connMaxIdleTime bounds how long an idle connection is kept.
	connMaxIdleTime = 5 * time.Minute
)

// aliasPattern constrains alias names so they map cleanly to RPEEK_DB_<ALIAS> environment
// variables and appear safely in banners and error messages.
var aliasPattern = func(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9' && i > 0)
		if !ok {
			return false
		}
	}
	return true
}

// Conn is one configured database: its pooled handle, engine, and the host or file it
// addresses. The host carries no credentials and is safe to display.
type Conn struct {
	// db is the pooled connection handle.
	db *sql.DB

	// engine is the inferred engine, selecting the query dialect and introspection SQL.
	engine sqlq.Engine

	// host is the server address or file path the connection targets, without credentials.
	host string
}

// Engine returns the connection's engine.
func (c *Conn) Engine() sqlq.Engine { return c.engine }

// Host returns the credential-free host or file path the connection targets.
func (c *Conn) Host() string { return c.host }

// BeginReadOnly starts a read-only transaction and applies the per-query timeout the engine
// supports. The read-only transaction is defence in depth behind the grammar; on Postgres
// and MySQL the driver enforces it, and on SQLite the connection is opened with query_only
// so a write cannot occur regardless. The caller must Rollback the returned transaction.
func (c *Conn) BeginReadOnly(ctx context.Context, timeout time.Duration) (*sql.Tx, error) {
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	if timeout > 0 && c.engine == sqlq.EnginePostgres {
		// SET LOCAL is scoped to the transaction and resets on rollback, so it does not
		// leak the timeout onto the pooled connection. The value is server-derived, not
		// client text. Postgres SET takes no bind parameters. MySQL and SQLite rely on the
		// context deadline, which their drivers honour.
		ms := timeout.Milliseconds()
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", ms)); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	return tx, nil
}

// Registry holds the configured connections, keyed by alias.
type Registry struct {
	// conns maps an alias to its connection.
	conns map[string]*Conn

	// aliases lists the configured aliases in sorted order for stable listing.
	aliases []string
}

// New opens one hardened, read-only pooled connection per alias in specs and returns the
// registry. Each DSN's engine is inferred from its scheme. Opening does not dial the
// database, so a server whose database is temporarily down still starts; connection errors
// surface on the first query. It fails on an invalid alias, an unrecognized DSN, or a
// driver-rejected DSN.
func New(specs map[string]string) (*Registry, error) {
	reg := &Registry{conns: make(map[string]*Conn, len(specs))}
	for alias, dsn := range specs {
		if !aliasPattern(alias) {
			reg.Close()
			return nil, fmt.Errorf("invalid db alias %q: use letters, digits, and underscores, starting with a letter or underscore", alias)
		}
		conn, err := open(dsn)
		if err != nil {
			reg.Close()
			return nil, fmt.Errorf("db alias %q: %w", alias, err)
		}
		reg.conns[alias] = conn
		reg.aliases = append(reg.aliases, alias)
	}
	sort.Strings(reg.aliases)
	return reg, nil
}

// Lookup returns the connection configured under alias and whether one exists.
func (r *Registry) Lookup(alias string) (*Conn, bool) {
	c, ok := r.conns[alias]
	return c, ok
}

// Aliases returns the configured aliases in sorted order.
func (r *Registry) Aliases() []string {
	return append([]string(nil), r.aliases...)
}

// Close closes every pooled connection, returning the first error encountered.
func (r *Registry) Close() error {
	var firstErr error
	for _, c := range r.conns {
		if c.db != nil {
			if err := c.db.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// open infers the engine from dsn, opens a hardened pooled connection, and records the
// credential-free host it targets.
func open(dsn string) (*Conn, error) {
	engine, driver, openDSN, host, err := resolveDSN(dsn)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driver, openDSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxIdleTime(connMaxIdleTime)
	return &Conn{db: db, engine: engine, host: host}, nil
}

// resolveDSN infers the engine from the DSN scheme and returns the engine, the database/sql
// driver name, the DSN to open with (hardened), and the credential-free host to display. The
// scheme must be exactly postgres://, mysql://, or sqlite://; any other DSN is rejected, so
// an operator's typo or an unsupported engine fails at startup rather than at first query.
func resolveDSN(dsn string) (engine sqlq.Engine, driver, openDSN, host string, err error) {
	switch {
	case strings.HasPrefix(dsn, "postgres://"):
		return sqlq.EnginePostgres, "pgx", dsn, urlHost(dsn), nil
	case strings.HasPrefix(dsn, "mysql://"):
		openDSN, host, err = mysqlURLToDSN(dsn)
		if err != nil {
			return 0, "", "", "", err
		}
		return sqlq.EngineMySQL, "mysql", openDSN, host, nil
	case strings.HasPrefix(dsn, "sqlite://"):
		openDSN, host = sqliteDSN(dsn)
		return sqlq.EngineSQLite, "sqlite", openDSN, host, nil
	default:
		return 0, "", "", "", fmt.Errorf("unrecognized DSN %q: use postgres://, mysql://, or sqlite://", dsn)
	}
}

// urlHost returns the host:port of a URL-form DSN, without any credentials.
func urlHost(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return u.Host
}

// mysqlURLToDSN converts a mysql:// URL into the go-sql-driver DSN, forcing the safety flags
// on, and returns it with the credential-free host. Building through mysql.Config carries
// the password as a field so DSN escaping cannot corrupt it.
func mysqlURLToDSN(dsn string) (openDSN, host string, err error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", "", fmt.Errorf("invalid mysql URL: %w", err)
	}
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = u.Host
	cfg.User = u.User.Username()
	if pw, ok := u.User.Password(); ok {
		cfg.Passwd = pw
	}
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	if q := u.Query(); len(q) > 0 {
		cfg.Params = map[string]string{}
		for k, vs := range q {
			cfg.Params[k] = vs[len(vs)-1]
		}
	}
	hardenMySQLConfig(cfg)
	return cfg.FormatDSN(), u.Host, nil
}

// hardenMySQLConfig disables the features an untrusted client or a malicious server could
// abuse: multi-statement queries and any LOAD DATA LOCAL INFILE file access. The generic
// parameter map is purged of the same keys as well, because a value left there survives
// FormatDSN and is parsed back into the struct field on open, which would re-enable the
// feature the struct field below just disabled.
func hardenMySQLConfig(cfg *mysql.Config) {
	cfg.MultiStatements = false
	cfg.AllowAllFiles = false
	delete(cfg.Params, "multiStatements")
	delete(cfg.Params, "allowAllFiles")
}

// sqliteDSN converts a sqlite:// DSN to the modernc file URI with query_only and
// case_sensitive_like enabled and returns it with the underlying file path. An absolute path
// is written sqlite:///path and a relative one sqlite://path; both reduce to stripping the
// scheme. query_only makes every pooled connection reject writes, the read-only enforcement
// SQLite's read-only transaction option does not provide. case_sensitive_like makes LIKE
// case-sensitive on every pooled connection, so LIKE means the same thing on SQLite as it
// does on PostgreSQL and MySQL; the case-insensitive ILIKE predicate is translated
// explicitly rather than relying on SQLite's default LIKE folding. Both _pragma parameters
// are applied by modernc on each new connection.
func sqliteDSN(dsn string) (openDSN, path string) {
	path = strings.TrimPrefix(dsn, "sqlite://")
	// Strip any existing query string from the reported path for a clean display value.
	displayPath := path
	if i := strings.IndexByte(displayPath, '?'); i >= 0 {
		displayPath = displayPath[:i]
	}

	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return "file:" + path + sep + "_pragma=query_only(1)&_pragma=case_sensitive_like(1)", displayPath
}
