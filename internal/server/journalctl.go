package server

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// runJournalctl executes journalctl with a fixed argument vector under the supplied
// context and returns its captured stdout. It is the only place the server execs an
// external program; the argument vector is always passed as discrete arguments, never
// interpolated into a shell string. This is the template any future exec-based tool
// must follow: fixed argv, validated inputs, context timeout, never a shell.
func runJournalctl(ctx context.Context, path string, argv []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, argv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("journalctl: %s", msg)
		}
		return nil, fmt.Errorf("journalctl: %w", err)
	}
	return stdout.Bytes(), nil
}
