package tools

import (
	"context"
	"strings"
	"testing"
)

func TestToolDisk(t *testing.T) {
	res, err := disk{}.Run(context.Background(), testEnv(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Output, "Filesystem") {
		t.Errorf("disk missing header:\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "/") {
		t.Errorf("disk should list at least the root filesystem:\n%s", res.Output)
	}
}
