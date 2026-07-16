package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// disk returns a df-style table of filesystem usage, read purely from /proc/mounts and
// syscall.Statfs.
type disk struct{ readOnly }

func init() { register(disk{}) }

// Name returns the subcommand name.
func (disk) Name() string { return "disk" }

// Summary returns the one-line help description.
func (disk) Summary() string { return "filesystem usage (df style)" }

// Usage returns the argument synopsis.
func (disk) Usage() string { return "disk" }

// diskArgs are the wire arguments for the disk tool. It takes none.
type diskArgs struct{}

// pseudoFS lists filesystem types disk skips, since they do not represent real storage
// usage.
var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true,
	"devtmpfs": true, "devpts": true, "mqueue": true, "hugetlbfs": true,
	"debugfs": true, "tracefs": true, "securityfs": true, "pstore": true,
	"bpf": true, "configfs": true, "fusectl": true, "autofs": true,
	"binfmt_misc": true, "sysfs2": true, "ramfs": true, "nsfs": true,
	"overlay": false, // overlay is real enough to report
}

// NewFlags builds the disk flag set and its argument builder.
func (disk) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("disk", flag.ContinueOnError)
	return fs, func(pos []string) (any, error) {
		if err := noPositionals("disk", pos); err != nil {
			return nil, err
		}
		return diskArgs{}, nil
	}
}

// Run returns a df-style table of filesystem usage.
func (disk) Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	if _, err := decodeArgs[diskArgs](raw); err != nil {
		return Result{}, err
	}

	mounts, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return Result{}, err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%-24s %8s %8s %8s %5s  %s\n",
		"Filesystem", "Size", "Used", "Avail", "Use%", "Mounted on")

	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(mounts))
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		device := unescapeMount(fields[0])
		mountpoint := unescapeMount(fields[1])
		fstype := fields[2]
		if skip, known := pseudoFS[fstype]; known && skip {
			continue
		}
		if seen[mountpoint] {
			continue
		}
		seen[mountpoint] = true

		var st syscall.Statfs_t
		if err := syscall.Statfs(mountpoint, &st); err != nil {
			continue
		}
		bsize := uint64(st.Bsize)
		total := st.Blocks * bsize
		if total == 0 {
			continue
		}
		avail := st.Bavail * bsize
		used := (st.Blocks - st.Bfree) * bsize
		usePct := 0
		if used+avail > 0 {
			usePct = int((used*100 + (used+avail)/2) / (used + avail))
		}
		fmt.Fprintf(&b, "%-24s %8s %8s %8s %4d%%  %s\n",
			device, humanBytes(total), humanBytes(used), humanBytes(avail), usePct, mountpoint)
	}

	out, capTrunc := capOutput(b.String(), env.Limits.MaxOutput)
	return Result{Output: out, Truncated: capTrunc}, nil
}

// unescapeMount decodes the octal escapes (\040, \011, \012, \134) that /proc/mounts
// uses for spaces, tabs, newlines, and backslashes in field values.
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// humanBytes formats a byte count in a compact, human-readable form (e.g. "20G").
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGTPE"[exp])
}
