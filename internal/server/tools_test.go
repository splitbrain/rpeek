package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"diag/internal/protocol"
)

// testLimits returns generous limits suitable for tests.
func testLimits() Limits {
	return Limits{MaxOutput: 1 << 20, Timeout: 10 * time.Second}
}

// fixtureJail builds a temp directory with sample content and returns the jail rooted
// at it plus the directory path.
func fixtureJail(t *testing.T) (*JailSet, string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"alpha.txt":      "one\ntwo\nthree\n",
		"beta.log":       "INFO start\nERROR boom\ninfo again\nERROR again\n",
		"sub/nested.txt": "nested ERROR here\n",
		".hidden":        "secret\n",
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	j, err := NewJailSet([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	return j, dir
}

func TestToolList(t *testing.T) {
	j, dir := fixtureJail(t)
	ctx := context.Background()

	out, _, err := toolList(ctx, j, testLimits(), protocol.ListArgs{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "alpha.txt") || !strings.Contains(out, "beta.log") {
		t.Errorf("list missing entries:\n%s", out)
	}
	if strings.Contains(out, ".hidden") {
		t.Errorf("list should skip dotfiles by default:\n%s", out)
	}

	outAll, _, err := toolList(ctx, j, testLimits(), protocol.ListArgs{Path: dir, All: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(outAll, ".hidden") {
		t.Errorf("list --all should include dotfiles:\n%s", outAll)
	}
}

func TestToolListRejectsFile(t *testing.T) {
	j, dir := fixtureJail(t)
	_, _, err := toolList(context.Background(), j, testLimits(), protocol.ListArgs{Path: filepath.Join(dir, "alpha.txt")})
	if err == nil {
		t.Error("listing a file should error")
	}
}

func TestToolRead(t *testing.T) {
	j, dir := fixtureJail(t)
	ctx := context.Background()
	path := filepath.Join(dir, "alpha.txt")

	out, trunc, err := toolRead(ctx, j, testLimits(), protocol.ReadArgs{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if out != "one\ntwo\nthree\n" {
		t.Errorf("read = %q", out)
	}
	if trunc {
		t.Error("small file should not be truncated")
	}

	// Offset paging.
	out, _, err = toolRead(ctx, j, testLimits(), protocol.ReadArgs{Path: path, Offset: 4})
	if err != nil {
		t.Fatal(err)
	}
	if out != "two\nthree\n" {
		t.Errorf("read with offset = %q", out)
	}

	// Byte cap sets Truncated when more remains.
	out, trunc, err = toolRead(ctx, j, testLimits(), protocol.ReadArgs{Path: path, MaxBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	if out != "one" || !trunc {
		t.Errorf("capped read = %q trunc=%v, want \"one\" true", out, trunc)
	}
}

func TestToolReadRejectsDir(t *testing.T) {
	j, dir := fixtureJail(t)
	_, _, err := toolRead(context.Background(), j, testLimits(), protocol.ReadArgs{Path: dir})
	if err == nil {
		t.Error("reading a directory should error")
	}
}

func TestToolReadEscape(t *testing.T) {
	j, dir := fixtureJail(t)
	_, _, err := toolRead(context.Background(), j, testLimits(),
		protocol.ReadArgs{Path: filepath.Join(dir, "..", "escape")})
	if err == nil {
		t.Error("reading outside the jail should error")
	}
}

func TestToolGrepFile(t *testing.T) {
	j, dir := fixtureJail(t)
	out, _, err := toolGrep(context.Background(), j, testLimits(),
		protocol.GrepArgs{Path: filepath.Join(dir, "beta.log"), Pattern: "ERROR"})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Count(out, "\n")
	if lines != 2 {
		t.Errorf("expected 2 ERROR matches, got %d:\n%s", lines, out)
	}
	if !strings.Contains(out, "beta.log:2: ERROR boom") {
		t.Errorf("grep output missing expected line:\n%s", out)
	}
}

func TestToolGrepIgnoreCase(t *testing.T) {
	j, dir := fixtureJail(t)
	out, _, err := toolGrep(context.Background(), j, testLimits(),
		protocol.GrepArgs{Path: filepath.Join(dir, "beta.log"), Pattern: "error", IgnoreCase: true})
	if err != nil {
		t.Fatal(err)
	}
	// Matches ERROR (x2) and info-case "error"? content has ERROR x2 only in caps.
	if strings.Count(out, "\n") != 2 {
		t.Errorf("case-insensitive grep expected 2 matches:\n%s", out)
	}
}

func TestToolGrepDirRecursive(t *testing.T) {
	j, dir := fixtureJail(t)
	out, _, err := toolGrep(context.Background(), j, testLimits(),
		protocol.GrepArgs{Path: dir, Pattern: "ERROR"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "nested.txt") {
		t.Errorf("recursive grep should reach nested file:\n%s", out)
	}
}

func TestToolGrepCap(t *testing.T) {
	j, dir := fixtureJail(t)
	out, trunc, err := toolGrep(context.Background(), j, testLimits(),
		protocol.GrepArgs{Path: dir, Pattern: "ERROR", MaxMatches: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, "\n") != 1 || !trunc {
		t.Errorf("capped grep = %d lines trunc=%v, want 1 true:\n%s", strings.Count(out, "\n"), trunc, out)
	}
}

func TestToolGrepBadPattern(t *testing.T) {
	j, dir := fixtureJail(t)
	_, _, err := toolGrep(context.Background(), j, testLimits(),
		protocol.GrepArgs{Path: dir, Pattern: "("})
	if err == nil {
		t.Error("invalid regexp should error")
	}
}

func TestToolTail(t *testing.T) {
	j, dir := fixtureJail(t)
	out, _, err := toolTail(context.Background(), j, testLimits(),
		protocol.TailArgs{Path: filepath.Join(dir, "beta.log"), Lines: 2})
	if err != nil {
		t.Fatal(err)
	}
	if out != "info again\nERROR again\n" {
		t.Errorf("tail = %q", out)
	}
}

func TestToolTailMoreThanFile(t *testing.T) {
	j, dir := fixtureJail(t)
	out, _, err := toolTail(context.Background(), j, testLimits(),
		protocol.TailArgs{Path: filepath.Join(dir, "alpha.txt"), Lines: 100})
	if err != nil {
		t.Fatal(err)
	}
	if out != "one\ntwo\nthree\n" {
		t.Errorf("tail of whole file = %q", out)
	}
}

func TestToolStat(t *testing.T) {
	j, dir := fixtureJail(t)
	out, _, err := toolStat(context.Background(), j, testLimits(),
		protocol.StatArgs{Path: filepath.Join(dir, "alpha.txt")})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"name:", "size:", "mode:", "modtime:", "uid:", "gid:"} {
		if !strings.Contains(out, key) {
			t.Errorf("stat missing %q:\n%s", key, out)
		}
	}
	if !strings.Contains(out, "size:    14") {
		t.Errorf("stat wrong size (want 14):\n%s", out)
	}
}

func TestToolPS(t *testing.T) {
	out, _, err := toolPS(context.Background(), testLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "PID") {
		t.Errorf("ps missing header:\n%s", out[:min(80, len(out))])
	}
	// This test process itself must appear.
	if strings.Count(out, "\n") < 2 {
		t.Errorf("ps returned too few rows:\n%s", out)
	}
}

func TestToolDisk(t *testing.T) {
	out, _, err := toolDisk(context.Background(), testLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Filesystem") {
		t.Errorf("disk missing header:\n%s", out)
	}
	if !strings.Contains(out, "/") {
		t.Errorf("disk should list at least the root filesystem:\n%s", out)
	}
}

func TestToolJournal(t *testing.T) {
	path, err := exec.LookPath("journalctl")
	if err != nil {
		t.Skip("journalctl not present")
	}
	out, _, jerr := toolJournal(context.Background(), testLimits(), path,
		protocol.JournalArgs{Lines: 5})
	if jerr != nil {
		// Access may be denied in some environments; that is a clean error, not a crash.
		t.Logf("journal returned: %v", jerr)
		return
	}
	t.Logf("journal output %d bytes", len(out))
}

func TestToolJournalInvalidUnit(t *testing.T) {
	_, _, err := toolJournal(context.Background(), testLimits(), "/bin/true",
		protocol.JournalArgs{Unit: "nginx; rm -rf /"})
	if err == nil {
		t.Error("invalid unit name should be rejected before exec")
	}
}

func TestToolJournalUnavailable(t *testing.T) {
	_, _, err := toolJournal(context.Background(), testLimits(), "",
		protocol.JournalArgs{})
	if err == nil {
		t.Error("missing journalctl should return a clean error")
	}
}
