package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolGrepFile(t *testing.T) {
	j, dir := fixtureJail(t)
	res, err := grep{}.Run(context.Background(), testEnv(j),
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
	res, err := grep{}.Run(context.Background(), testEnv(j),
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
	res, err := grep{}.Run(context.Background(), testEnv(j),
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
	res, err := grep{}.Run(context.Background(), testEnv(j),
		mustRaw(t, grepArgs{Path: dir, Pattern: "ERROR", MaxMatches: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(res.Output, "\n") != 1 || !res.Truncated {
		t.Errorf("capped grep = %d lines trunc=%v, want 1 true:\n%s", strings.Count(res.Output, "\n"), res.Truncated, res.Output)
	}
}

func TestToolGrepBadPattern(t *testing.T) {
	j, dir := fixtureJail(t)
	_, err := grep{}.Run(context.Background(), testEnv(j),
		mustRaw(t, grepArgs{Path: dir, Pattern: "("}))
	if err == nil {
		t.Error("invalid regexp should error")
	}
}
