package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
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

	"diag/internal/netutil"
	"diag/internal/server"
	"diag/internal/tlsutil"
	"diag/internal/tools"
)

// defaultBindHost is the bind address serve uses when neither --host nor DIAG_HOST is
// set: all interfaces on the default port.
const defaultBindHost = "0.0.0.0"

// runServe parses the serve flags, builds the jail and TLS listener, prints the startup
// banner, and serves until interrupted or the TTL elapses. gHost and gToken are the
// values of any --host/--token given before the subcommand; they seed the corresponding
// flags so an explicit --host/--token after "serve" overrides them. It returns the
// process exit code.
func runServe(args []string, gHost, gToken string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // render help and errors ourselves for consistent formatting
	host := fs.String("host", gHost, "bind address host or host:port (or DIAG_HOST); default 0.0.0.0")
	tokenFlag := fs.String("token", gToken, "fixed auth token (or DIAG_TOKEN); generated if empty")
	ttl := fs.Duration("ttl", 30*time.Minute, "auto-shutdown after this duration; 0 disables")
	maxOutput := fs.Int("max-output", 1<<20, "global output byte cap applied by tools")
	timeout := fs.Duration("timeout", 15*time.Second, "per-tool wall-clock timeout")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printServeHelp(os.Stdout, fs)
			return exitOK
		}
		return usageErr("%v", err)
	}

	bind := *host
	if bind == "" {
		bind = os.Getenv("DIAG_HOST")
	}
	if bind == "" {
		bind = defaultBindHost
	}
	listenAddr, err := netutil.NormalizeAddr(bind)
	if err != nil {
		return fatalf("invalid bind address: %v", err)
	}

	roots := fs.Args()
	if len(roots) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			return fatalf("cannot determine working directory: %v", err)
		}
		roots = []string{cwd}
	}

	jail, err := tools.NewJailSet(roots)
	if err != nil {
		return fatalf("%v", err)
	}

	token := *tokenFlag
	if token == "" {
		token = os.Getenv("DIAG_TOKEN")
	}
	if token == "" {
		token, err = generateToken()
		if err != nil {
			return fatalf("cannot generate token: %v", err)
		}
	}

	tlsCfg, err := tlsutil.ServerTLSConfig()
	if err != nil {
		return fatalf("cannot create TLS config: %v", err)
	}

	ln, err := tls.Listen("tcp", listenAddr, tlsCfg)
	if err != nil {
		return fatalf("cannot listen on %s: %v", listenAddr, err)
	}
	defer ln.Close()

	journalctlPath, _ := exec.LookPath("journalctl")
	logger := log.New(os.Stderr, "", log.LstdFlags)
	srv := server.NewServer(jail, token, tools.Limits{
		MaxOutput: *maxOutput,
		Timeout:   *timeout,
	}, journalctlPath, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *ttl > 0 {
		t := time.AfterFunc(*ttl, stop)
		defer t.Stop()
	}

	printBanner(os.Stdout, listenAddr, jail.Roots(), token, *ttl)

	if err := srv.Serve(ctx, ln); err != nil {
		return fatalf("server error: %v", err)
	}
	logger.Printf("shutting down")
	return exitOK
}

// generateToken returns a 16-byte, hex-encoded random token.
func generateToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// printBanner writes the startup banner describing the server's configuration to w.
func printBanner(w io.Writer, listen string, roots []string, token string, ttl time.Duration) {
	ttlLine := "disabled (WARNING: server runs until stopped)"
	if ttl > 0 {
		ttlLine = fmt.Sprintf("%s (shuts down ~%s)", ttl, time.Now().Add(ttl).Format("15:04"))
	}
	fmt.Fprintln(w, "diag serve — read-only diagnostic server")
	fmt.Fprintf(w, "listen : %s\n", listen)
	fmt.Fprintf(w, "jails  : %s   (file tools may read within these)\n", strings.Join(roots, ", "))
	fmt.Fprintf(w, "token  : %s   (pass to the client via --token or DIAG_TOKEN)\n", token)
	fmt.Fprintf(w, "ttl    : %s\n", ttlLine)
	fmt.Fprintln(w, "tls    : ad-hoc self-signed; client skips verification by design")
	fmt.Fprintf(w, "tools  : %s   (READ-ONLY)\n", strings.Join(tools.Names(), " "))
}

// printServeHelp writes the serve subcommand's usage and flags to w.
func printServeHelp(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintln(w, "Usage: diag serve [--host HOST[:PORT]] [--token TOKEN] [flags] [roots...]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Runs the read-only diagnostic server. With no roots given, it jails to the")
	fmt.Fprintln(w, "current working directory. It prints an auth token at startup for the client")
	fmt.Fprintln(w, "to authenticate with.")
	fmt.Fprintln(w, "\nFlags:")
	fs.SetOutput(w)
	fs.PrintDefaults()
}
