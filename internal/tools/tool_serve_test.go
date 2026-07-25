package tools

import (
	"testing"
	"time"
)

func TestServeTool(t *testing.T) {
	// serve is the sole ServerMode; it is neither a LocalTool nor a RemoteTool.
	var st Tool = serveTool{}
	if _, ok := st.(ServerMode); !ok {
		t.Error("serve must implement ServerMode")
	}
	if _, ok := st.(RemoteTool); ok {
		t.Error("serve must not implement RemoteTool")
	}
	if _, ok := st.(LocalTool); ok {
		t.Error("serve must not implement LocalTool")
	}

	// NewFlags parses the serve flags and collects roots as positionals.
	fs, build := serveTool{}.NewFlags()
	if err := fs.Parse([]string{"--ttl", "5m", "--token", "abc", "/var/log", "/etc"}); err != nil {
		t.Fatal(err)
	}
	v, err := build(fs.Args())
	if err != nil {
		t.Fatal(err)
	}
	args, ok := v.(serveArgs)
	if !ok {
		t.Fatalf("build returned %T, want serveArgs", v)
	}
	if args.Token != "abc" {
		t.Errorf("token = %q, want %q", args.Token, "abc")
	}
	if args.TTL != 5*time.Minute {
		t.Errorf("ttl = %s, want 5m", args.TTL)
	}
	if len(args.Roots) != 2 || args.Roots[0] != "/var/log" || args.Roots[1] != "/etc" {
		t.Errorf("roots = %v, want [/var/log /etc]", args.Roots)
	}
}

// TestServeDBFlags checks that repeated --db flags are collected into serveArgs.DBs with
// their aliases lower-cased, alongside the jail roots.
func TestServeDBFlags(t *testing.T) {
	fs, build := serveTool{}.NewFlags()
	if err := fs.Parse([]string{"--db", "App=sqlite://a.db", "--db", "reports=postgres://u:p@h/r", "/var/log"}); err != nil {
		t.Fatal(err)
	}
	v, err := build(fs.Args())
	if err != nil {
		t.Fatal(err)
	}
	args, ok := v.(serveArgs)
	if !ok {
		t.Fatalf("build returned %T, want serveArgs", v)
	}
	if args.DBs["app"] != "sqlite://a.db" {
		t.Errorf(`DBs["app"] = %q, want "sqlite://a.db"`, args.DBs["app"])
	}
	if args.DBs["reports"] != "postgres://u:p@h/r" {
		t.Errorf(`DBs["reports"] = %q, want "postgres://u:p@h/r"`, args.DBs["reports"])
	}
	if len(args.Roots) != 1 || args.Roots[0] != "/var/log" {
		t.Errorf("roots = %v, want [/var/log]", args.Roots)
	}
}

// TestKVFlagPairs checks the --db spec parser: it splits on the first '=' so a DSN may
// itself contain '=', lower-cases the alias, and rejects a malformed spec, an empty alias
// or DSN, and a duplicate alias.
func TestKVFlagPairs(t *testing.T) {
	// The DSN carries a query string with its own '='; the alias is upper-case here.
	k := kvFlag{"app=postgres://u:p@h/db?sslmode=require", "Analytics=sqlite://an.db"}
	m, err := k.pairs()
	if err != nil {
		t.Fatalf("pairs() error: %v", err)
	}
	if m["app"] != "postgres://u:p@h/db?sslmode=require" {
		t.Errorf(`m["app"] = %q, want the full DSN including its query string`, m["app"])
	}
	if m["analytics"] != "sqlite://an.db" {
		t.Errorf(`m["analytics"] = %q, want the alias lower-cased to "analytics"`, m["analytics"])
	}

	// No --db flags yields a nil map and no error.
	if got, err := (kvFlag{}).pairs(); err != nil || got != nil {
		t.Errorf("pairs() on empty = (%v, %v), want (nil, nil)", got, err)
	}

	bad := []kvFlag{
		{"noequals"},       // missing '='
		{"=dsn"},           // empty alias
		{"app="},           // empty DSN
		{"app=x", "APP=y"}, // duplicate alias after lower-casing
	}
	for _, spec := range bad {
		if _, err := spec.pairs(); err == nil {
			t.Errorf("pairs(%v) = nil error, want rejection", []string(spec))
		}
	}
}
