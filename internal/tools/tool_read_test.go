package tools

import (
	"context"
	"path/filepath"
	"testing"
)

func TestToolRead(t *testing.T) {
	j, dir := fixtureJail(t)
	ctx := context.Background()
	env := testEnv(j)
	path := filepath.Join(dir, "alpha.txt")

	res, err := read{}.Remote(ctx, env, mustRaw(t, readArgs{Path: path}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "one\ntwo\nthree\n" {
		t.Errorf("read = %q", res.Output)
	}
	if res.Truncated {
		t.Error("small file should not be truncated")
	}

	// Offset paging.
	res, err = read{}.Remote(ctx, env, mustRaw(t, readArgs{Path: path, Offset: 4}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "two\nthree\n" {
		t.Errorf("read with offset = %q", res.Output)
	}

	// Byte cap sets Truncated when more remains.
	res, err = read{}.Remote(ctx, env, mustRaw(t, readArgs{Path: path, MaxBytes: 3}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "one" || !res.Truncated {
		t.Errorf("capped read = %q trunc=%v, want \"one\" true", res.Output, res.Truncated)
	}
}

func TestToolReadRejectsDir(t *testing.T) {
	j, dir := fixtureJail(t)
	_, err := read{}.Remote(context.Background(), testEnv(j), mustRaw(t, readArgs{Path: dir}))
	if err == nil {
		t.Error("reading a directory should error")
	}
}

func TestToolReadEscape(t *testing.T) {
	j, dir := fixtureJail(t)
	_, err := read{}.Remote(context.Background(), testEnv(j),
		mustRaw(t, readArgs{Path: filepath.Join(dir, "..", "escape")}))
	if err == nil {
		t.Error("reading outside the jail should error")
	}
}
