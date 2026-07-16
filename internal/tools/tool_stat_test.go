package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolStat(t *testing.T) {
	j, dir := fixtureJail(t)
	res, err := stat{}.Remote(context.Background(), testEnv(j),
		mustRaw(t, statArgs{Path: filepath.Join(dir, "alpha.txt")}))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"name:", "size:", "mode:", "modtime:", "uid:", "gid:"} {
		if !strings.Contains(res.Output, key) {
			t.Errorf("stat missing %q:\n%s", key, res.Output)
		}
	}
	if !strings.Contains(res.Output, "size:    14") {
		t.Errorf("stat wrong size (want 14):\n%s", res.Output)
	}
}
