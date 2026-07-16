package tools

import (
	"context"
	"strings"
	"testing"

	"rpeek/internal/version"
)

func TestVersionTool(t *testing.T) {
	// versionTool must satisfy LocalTool so the client can answer without a server.
	var lt LocalTool = versionTool{}

	local, err := lt.Local()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(local.Output); got != version.Version {
		t.Errorf("Local() = %q, want %q", got, version.Version)
	}
	if !strings.HasSuffix(local.Output, "\n") {
		t.Errorf("Local() output should end with a newline: %q", local.Output)
	}

	run, err := versionTool{}.Run(context.Background(), testEnv(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(run.Output); got != version.Version {
		t.Errorf("Run() = %q, want %q", got, version.Version)
	}
}
