package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mustEvalSymlinks resolves a path for comparison, matching how the jail stores roots.
func mustEvalSymlinks(t *testing.T, p string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", p, err)
	}
	return real
}

func TestNewJailSet(t *testing.T) {
	dir := t.TempDir()

	if _, err := NewJailSet(nil); err == nil {
		t.Error("empty jail set should be rejected")
	}

	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewJailSet([]string{file}); err == nil {
		t.Error("a file root should be rejected as not a directory")
	}

	if _, err := NewJailSet([]string{filepath.Join(dir, "nope")}); err == nil {
		t.Error("a nonexistent root should be rejected")
	}

	j, err := NewJailSet([]string{dir})
	if err != nil {
		t.Fatalf("valid root rejected: %v", err)
	}
	if got := j.Roots(); len(got) != 1 || got[0] != mustEvalSymlinks(t, dir) {
		t.Errorf("Roots() = %v, want [%s]", got, mustEvalSymlinks(t, dir))
	}
}

func TestResolveRootItself(t *testing.T) {
	dir := t.TempDir()
	j, err := NewJailSet([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	got, err := j.Resolve(dir)
	if err != nil {
		t.Fatalf("resolving the root itself failed: %v", err)
	}
	if got != mustEvalSymlinks(t, dir) {
		t.Errorf("Resolve(root) = %q, want %q", got, mustEvalSymlinks(t, dir))
	}
}

func TestResolveFileInsideJail(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "sub", "f.txt")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	j, _ := NewJailSet([]string{dir})
	got, err := j.Resolve(file)
	if err != nil {
		t.Fatalf("file inside jail rejected: %v", err)
	}
	if got != mustEvalSymlinks(t, file) {
		t.Errorf("Resolve = %q, want %q", got, mustEvalSymlinks(t, file))
	}
}

func TestResolveTraversalEscape(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// A secret file lives beside, not beneath, the root.
	secret := filepath.Join(parent, "secret")
	if err := os.WriteFile(secret, []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	j, _ := NewJailSet([]string{root})

	_, err := j.Resolve(filepath.Join(root, "..", "secret"))
	if !errors.Is(err, ErrNotInJail) {
		t.Errorf("traversal escape: got %v, want ErrNotInJail", err)
	}
}

func TestResolveSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(parent, "outside.txt")
	if err := os.WriteFile(outside, []byte("o"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlink living inside the root but pointing outside every root.
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	j, _ := NewJailSet([]string{root})

	_, err := j.Resolve(link)
	if !errors.Is(err, ErrNotInJail) {
		t.Errorf("symlink escape: got %v, want ErrNotInJail", err)
	}
}

func TestResolveSymlinkWithinJailAllowed(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real.txt")
	if err := os.WriteFile(target, []byte("r"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	j, _ := NewJailSet([]string{root})

	got, err := j.Resolve(link)
	if err != nil {
		t.Fatalf("symlink to a file within the jail should be allowed: %v", err)
	}
	if got != mustEvalSymlinks(t, target) {
		t.Errorf("Resolve(link) = %q, want %q", got, mustEvalSymlinks(t, target))
	}
}

func TestResolveMultiRoot(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	other := t.TempDir()

	fileA := filepath.Join(rootA, "a.txt")
	fileB := filepath.Join(rootB, "b.txt")
	fileC := filepath.Join(other, "c.txt")
	for _, f := range []string{fileA, fileB, fileC} {
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	j, _ := NewJailSet([]string{rootA, rootB})

	if _, err := j.Resolve(fileA); err != nil {
		t.Errorf("path under first root rejected: %v", err)
	}
	if _, err := j.Resolve(fileB); err != nil {
		t.Errorf("path under second root rejected: %v", err)
	}
	if _, err := j.Resolve(fileC); !errors.Is(err, ErrNotInJail) {
		t.Errorf("path under no root: got %v, want ErrNotInJail", err)
	}
}

func TestResolveRelativePath(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "rel.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	j, _ := NewJailSet([]string{root})

	// With cwd inside the root, a relative path resolves into the root.
	t.Chdir(root)
	if _, err := j.Resolve("rel.txt"); err != nil {
		t.Errorf("relative path in cwd (a root) rejected: %v", err)
	}
	// A relative traversal that climbs out of the root is rejected.
	if _, err := j.Resolve(filepath.Join("..", filepath.Base(root)+"-x")); !errors.Is(err, ErrNotInJail) {
		t.Errorf("relative escape: got %v, want ErrNotInJail", err)
	}
}

func TestResolveNonexistentInsideJail(t *testing.T) {
	root := t.TempDir()
	j, _ := NewJailSet([]string{root})

	_, err := j.Resolve(filepath.Join(root, "missing.txt"))
	if err == nil {
		t.Fatal("nonexistent path should error")
	}
	if errors.Is(err, ErrNotInJail) {
		t.Errorf("nonexistent path inside jail should be a not-found error, got ErrNotInJail")
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Errorf("expected a not-found error, got %v", err)
	}
}

func TestResolveNulByte(t *testing.T) {
	root := t.TempDir()
	j, _ := NewJailSet([]string{root})

	if _, err := j.Resolve(filepath.Join(root, "a\x00b")); err == nil {
		t.Error("path with NUL byte should be rejected")
	}
}

func TestResolveFileRejectsNonRegular(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	j, _ := NewJailSet([]string{root})

	if _, err := j.ResolveFile(sub); err == nil {
		t.Error("ResolveFile should reject a directory")
	}
}
