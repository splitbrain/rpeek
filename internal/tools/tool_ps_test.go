package tools

import (
	"context"
	"strings"
	"testing"
)

func TestToolPS(t *testing.T) {
	res, err := ps{}.Remote(context.Background(), testEnv(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Output, "PID") {
		t.Errorf("ps missing header:\n%s", res.Output[:min(80, len(res.Output))])
	}
	// This test process itself must appear.
	if strings.Count(res.Output, "\n") < 2 {
		t.Errorf("ps returned too few rows:\n%s", res.Output)
	}
}
