package tools

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"rpeek/internal/conf"
	"rpeek/internal/dbconn"
	"rpeek/internal/netutil"
	"rpeek/internal/server"
	"rpeek/internal/tlsutil"
	"rpeek/internal/version"
)

// defaultBindHost is the bind address serve uses when neither --host nor RPEEK_HOST is
// set: all interfaces on the default port.
const defaultBindHost = "0.0.0.0"

// serveTool runs the diagnostic server. It is the one ServerMode tool: instead of a
// one-shot Local/Remote result, its execution is the server process — it builds the jail
// and TLS listener from its flags, prints a banner, and serves until interrupted or the
// TTL elapses.
type serveTool struct{ readOnly }

func init() { register(serveTool{}) }

// Name returns the subcommand name.
func (serveTool) Name() string { return "serve" }

// Summary returns the one-line help description.
func (serveTool) Summary() string { return "run the read-only diagnostic server" }

// Usage returns the argument synopsis.
func (serveTool) Usage() string { return "serve [flags] [roots...]" }

// serveArgs are the parsed serve configuration. It is marshalled and decoded within the
// one process, never sent over the wire.
type serveArgs struct {
	// Host is the bind address, empty to fall back to RPEEK_HOST then all interfaces.
	Host string `json:"host,omitempty"`
	// Token is the fixed auth token, empty to fall back to RPEEK_TOKEN then a random one.
	Token string `json:"token,omitempty"`
	// TTL auto-shuts the server down after this duration; zero disables it.
	TTL time.Duration `json:"ttl,omitempty"`
	// MaxOutput is the global per-tool output byte cap.
	MaxOutput int `json:"max_output,omitempty"`
	// Timeout is the per-tool wall-clock deadline.
	Timeout time.Duration `json:"timeout,omitempty"`
	// Roots are the jail roots file tools may read within; empty means the working directory.
	Roots []string `json:"roots,omitempty"`
	// DBs maps a database alias to its DSN, from the repeatable --db flag. Env-configured
	// aliases (RPEEK_DB_<ALIAS>) are merged in at serve time.
	DBs map[string]string `json:"dbs,omitempty"`
}

// NewFlags builds the serve flag set and its argument builder. Positional arguments are
// the jail roots.
func (serveTool) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	host := fs.String("host", "", "bind address host or host:port (or RPEEK_HOST); default 0.0.0.0")
	token := fs.String("token", "", "fixed auth token (or RPEEK_TOKEN); generated if empty")
	ttl := fs.Duration("ttl", 30*time.Minute, "auto-shutdown after this duration; 0 disables")
	maxOutput := fs.Int("max-output", 1<<20, "global output byte cap applied by tools")
	timeout := fs.Duration("timeout", 15*time.Second, "per-tool wall-clock timeout")
	var dbs kvFlag
	fs.Var(&dbs, "db", "database as alias=dsn (repeatable; DSN also via RPEEK_DB_<ALIAS>)")
	return fs, func(pos []string) (any, error) {
		dbMap, err := dbs.pairs()
		if err != nil {
			return nil, err
		}
		return serveArgs{
			Host:      *host,
			Token:     *token,
			TTL:       *ttl,
			MaxOutput: *maxOutput,
			Timeout:   *timeout,
			Roots:     pos,
			DBs:       dbMap,
		}, nil
	}
}

// kvFlag collects repeated "alias=dsn" flag values.
type kvFlag []string

// String renders the collected values for the flag package.
func (k *kvFlag) String() string { return strings.Join(*k, ",") }

// Set appends one --db value.
func (k *kvFlag) Set(v string) error {
	*k = append(*k, v)
	return nil
}

// pairs parses the collected values into an alias→dsn map, rejecting a malformed spec or a
// duplicate alias. Aliases are lower-cased to match the RPEEK_DB_<ALIAS> environment form,
// so the two configuration sources address a connection by the same name and an explicit
// --db reliably overrides an env-configured alias of the same spelling.
func (k kvFlag) pairs() (map[string]string, error) {
	if len(k) == 0 {
		return nil, nil
	}
	m := make(map[string]string, len(k))
	for _, spec := range k {
		alias, dsn, ok := strings.Cut(spec, "=")
		if !ok || alias == "" || dsn == "" {
			return nil, fmt.Errorf("invalid --db %q: want alias=dsn", spec)
		}
		alias = strings.ToLower(alias)
		if _, dup := m[alias]; dup {
			return nil, fmt.Errorf("duplicate --db alias %q", alias)
		}
		m[alias] = dsn
	}
	return m, nil
}

