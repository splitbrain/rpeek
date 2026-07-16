package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testEnv returns an Env with the given jail and generous limits suitable for tests.
func testEnv(j *JailSet) Env {
	return Env{Jail: j, Limits: Limits{MaxOutput: 1 << 20, Timeout: 10 * time.Second}}
}

// mustRaw marshals v to the raw JSON a tool's Run expects.
func mustRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fixtureJail builds a temp directory with sample content and returns the jail rooted
// at it plus the directory path.
func fixtureJail(t *testing.T) (*JailSet, string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"alpha.txt":      "one\ntwo\nthree\n",
		"beta.log":       "INFO start\nERROR boom\ninfo again\nERROR again\n",
		"sub/nested.txt": "nested ERROR here\n",
		".hidden":        "secret\n",
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	j, err := NewJailSet([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	return j, dir
}
