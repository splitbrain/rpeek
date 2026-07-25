package tools

import (
	"bytes"
	"fmt"
	"unicode/utf8"
)

// scanLineCap is the maximum line length the line scanners accept before erroring. It
// is shared by the tools that scan files line by line.
const scanLineCap = 1 << 20

// capWriter is an io.Writer that retains at most cap bytes and silently discards the
// rest, recording whether any bytes were dropped. Write always reports the input as
// fully consumed, so a process streaming into it is never blocked once the cap is
// reached; this bounds memory when output is collected from a child process
// incrementally, rather than buffering the whole stream and trimming afterwards. A
// negative cap disables the limit.
type capWriter struct {
	buf     bytes.Buffer
	cap     int
	dropped bool
}

// Write stores up to the remaining capacity and discards any excess, reporting the
// whole input as written so the caller is never blocked.
func (w *capWriter) Write(p []byte) (int, error) {
	if w.cap >= 0 {
		if room := w.cap - w.buf.Len(); room < len(p) {
			if room > 0 {
				w.buf.Write(p[:room])
			}
			w.dropped = true
			return len(p), nil
		}
	}
	return w.buf.Write(p)
}

// capOutput truncates s to at most max bytes on a UTF-8 rune boundary and reports
// whether truncation occurred. A negative max disables the cap.
func capOutput(s string, max int) (string, bool) {
	if max < 0 || len(s) <= max {
		return s, false
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}

// clampLimit resolves a requested count against a default and a hard maximum. A
// non-positive request selects def; the result is then capped at max.
func clampLimit(requested, def, max int) int {
	if requested <= 0 {
		requested = def
	}
	if requested > max {
		requested = max
	}
	return requested
}

// oneArg requires exactly one positional argument and returns it. what names the argument
// in error messages as a bare noun, e.g. "path" or "table name".
func oneArg(tool, what string, pos []string) (string, error) {
	switch len(pos) {
	case 0:
		return "", fmt.Errorf("%s requires a %s", tool, what)
	case 1:
		return pos[0], nil
	default:
		return "", fmt.Errorf("%s takes a single %s, got %d arguments", tool, what, len(pos))
	}
}

// singlePath requires exactly one positional path argument and returns it.
func singlePath(tool string, pos []string) (string, error) {
	return oneArg(tool, "path", pos)
}

// noPositionals requires that no positional arguments were given.
func noPositionals(tool string, pos []string) error {
	if len(pos) > 0 {
		return fmt.Errorf("%s takes no positional arguments", tool)
	}
	return nil
}
