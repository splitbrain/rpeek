package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestToolGrepFile(t *testing.T) {
	j, dir := fixtureJail(t)
	res, err := grep{}.Remote(context.Background(), testEnv(j),
		mustRaw(t, grepArgs{Path: filepath.Join(dir, "beta.log"), Pattern: "ERROR"}))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Count(res.Output, "\n")
	if lines != 2 {
		t.Errorf("expected 2 ERROR matches, got %d:\n%s", lines, res.Output)
	}
	if !strings.Contains(res.Output, "beta.log:2: ERROR boom") {
		t.Errorf("grep output missing expected line:\n%s", res.Output)
	}
}

func TestToolGrepIgnoreCase(t *testing.T) {
	j, dir := fixtureJail(t)
	res, err := grep{}.Remote(context.Background(), testEnv(j),
		mustRaw(t, grepArgs{Path: filepath.Join(dir, "beta.log"), Pattern: "error", IgnoreCase: true}))
	if err != nil {
		t.Fatal(err)
	}
	// Matches ERROR (x2); the content has ERROR only in caps.
	if strings.Count(res.Output, "\n") != 2 {
		t.Errorf("case-insensitive grep expected 2 matches:\n%s", res.Output)
	}
}

func TestToolGrepDirRecursive(t *testing.T) {
	j, dir := fixtureJail(t)
	res, err := grep{}.Remote(context.Background(), testEnv(j),
		mustRaw(t, grepArgs{Path: dir, Pattern: "ERROR"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "nested.txt") {
		t.Errorf("recursive grep should reach nested file:\n%s", res.Output)
	}
}

func TestToolGrepCap(t *testing.T) {
	j, dir := fixtureJail(t)
	res, err := grep{}.Remote(context.Background(), testEnv(j),
		mustRaw(t, grepArgs{Path: dir, Pattern: "ERROR", MaxMatches: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(res.Output, "\n") != 1 || !res.Truncated {
		t.Errorf("capped grep = %d lines trunc=%v, want 1 true:\n%s", strings.Count(res.Output, "\n"), res.Truncated, res.Output)
	}
}

func TestToolGrepMaxMatchesHardCap(t *testing.T) {
	j, dir := fixtureJail(t)
	// A match count far above the hard cap must be clamped, not honoured verbatim.
	res, err := grep{}.Remote(context.Background(), testEnv(j),
		mustRaw(t, grepArgs{Path: dir, Pattern: "ERROR", MaxMatches: 1_000_000_000}))
	if err != nil {
		t.Fatal(err)
	}
	// The fixture has fewer than grepMaxMatches matches, so all are returned untruncated;
	// the clamp is exercised by the request not producing an error or unbounded work.
	if strings.Count(res.Output, "\n") == 0 {
		t.Errorf("expected matches with an over-large --max-matches:\n%s", res.Output)
	}
}

func TestToolGrepIncrementalOutputCap(t *testing.T) {
	dir := t.TempDir()
	// One very long matching line larger than a tiny output cap.
	long := strings.Repeat("x", 4096) + " ERROR\n"
	var content strings.Builder
	for i := 0; i < 50; i++ {
		content.WriteString(long)
	}
	p := filepath.Join(dir, "big.log")
	if err := os.WriteFile(p, []byte(content.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	j, err := NewJailSet([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	env := Env{Jail: j, Limits: Limits{MaxOutput: 1024, Timeout: 10 * time.Second}}
	res, err := grep{}.Remote(context.Background(), env, mustRaw(t, grepArgs{Path: p, Pattern: "ERROR"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Error("expected truncation when matches exceed the output cap")
	}
	if len(res.Output) > 1024 {
		t.Errorf("output %d bytes exceeds MaxOutput cap of 1024", len(res.Output))
	}
}

func TestToolGrepBadPattern(t *testing.T) {
	j, dir := fixtureJail(t)
	_, err := grep{}.Remote(context.Background(), testEnv(j),
		mustRaw(t, grepArgs{Path: dir, Pattern: "("}))
	if err == nil {
		t.Error("invalid regexp should error")
	}
}
