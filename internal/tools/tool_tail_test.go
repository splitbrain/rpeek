package tools

import (
	"context"
	"path/filepath"
	"testing"
)

func TestToolTail(t *testing.T) {
	j, dir := fixtureJail(t)
	res, err := tail{}.Run(context.Background(), testEnv(j),
		mustRaw(t, tailArgs{Path: filepath.Join(dir, "beta.log"), Lines: 2}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "info again\nERROR again\n" {
		t.Errorf("tail = %q", res.Output)
	}
}

func TestToolTailMoreThanFile(t *testing.T) {
	j, dir := fixtureJail(t)
	res, err := tail{}.Run(context.Background(), testEnv(j),
		mustRaw(t, tailArgs{Path: filepath.Join(dir, "alpha.txt"), Lines: 100}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Output != "one\ntwo\nthree\n" {
		t.Errorf("tail of whole file = %q", res.Output)
	}
}
