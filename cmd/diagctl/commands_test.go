package main

import (
	"errors"
	"flag"
	"testing"

	"diag/internal/protocol"
)

func TestToolOrderMatchesRegistry(t *testing.T) {
	if len(toolOrder) != len(tools) {
		t.Fatalf("toolOrder has %d entries, registry has %d", len(toolOrder), len(tools))
	}
	for _, name := range toolOrder {
		if _, ok := tools[name]; !ok {
			t.Errorf("toolOrder names %q, absent from registry", name)
		}
	}
}

func TestBuildArgsOrderIndependent(t *testing.T) {
	want := protocol.ReadArgs{Path: "/var/log/syslog", MaxBytes: 5, Offset: 2}
	cases := [][]string{
		{"/var/log/syslog", "--max-bytes", "5", "--offset", "2"},
		{"--max-bytes", "5", "--offset", "2", "/var/log/syslog"},
		{"--max-bytes", "5", "/var/log/syslog", "--offset", "2"},
		{"--max-bytes=5", "/var/log/syslog", "--offset=2"},
	}
	for _, args := range cases {
		got, err := buildToolArgs(tools["read"], args)
		if err != nil {
			t.Errorf("buildToolArgs(read, %v) error: %v", args, err)
			continue
		}
		if got != any(want) {
			t.Errorf("buildToolArgs(read, %v) = %+v, want %+v", args, got, want)
		}
	}
}

func TestBuildArgsGrep(t *testing.T) {
	got, err := buildToolArgs(tools["grep"], []string{"/var/log", "--pattern", "ERROR", "--ignore-case"})
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.GrepArgs{Path: "/var/log", Pattern: "ERROR", IgnoreCase: true}
	if got != any(want) {
		t.Errorf("grep args = %+v, want %+v", got, want)
	}
}

func TestBuildArgsGrepRequiresPattern(t *testing.T) {
	_, err := buildToolArgs(tools["grep"], []string{"/var/log"})
	if err == nil {
		t.Error("grep without --pattern should error")
	}
}

func TestBuildArgsMissingPath(t *testing.T) {
	if _, err := buildToolArgs(tools["read"], []string{"--max-bytes", "5"}); err == nil {
		t.Error("read without a path should error")
	}
}

func TestBuildArgsTooManyPaths(t *testing.T) {
	if _, err := buildToolArgs(tools["stat"], []string{"/a", "/b"}); err == nil {
		t.Error("stat with two paths should error")
	}
}

func TestBuildArgsNoPositionalsTools(t *testing.T) {
	if _, err := buildToolArgs(tools["ps"], nil); err != nil {
		t.Errorf("ps with no args should succeed: %v", err)
	}
	if _, err := buildToolArgs(tools["ps"], []string{"/unexpected"}); err == nil {
		t.Error("ps with a positional should error")
	}
}

func TestBuildArgsJournal(t *testing.T) {
	got, err := buildToolArgs(tools["journal"], []string{"--unit", "nginx", "--lines", "50"})
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.JournalArgs{Unit: "nginx", Lines: 50}
	if got != any(want) {
		t.Errorf("journal args = %+v, want %+v", got, want)
	}
}

func TestBuildArgsHelp(t *testing.T) {
	for _, flagName := range []string{"-h", "--help"} {
		_, err := buildToolArgs(tools["read"], []string{flagName})
		if !errors.Is(err, flag.ErrHelp) {
			t.Errorf("buildToolArgs(read, %q) err = %v, want flag.ErrHelp", flagName, err)
		}
	}
	// --help still wins when a positional is also present.
	if _, err := buildToolArgs(tools["read"], []string{"/p", "--help"}); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("help after positional: err = %v, want flag.ErrHelp", err)
	}
}

func TestBuildArgsUnknownFlag(t *testing.T) {
	if _, err := buildToolArgs(tools["read"], []string{"/p", "--bogus"}); err == nil {
		t.Error("unknown flag should error")
	}
}
