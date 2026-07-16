package tools

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestToolHostname(t *testing.T) {
	res, err := hostname{}.Run(context.Background(), testEnv(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimRight(res.Output, "\n"); got != want {
		t.Errorf("hostname = %q, want %q", got, want)
	}
	if !strings.HasSuffix(res.Output, "\n") {
		t.Errorf("hostname output should end with a newline: %q", res.Output)
	}
}