// Serve builds the jail and TLS listener from the decoded arguments, prints the startup
// banner to stdout, and serves until ctx is cancelled, an OS signal arrives, or the TTL
// elapses.
func (serveTool) Serve(ctx context.Context, raw json.RawMessage, stdout io.Writer) error {
	args, err := decodeArgs[serveArgs](raw)
	if err != nil {
		return err
	}

	bind := conf.Resolve(args.Host, conf.EnvHost, defaultBindHost)
	listenAddr, err := netutil.NormalizeAddr(bind)
	if err != nil {
		return fmt.Errorf("invalid bind address: %v", err)
	}

	roots := args.Roots
	if len(roots) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("cannot determine working directory: %v", err)
		}
		roots = []string{cwd}
	}
	jail, err := NewJailSet(roots)
	if err != nil {
		return err
	}

	token := conf.Resolve(args.Token, conf.EnvToken, "")
	if token == "" {
		token, err = generateToken()
		if err != nil {
			return fmt.Errorf("cannot generate token: %v", err)
		}
	}

	// Merge database aliases from the environment with those given on the command line;
	// an explicit --db alias overrides an env-configured one of the same name.
	dbSpecs := conf.DBSpecs()
	for alias, dsn := range args.DBs {
		dbSpecs[alias] = dsn
	}
	var registry *dbconn.Registry
	if len(dbSpecs) > 0 {
		registry, err = dbconn.New(dbSpecs)
		if err != nil {
			return fmt.Errorf("cannot configure databases: %v", err)
		}
		defer registry.Close()
	}

	tlsCfg, err := tlsutil.ServerTLSConfig()
	if err != nil {
		return fmt.Errorf("cannot create TLS config: %v", err)
	}
	ln, err := tls.Listen("tcp", listenAddr, tlsCfg)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %v", listenAddr, err)
	}
	defer ln.Close()

	journalctlPath, _ := exec.LookPath("journalctl")
	logger := log.New(os.Stderr, "", log.LstdFlags)
	runner := NewRunner(Env{
		Jail:       jail,
		Limits:     Limits{MaxOutput: args.MaxOutput, Timeout: args.Timeout},
		Journalctl: journalctlPath,
		DB:         registry,
	})
	srv := server.NewServer(runner, token, logger)

	sctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if args.TTL > 0 {
		t := time.AfterFunc(args.TTL, stop)
		defer t.Stop()
	}

	serveBanner(stdout, listenAddr, jail.Roots(), token, args.TTL, registry)

	if err := srv.Serve(sctx, ln); err != nil {
		return fmt.Errorf("server error: %v", err)
	}
	logger.Printf("shutting down")
	return nil
}

// generateToken returns a 16-byte, hex-encoded random token.
func generateToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// serveBanner writes the startup banner describing the server's configuration to w. The
// database line lists each alias with its engine and host only; the DSN and its password
// are never printed.
func serveBanner(w io.Writer, listen string, roots []string, token string, ttl time.Duration, registry *dbconn.Registry) {
	ttlLine := "disabled (WARNING: server runs until stopped)"
	if ttl > 0 {
		ttlLine = fmt.Sprintf("%s (shuts down ~%s)", ttl, time.Now().Add(ttl).Format("15:04"))
	}
	fmt.Fprintf(w, "rpeek %s serve — read-only diagnostic server\n", version.Version)
	fmt.Fprintf(w, "listen : %s\n", listen)
	fmt.Fprintf(w, "jails  : %s   (file tools may read within these)\n", strings.Join(roots, ", "))
	fmt.Fprintf(w, "token  : %s   (pass to the client via --token or RPEEK_TOKEN)\n", token)
	fmt.Fprintf(w, "ttl    : %s\n", ttlLine)
	fmt.Fprintln(w, "tls    : ad-hoc self-signed; client skips verification by design")
	if registry != nil && len(registry.Aliases()) > 0 {
		fmt.Fprintf(w, "dbs    : %s   (passwords redacted)\n", bannerDBs(registry))
	}
	fmt.Fprintf(w, "tools  : %s   (READ-ONLY)\n", strings.Join(RemoteNames(), " "))
}

// bannerDBs renders the configured databases as "alias → engine@host" entries, without any
// credentials.
func bannerDBs(registry *dbconn.Registry) string {
	var parts []string
	for _, alias := range registry.Aliases() {
		conn, ok := registry.Lookup(alias)
		if !ok {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s → %s@%s", alias, conn.Engine(), conn.Host()))
	}
	return strings.Join(parts, ", ")
}
