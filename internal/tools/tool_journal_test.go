package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestToolJournal(t *testing.T) {
	path, err := exec.LookPath("journalctl")
	if err != nil {
		t.Skip("journalctl not present")
	}
	env := Env{Limits: Limits{MaxOutput: 1 << 20, Timeout: 10 * time.Second}, Journalctl: path}
	res, jerr := journal{}.Remote(context.Background(), env, mustRaw(t, journalArgs{Lines: 5}))
	if jerr != nil {
		// Access may be denied in some environments; that is a clean error, not a crash.
		t.Logf("journal returned: %v", jerr)
		return
	}
	t.Logf("journal output %d bytes", len(res.Output))
}

func TestToolJournalInvalidUnit(t *testing.T) {
	env := Env{Limits: Limits{MaxOutput: 1 << 20, Timeout: 10 * time.Second}, Journalctl: "/bin/true"}
	_, err := journal{}.Remote(context.Background(), env, mustRaw(t, journalArgs{Unit: "nginx; rm -rf /"}))
	if err == nil {
		t.Error("invalid unit name should be rejected before exec")
	}
}

// TestToolJournalOutputCapped stands in a fake journalctl that emits far more than the
// output cap and exits, verifying that Remote bounds the returned output and flags it as
// truncated rather than buffering the whole stream.
func TestToolJournalOutputCapped(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not present")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "fakejournal")
	body := "#!/bin/sh\ni=0\nwhile [ $i -lt 2000 ]; do printf '%0100d\\n' 0; i=$((i+1)); done\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	const maxOut = 4096
	env := Env{Limits: Limits{MaxOutput: maxOut, Timeout: 10 * time.Second}, Journalctl: script}
	res, err := journal{}.Remote(context.Background(), env, mustRaw(t, journalArgs{Lines: 5}))
	if err != nil {
		t.Fatalf("journal: %v", err)
	}
	if len(res.Output) > maxOut {
		t.Errorf("output %d bytes exceeds MaxOutput cap of %d", len(res.Output), maxOut)
	}
	if !res.Truncated {
		t.Error("expected Truncated to be set for over-cap output")
	}
}

func TestToolJournalUnavailable(t *testing.T) {
	env := Env{Limits: Limits{MaxOutput: 1 << 20, Timeout: 10 * time.Second}, Journalctl: ""}
	_, err := journal{}.Remote(context.Background(), env, mustRaw(t, journalArgs{}))
	if err == nil {
		t.Error("missing journalctl should return a clean error")
	}
}
