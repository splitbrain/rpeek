package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotInJail indicates a resolved path did not fall within any configured jail root.
var ErrNotInJail = errors.New("path is not within any allowed root")

// JailSet is an immutable set of filesystem roots that file-addressing tools may read
// within. Each root is stored as an absolute, cleaned, symlink-resolved path.
type JailSet struct {
	// roots holds the resolved absolute root directories.
	roots []string
}

// NewJailSet resolves every supplied path (cleaning and following symlinks) and
// returns a JailSet. Each path must exist and be a directory. It returns an error if
// the list is empty or any path is missing, not a directory, or cannot be resolved.
func NewJailSet(paths []string) (*JailSet, error) {
	if len(paths) == 0 {
		return nil, errors.New("jail set requires at least one root")
	}
	roots := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("resolve root %q: %w", p, err)
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, fmt.Errorf("resolve root %q: %w", p, err)
		}
		info, err := os.Stat(real)
		if err != nil {
			return nil, fmt.Errorf("resolve root %q: %w", p, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("jail root %q is not a directory", p)
		}
		roots = append(roots, real)
	}
	return &JailSet{roots: roots}, nil
}

// Roots returns a copy of the resolved jail root directories, for display purposes.
func (j *JailSet) Roots() []string {
	out := make([]string, len(j.roots))
	copy(out, j.roots)
	return out
}

// Resolve maps a client-supplied path to a real absolute path guaranteed to be inside
// one of the roots. A relative path is resolved against the server's current working
// directory; an absolute path is taken as-is. Symlinks are resolved before the
// membership check, so a symlink inside a jail that points outside every root is
// rejected. It returns ErrNotInJail if the resolved path escapes every root, or a
// "no such file or directory" error if the path is within a root but does not exist.
func (j *JailSet) Resolve(p string) (string, error) {
	if strings.IndexByte(p, 0) >= 0 {
		return "", errors.New("path contains NUL byte")
	}
	if p == "" {
		return "", errors.New("empty path")
	}

	abs := p
	if !filepath.IsAbs(abs) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		abs = filepath.Join(cwd, abs)
	}
	abs = filepath.Clean(abs)

	resolved, exists, err := resolveExisting(abs)
	if err != nil {
		return "", err
	}
	if !j.contains(resolved) {
		return "", ErrNotInJail
	}
	if !exists {
		return "", fmt.Errorf("%s: no such file or directory", p)
	}
	return resolved, nil
}

// ResolveFile resolves p like Resolve and additionally requires the target to be a
// regular file, rejecting directories, devices, FIFOs, and sockets.
func (j *JailSet) ResolveFile(p string) (string, error) {
	real, err := j.Resolve(p)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s: not a regular file", p)
	}
	return real, nil
}

// contains reports whether real equals one of the roots or lies beneath one, using a
// path-separator-aware prefix check so "/rootfoo" is not treated as inside "/root".
func (j *JailSet) contains(real string) bool {
	for _, root := range j.roots {
		if real == root {
			return true
		}
		if strings.HasPrefix(real, root+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// resolveExisting resolves symlinks along the longest existing prefix of p and
// re-appends the trailing components that do not exist. Because the existing prefix is
// symlink-resolved, the returned path is safe to membership-check even when the full
// path is missing. It reports whether the full path exists.
func resolveExisting(p string) (resolved string, exists bool, err error) {
	resolved, err = filepath.EvalSymlinks(p)
	if err == nil {
		return resolved, true, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}

	dir := p
	var missing []string
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without finding an existing ancestor.
			return filepath.Clean(p), false, nil
		}
		missing = append([]string{filepath.Base(dir)}, missing...)
		dir = parent
		r, e := filepath.EvalSymlinks(dir)
		if e == nil {
			return filepath.Join(append([]string{r}, missing...)...), false, nil
		}
		if !errors.Is(e, fs.ErrNotExist) {
			return "", false, e
		}
	}
}
