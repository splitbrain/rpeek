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
