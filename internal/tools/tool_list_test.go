package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolList(t *testing.T) {
	j, dir := fixtureJail(t)
	ctx := context.Background()
	env := testEnv(j)

	res, err := list{}.Run(ctx, env, mustRaw(t, listArgs{Path: dir}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "alpha.txt") || !strings.Contains(res.Output, "beta.log") {
		t.Errorf("list missing entries:\n%s", res.Output)
	}
	if strings.Contains(res.Output, ".hidden") {
		t.Errorf("list should skip dotfiles by default:\n%s", res.Output)
	}

	resAll, err := list{}.Run(ctx, env, mustRaw(t, listArgs{Path: dir, All: true}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resAll.Output, ".hidden") {
		t.Errorf("list --all should include dotfiles:\n%s", resAll.Output)
	}
}

func TestToolListRejectsFile(t *testing.T) {
	j, dir := fixtureJail(t)
	_, err := list{}.Run(context.Background(), testEnv(j), mustRaw(t, listArgs{Path: filepath.Join(dir, "alpha.txt")}))
	if err == nil {
		t.Error("listing a file should error")
	}
}
