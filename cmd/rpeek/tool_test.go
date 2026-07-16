package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"testing"

	"rpeek/internal/tools"
)

// buildArgs drives a tool's client-side flag parsing the way runTool does — building the
// tool's flag set, parsing flags and positionals in any order, and running the builder —
// then returns the resulting wire argument value.
func buildArgs(t *testing.T, name string, args []string) (any, error) {
	t.Helper()
	tool, ok := tools.Lookup(name)
	if !ok {
		t.Fatalf("unknown tool %q", name)
	}
	fs, build := tool.NewFlags()
	fs.SetOutput(io.Discard)
	positionals, err := parseFlagsAnyOrder(fs, args)
	if err != nil {
		return nil, err
	}
	return build(positionals)
}

// wireJSON marshals a built argument value to its on-the-wire JSON form, the only stable
// view of an argument struct whose concrete type is unexported by the tools package.
func wireJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBuildArgsOrderIndependent(t *testing.T) {
	const want = `{"path":"/var/log/syslog","max_bytes":5,"offset":2}`
	cases := [][]string{
		{"/var/log/syslog", "--max-bytes", "5", "--offset", "2"},
		{"--max-bytes", "5", "--offset", "2", "/var/log/syslog"},
		{"--max-bytes", "5", "/var/log/syslog", "--offset", "2"},
		{"--max-bytes=5", "/var/log/syslog", "--offset=2"},
	}
	for _, args := range cases {
		got, err := buildArgs(t, "read", args)
		if err != nil {
			t.Errorf("buildArgs(read, %v) error: %v", args, err)
			continue
		}
		if j := wireJSON(t, got); j != want {
			t.Errorf("buildArgs(read, %v) = %s, want %s", args, j, want)
		}
	}
}

func TestBuildArgsGrep(t *testing.T) {
	got, err := buildArgs(t, "grep", []string{"/var/log", "--pattern", "ERROR", "--ignore-case"})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"path":"/var/log","pattern":"ERROR","ignore_case":true}`
	if j := wireJSON(t, got); j != want {
		t.Errorf("grep args = %s, want %s", j, want)
	}
}

func TestBuildArgsGrepRequiresPattern(t *testing.T) {
	if _, err := buildArgs(t, "grep", []string{"/var/log"}); err == nil {
		t.Error("grep without --pattern should error")
	}
}

func TestBuildArgsMissingPath(t *testing.T) {
	if _, err := buildArgs(t, "read", []string{"--max-bytes", "5"}); err == nil {
		t.Error("read without a path should error")
	}
}

func TestBuildArgsTooManyPaths(t *testing.T) {
	if _, err := buildArgs(t, "stat", []string{"/a", "/b"}); err == nil {
		t.Error("stat with two paths should error")
	}
}

func TestBuildArgsNoPositionalsTools(t *testing.T) {
	if _, err := buildArgs(t, "ps", nil); err != nil {
		t.Errorf("ps with no args should succeed: %v", err)
	}
	if _, err := buildArgs(t, "ps", []string{"/unexpected"}); err == nil {
		t.Error("ps with a positional should error")
	}
}

func TestBuildArgsJournal(t *testing.T) {
	got, err := buildArgs(t, "journal", []string{"--unit", "nginx", "--lines", "50"})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"unit":"nginx","lines":50}`
	if j := wireJSON(t, got); j != want {
		t.Errorf("journal args = %s, want %s", j, want)
	}
}

func TestBuildArgsHelp(t *testing.T) {
	for _, flagName := range []string{"-h", "--help"} {
		if _, err := buildArgs(t, "read", []string{flagName}); !errors.Is(err, flag.ErrHelp) {
			t.Errorf("buildArgs(read, %q) err = %v, want flag.ErrHelp", flagName, err)
		}
	}
	// --help still wins when a positional is also present.
	if _, err := buildArgs(t, "read", []string{"/p", "--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("help after positional: err = %v, want flag.ErrHelp", err)
	}
}

func TestBuildArgsUnknownFlag(t *testing.T) {
	if _, err := buildArgs(t, "read", []string{"/p", "--bogus"}); err == nil {
		t.Error("unknown flag should error")
	}
}
