package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/user"
	"sort"
	"strconv"
	"strings"
)

// ps returns a ps-style snapshot of running processes, read purely from /proc.
type ps struct{ readOnly }

func init() { register(ps{}) }

// Name returns the subcommand name.
func (ps) Name() string { return "ps" }

// Summary returns the one-line help description.
func (ps) Summary() string { return "process snapshot from /proc (PID PPID USER RSS CMD)" }

// Usage returns the argument synopsis.
func (ps) Usage() string { return "ps" }

// psArgs are the wire arguments for the ps tool. It takes none.
type psArgs struct{}

// psMaxProcs bounds how many processes ps returns.
const psMaxProcs = 5000

// NewFlags builds the ps flag set and its argument builder.
func (ps) NewFlags() (*flag.FlagSet, func([]string) (any, error)) {
	fs := flag.NewFlagSet("ps", flag.ContinueOnError)
	return fs, func(pos []string) (any, error) {
		if err := noPositionals("ps", pos); err != nil {
			return nil, err
		}
		return psArgs{}, nil
	}
}

// psRow is one process record collected from /proc.
type psRow struct {
	pid  int
	ppid int
	user string
	rss  int64 // resident set size in kB
	cmd  string
}

// Run returns a ps-style snapshot of running processes.
func (ps) Remote(ctx context.Context, env Env, raw json.RawMessage) (Result, error) {
	if _, err := decodeArgs[psArgs](raw); err != nil {
		return Result{}, err
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return Result{}, err
	}

	userCache := map[uint32]string{}
	lookupUser := func(uid uint32) string {
		if name, ok := userCache[uid]; ok {
			return name
		}
		name := strconv.FormatUint(uint64(uid), 10)
		if u, err := user.LookupId(name); err == nil {
			name = u.Username
		}
		userCache[uid] = name
		return name
	}

	var rows []psRow
	truncated := false
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if len(rows) >= psMaxProcs {
			truncated = true
			break
		}
		row, ok := readProc(pid, lookupUser)
		if !ok {
			continue // process exited between listing and reading
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, k int) bool { return rows[i].pid < rows[k].pid })

	var b strings.Builder
	fmt.Fprintf(&b, "%-8s %-8s %-12s %10s  %s\n", "PID", "PPID", "USER", "RSS(kB)", "CMD")
	for _, r := range rows {
		fmt.Fprintf(&b, "%-8d %-8d %-12s %10d  %s\n", r.pid, r.ppid, r.user, r.rss, r.cmd)
	}

	out, capTrunc := capOutput(b.String(), env.Limits.MaxOutput)
	return Result{Output: out, Truncated: truncated || capTrunc}, nil
}

// readProc reads one process's fields from /proc, returning false if the process has
// gone away or its stat line is unparseable.
func readProc(pid int, lookupUser func(uint32) string) (psRow, bool) {
	statData, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return psRow{}, false
	}
	s := string(statData)
	// The comm field is parenthesized and may contain spaces or parens; split on the
	// last ')' so the numeric fields after it parse cleanly.
	open := strings.IndexByte(s, '(')
	closeP := strings.LastIndexByte(s, ')')
	if open < 0 || closeP < 0 || closeP < open {
		return psRow{}, false
	}
	comm := s[open+1 : closeP]
	rest := strings.Fields(s[closeP+1:])
	// rest[0] is state, rest[1] is ppid.
	if len(rest) < 2 {
		return psRow{}, false
	}
	ppid, _ := strconv.Atoi(rest[1])

	row := psRow{pid: pid, ppid: ppid}

	if statusData, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(statusData))
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "Uid:"):
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if uid, err := strconv.ParseUint(fields[1], 10, 32); err == nil {
						row.user = lookupUser(uint32(uid))
					}
				}
			case strings.HasPrefix(line, "VmRSS:"):
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					row.rss, _ = strconv.ParseInt(fields[1], 10, 64)
				}
			}
		}
	}
	if row.user == "" {
		row.user = "?"
	}

	cmd := ""
	if cl, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
		cmd = strings.TrimRight(string(cl), "\x00")
		cmd = strings.ReplaceAll(cmd, "\x00", " ")
	}
	if cmd == "" {
		cmd = "[" + comm + "]" // kernel thread or zombie: fall back to comm
	}
	row.cmd = cmd

	return row, true
}
