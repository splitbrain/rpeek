package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestToolList(t *testing.T) {
	j, dir := fixtureJail(t)
	ctx := context.Background()
	env := testEnv(j)

	res, err := list{}.Remote(ctx, env, mustRaw(t, listArgs{Path: dir}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "alpha.txt") || !strings.Contains(res.Output, "beta.log") {
		t.Errorf("list missing entries:\n%s", res.Output)
	}
	if strings.Contains(res.Output, ".hidden") {
		t.Errorf("list should skip dotfiles by default:\n%s", res.Output)
	}

	resAll, err := list{}.Remote(ctx, env, mustRaw(t, listArgs{Path: dir, All: true}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resAll.Output, ".hidden") {
		t.Errorf("list --all should include dotfiles:\n%s", resAll.Output)
	}
}

func TestToolListBoundsLargeDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "many")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	const n = maxListEntries + 5
	for i := 0; i < n; i++ {
		p := filepath.Join(sub, fmt.Sprintf("f%06d.txt", i))
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	j, err := NewJailSet([]string{dir})
	if err != nil {
		t.Fatal(err)
	}

	res, err := list{}.Remote(context.Background(), testEnv(j), mustRaw(t, listArgs{Path: sub}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated {
		t.Error("expected Truncated for a directory exceeding the entry cap")
	}

	lines := strings.Split(strings.TrimRight(res.Output, "\n"), "\n")
	if len(lines) != maxListEntries {
		t.Errorf("expected %d entries, got %d", maxListEntries, len(lines))
	}

	names := make([]string, len(lines))
	for i, ln := range lines {
		fields := strings.Fields(ln)
		names[i] = fields[len(fields)-1]
	}
	if !sort.StringsAreSorted(names) {
		t.Error("list output is not sorted by name")
	}

	// The names are zero-padded and sequential, so the alphabetical prefix is
	// the numeric prefix: the cap must keep f000000..f009999 and drop the tail,
	// not an arbitrary filesystem-order subset.
	if got, want := names[0], "f000000.txt"; got != want {
		t.Errorf("first entry = %q, want %q", got, want)
	}
	if got, want := names[len(names)-1], fmt.Sprintf("f%06d.txt", maxListEntries-1); got != want {
		t.Errorf("last entry = %q, want %q", got, want)
	}
}

func TestToolListRejectsFile(t *testing.T) {
	j, dir := fixtureJail(t)
	_, err := list{}.Remote(context.Background(), testEnv(j), mustRaw(t, listArgs{Path: filepath.Join(dir, "alpha.txt")}))
	if err == nil {
		t.Error("listing a file should error")
	}
}
