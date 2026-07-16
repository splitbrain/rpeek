// Command diagd is the read-only diagnostic server. It is copied onto a remote host
// and run there, listens on TLS, authenticates callers with a token it prints at
// startup, and exposes a fixed set of read-only diagnostic tools.
package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"diag/internal/netutil"
	"diag/internal/server"
	"diag/internal/tlsutil"
)

func main() {
	listen := flag.String("listen", "0.0.0.0", "bind address (host or host:port; port defaults to 7017); use 127.0.0.1 to restrict to local")
	tokenFlag := flag.String("token", "", "fixed auth token; generated if empty")
	ttl := flag.Duration("ttl", 30*time.Minute, "auto-shutdown after this duration; 0 disables")
	maxOutput := flag.Int("max-output", 1<<20, "global output byte cap applied by tools")
	timeout := flag.Duration("timeout", 15*time.Second, "per-tool wall-clock timeout")
	flag.Parse()

	listenAddr, err := netutil.NormalizeAddr(*listen)
	if err != nil {
		fatalf("invalid --listen address: %v", err)
	}

	roots := flag.Args()
	if len(roots) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			fatalf("cannot determine working directory: %v", err)
		}
		roots = []string{cwd}
	}

	jail, err := server.NewJailSet(roots)
	if err != nil {
		fatalf("%v", err)
	}

	token := *tokenFlag
	if token == "" {
		token = generateToken()
	}

	tlsCfg, err := tlsutil.ServerTLSConfig()
	if err != nil {
		fatalf("cannot create TLS config: %v", err)
	}

	ln, err := tls.Listen("tcp", listenAddr, tlsCfg)
	if err != nil {
		fatalf("cannot listen on %s: %v", listenAddr, err)
	}
	defer ln.Close()

	logger := log.New(os.Stderr, "", log.LstdFlags)
	srv := server.NewServer(jail, token, server.Limits{
		MaxOutput: *maxOutput,
		Timeout:   *timeout,
	}, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *ttl > 0 {
		t := time.AfterFunc(*ttl, stop)
		defer t.Stop()
	}

	printBanner(os.Stdout, listenAddr, jail.Roots(), token, *ttl)

	if err := srv.Serve(ctx, ln); err != nil {
		fatalf("server error: %v", err)
	}
	logger.Printf("shutting down")
}

// generateToken returns a 16-byte, hex-encoded random token.
func generateToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		fatalf("cannot generate token: %v", err)
	}
	return hex.EncodeToString(b)
}

// printBanner writes the startup banner describing the server's configuration to w.
func printBanner(w *os.File, listen string, roots []string, token string, ttl time.Duration) {
	ttlLine := "disabled (WARNING: server runs until stopped)"
	if ttl > 0 {
		ttlLine = fmt.Sprintf("%s (shuts down ~%s)", ttl, time.Now().Add(ttl).Format("15:04"))
	}
	fmt.Fprintln(w, "diagd — read-only diagnostic server")
	fmt.Fprintf(w, "listen : %s\n", listen)
	fmt.Fprintf(w, "jails  : %s   (file tools may read within these)\n", strings.Join(roots, ", "))
	fmt.Fprintf(w, "token  : %s   (pass to diagctl via --token or DIAG_TOKEN)\n", token)
	fmt.Fprintf(w, "ttl    : %s\n", ttlLine)
	fmt.Fprintln(w, "tls    : ad-hoc self-signed; client uses --insecure by design")
	fmt.Fprintf(w, "tools  : %s   (READ-ONLY)\n", strings.Join(server.ToolNames(), " "))
}

// fatalf prints a formatted error to stderr and exits with status 1.
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "diagd: "+format+"\n", args...)
	os.Exit(1)
}
