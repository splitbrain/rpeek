package tools

import (
	"fmt"
	"unicode/utf8"
)

// scanLineCap is the maximum line length the line scanners accept before erroring. It
// is shared by the tools that scan files line by line.
const scanLineCap = 1 << 20

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

// singlePath requires exactly one positional argument and returns it.
func singlePath(tool string, pos []string) (string, error) {
	switch len(pos) {
	case 0:
		return "", fmt.Errorf("%s requires a path", tool)
	case 1:
		return pos[0], nil
	default:
		return "", fmt.Errorf("%s takes a single path, got %d arguments", tool, len(pos))
	}
}

// noPositionals requires that no positional arguments were given.
func noPositionals(tool string, pos []string) error {
	if len(pos) > 0 {
		return fmt.Errorf("%s takes no positional arguments", tool)
	}
	return nil
}
