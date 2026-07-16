package tools

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestToolJournal(t *testing.T) {
	path, err := exec.LookPath("journalctl")
	if err != nil {
		t.Skip("journalctl not present")
	}
	env := Env{Limits: Limits{MaxOutput: 1 << 20, Timeout: 10 * time.Second}, Journalctl: path}
	res, jerr := journal{}.Run(context.Background(), env, mustRaw(t, journalArgs{Lines: 5}))
	if jerr != nil {
		// Access may be denied in some environments; that is a clean error, not a crash.
		t.Logf("journal returned: %v", jerr)
		return
	}
	t.Logf("journal output %d bytes", len(res.Output))
}

func TestToolJournalInvalidUnit(t *testing.T) {
	env := Env{Limits: Limits{MaxOutput: 1 << 20, Timeout: 10 * time.Second}, Journalctl: "/bin/true"}
	_, err := journal{}.Run(context.Background(), env, mustRaw(t, journalArgs{Unit: "nginx; rm -rf /"}))
	if err == nil {
		t.Error("invalid unit name should be rejected before exec")
	}
}

func TestToolJournalUnavailable(t *testing.T) {
	env := Env{Limits: Limits{MaxOutput: 1 << 20, Timeout: 10 * time.Second}, Journalctl: ""}
	_, err := journal{}.Run(context.Background(), env, mustRaw(t, journalArgs{}))
	if err == nil {
		t.Error("missing journalctl should return a clean error")
	}
}
