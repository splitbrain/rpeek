package tools

import (
	"context"
	"fmt"
	"os"
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

// TestToolPSRedactsOwnArgs checks that the server's own process is listed with just its
// program name and none of its arguments, so a --db DSN passed to serve cannot be read
// back through ps. The test process stands in for the server: the go test runner is
// invoked with arguments (e.g. -test.run), which must not leak into the row.
func TestToolPSRedactsOwnArgs(t *testing.T) {
	res, err := ps{}.Remote(context.Background(), testEnv(nil), nil)
	if err != nil {
		t.Fatal(err)
	}

	prefix := fmt.Sprintf("%d ", os.Getpid())
	var self string
	for _, line := range strings.Split(res.Output, "\n") {
		if strings.HasPrefix(line, prefix) {
			self = line
			break
		}
	}
	if self == "" {
		t.Fatalf("own process (pid %d) not found in ps output:\n%s", os.Getpid(), res.Output)
	}

	// The CMD is the tail of the row, so the line must end with the program name alone.
	if !strings.HasSuffix(self, os.Args[0]) {
		t.Errorf("own row does not end with the bare program %q:\n%s", os.Args[0], self)
	}
	// If the runner was invoked with arguments, none may survive into the row.
	if len(os.Args) > 1 && strings.Contains(self, os.Args[1]) {
		t.Errorf("own row leaked argument %q:\n%s", os.Args[1], self)
	}
}
