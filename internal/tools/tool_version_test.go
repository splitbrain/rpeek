package tools

import (
	"context"
	"strings"
	"testing"

	"rpeek/internal/version"
)

func TestVersionTool(t *testing.T) {
	// versionTool implements both halves so the client can answer without a server and
	// the server can answer over the wire.
	var lt LocalTool = versionTool{}
	var rt RemoteTool = versionTool{}

	local, err := lt.Local(context.Background(), testEnv(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(local.Output); got != version.Version {
		t.Errorf("Local() = %q, want %q", got, version.Version)
	}
	if !strings.HasSuffix(local.Output, "\n") {
		t.Errorf("Local() output should end with a newline: %q", local.Output)
	}

	remote, err := rt.Remote(context.Background(), testEnv(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(remote.Output); got != version.Version {
		t.Errorf("Remote() = %q, want %q", got, version.Version)
	}
}
