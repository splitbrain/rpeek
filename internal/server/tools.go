package server

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"diag/internal/protocol"
)

// Limits bounds the work and output size of every tool.
type Limits struct {
	// MaxOutput is the maximum number of bytes a tool may return before its output is
	// truncated and the Truncated flag is set.
	MaxOutput int

	// Timeout is the per-tool wall-clock deadline, enforced through the context.
	Timeout time.Duration
}

// Tool-specific caps that are fixed rather than operator-configurable.
const (
	// maxListEntries bounds how many directory entries "list" returns.
	maxListEntries = 10000
	// readDefaultBytes is the default byte cap for "read" when the client asks for none.
	readDefaultBytes = 65536
	// readHardBytes is the hard byte cap for "read" regardless of the client request.
	readHardBytes = 1 << 20
	// grepDefaultMatches is the default cap on matching lines for "grep".
	grepDefaultMatches = 1000
	// scanLineCap is the maximum line length for line scanners.
	scanLineCap = 1 << 20
	// tailDefaultLines is the default number of trailing lines for "tail".
	tailDefaultLines = 100
	// tailMaxLines is the hard cap on trailing lines for "tail".
	tailMaxLines = 10000
	// tailMaxScan bounds how many bytes "tail" reads from the end of a file, which
	// also protects against zero-length-but-streaming files under /proc.
	tailMaxScan = 8 << 20
	// psMaxProcs bounds how many processes "ps" returns.
	psMaxProcs = 5000
	// journalDefaultLines is the default line count for "journal".
	journalDefaultLines = 100
	// journalMaxLines is the hard cap on lines for "journal".
	journalMaxLines = 10000
)

// unitPattern validates a systemd unit name before it is passed to journalctl.
var unitPattern = regexp.MustCompile(`^[a-zA-Z0-9@._-]+$`)

// capOutput truncates s to at most max bytes on a UTF-8 rune boundary and reports
// whether truncation occurred.
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

// toolList lists a directory in an ls -l style, one entry per line.
func toolList(ctx context.Context, j *JailSet, lim Limits, args protocol.ListArgs) (string, bool, error) {
	dir, err := j.Resolve(args.Path)
	if err != nil {
		return "", false, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", false, err
	}
	if !info.IsDir() {
		return "", false, fmt.Errorf("%s: not a directory", args.Path)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false, err
	}

	var b strings.Builder
	truncated := false
	count := 0
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		name := e.Name()
		if !args.All && strings.HasPrefix(name, ".") {
			continue
		}
		if count >= maxListEntries {
			truncated = true
			break
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%s %10d %s %s\n",
			fi.Mode().String(),
			fi.Size(),
			fi.ModTime().Format(time.RFC3339),
			name,
		)
		count++
	}

	out, capTrunc := capOutput(b.String(), lim.MaxOutput)
	return out, truncated || capTrunc, nil
}

// toolRead returns up to a capped number of bytes of a regular file, optionally from
// a byte offset. The byte cap is enforced with an io.LimitReader so that streaming
// pseudo-files cannot produce unbounded output.
func toolRead(ctx context.Context, j *JailSet, lim Limits, args protocol.ReadArgs) (string, bool, error) {
	path, err := j.ResolveFile(args.Path)
	if err != nil {
		return "", false, err
	}
	if args.Offset < 0 {
		return "", false, fmt.Errorf("offset must not be negative")
	}

	maxBytes := args.MaxBytes
	if maxBytes <= 0 {
		maxBytes = readDefaultBytes
	}
	if maxBytes > readHardBytes {
		maxBytes = readHardBytes
	}
	if maxBytes > lim.MaxOutput {
		maxBytes = lim.MaxOutput
	}

	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	if args.Offset > 0 {
		if _, err := f.Seek(int64(args.Offset), io.SeekStart); err != nil {
			return "", false, err
		}
	}

	data, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)))
	if err != nil {
		return "", false, err
	}

	// A successful single-byte read past the cap means more data remains.
	var probe [1]byte
	n, _ := f.Read(probe[:])
	return string(data), n > 0, nil
}

// toolGrep searches a file, or a directory tree, for lines matching an RE2 pattern and
// returns them in grep -n style: "path:line: text".
func toolGrep(ctx context.Context, j *JailSet, lim Limits, args protocol.GrepArgs) (string, bool, error) {
	if args.Pattern == "" {
		return "", false, fmt.Errorf("pattern must not be empty")
	}
	pattern := args.Pattern
	if args.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", false, fmt.Errorf("invalid pattern: %w", err)
	}

	maxMatches := args.MaxMatches
	if maxMatches <= 0 {
		maxMatches = grepDefaultMatches
	}

	target, err := j.Resolve(args.Path)
	if err != nil {
		return "", false, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", false, err
	}

	var b strings.Builder
	matches := 0
	truncated := false

	// errStopScan is a sentinel that unwinds a directory walk once the match cap or
	// the deadline is reached.
	errStopScan := fmt.Errorf("stop scan")

	scanFile := func(path string) error {
		f, err := os.Open(path)
		if err != nil {
			return nil // skip unreadable files
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), scanLineCap)
		line := 0
		for sc.Scan() {
			if err := ctx.Err(); err != nil {
				return err
			}
			line++
			text := sc.Text()
			if re.MatchString(text) {
				if matches >= maxMatches {
					truncated = true
					return errStopScan
				}
				fmt.Fprintf(&b, "%s:%d: %s\n", path, line, text)
				matches++
			}
		}
		return nil
	}

	if info.IsDir() {
		err = filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable subtrees
			}
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return nil // skip symlinks, devices, sockets, FIFOs
			}
			return scanFile(path)
		})
	} else {
		if !info.Mode().IsRegular() {
			return "", false, fmt.Errorf("%s: not a regular file", args.Path)
		}
		err = scanFile(target)
	}
	if err != nil && err != errStopScan {
		return "", false, err
	}

	out, capTrunc := capOutput(b.String(), lim.MaxOutput)
	return out, truncated || capTrunc, nil
}

// toolTail returns the last N lines of a regular file. It reads only the trailing
// window of the file so that huge files do not force unbounded work.
func toolTail(ctx context.Context, j *JailSet, lim Limits, args protocol.TailArgs) (string, bool, error) {
	path, err := j.ResolveFile(args.Path)
	if err != nil {
		return "", false, err
	}

	lines := args.Lines
	if lines <= 0 {
		lines = tailDefaultLines
	}
	if lines > tailMaxLines {
		lines = tailMaxLines
	}

	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	// Read at most the last tailMaxScan bytes. For a file larger than that we skip the
	// first (likely partial) line to avoid emitting a fragment.
	var start int64
	if fi, err := f.Stat(); err == nil && fi.Size() > tailMaxScan {
		start = fi.Size() - tailMaxScan
	}
	dropFirst := start > 0
	if start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return "", false, err
		}
	}

	sc := bufio.NewScanner(io.LimitReader(f, tailMaxScan))
	sc.Buffer(make([]byte, 0, 64*1024), scanLineCap)

	ring := make([]string, lines)
	count := 0
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if dropFirst {
			dropFirst = false
			continue
		}
		ring[count%lines] = sc.Text()
		count++
	}
	if err := sc.Err(); err != nil {
		return "", false, err
	}

	n := lines
	if count < lines {
		n = count
	}
	first := count - n
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(ring[(first+i)%lines])
		b.WriteByte('\n')
	}

	out, capTrunc := capOutput(b.String(), lim.MaxOutput)
	// start > 0 means older lines were skipped, which for tail is expected; only the
	// output-size cap constitutes truncation of the requested lines.
	return out, capTrunc, nil
}

// toolStat reports metadata for a path as key: value lines. Because the jail resolves
// symlinks, the reported metadata is that of the resolved target.
func toolStat(ctx context.Context, j *JailSet, lim Limits, args protocol.StatArgs) (string, bool, error) {
	real, err := j.Resolve(args.Path)
	if err != nil {
		return "", false, err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", false, err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "name:    %s\n", filepath.Base(real))
	fmt.Fprintf(&b, "path:    %s\n", real)
	fmt.Fprintf(&b, "size:    %d\n", info.Size())
	fmt.Fprintf(&b, "mode:    %s\n", info.Mode())
	fmt.Fprintf(&b, "modtime: %s\n", info.ModTime().Format(time.RFC3339))
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		fmt.Fprintf(&b, "uid:     %d\n", st.Uid)
		fmt.Fprintf(&b, "gid:     %d\n", st.Gid)
	}

	out, capTrunc := capOutput(b.String(), lim.MaxOutput)
	return out, capTrunc, nil
}

// psRow is one process record collected from /proc.
type psRow struct {
	pid  int
	ppid int
	user string
	rss  int64 // resident set size in kB
	cmd  string
}

// toolPS returns a ps-style snapshot of running processes, read purely from /proc.
func toolPS(ctx context.Context, lim Limits) (string, bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return "", false, err
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
			return "", false, err
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

	out, capTrunc := capOutput(b.String(), lim.MaxOutput)
	return out, truncated || capTrunc, nil
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

// pseudoFS lists filesystem types "disk" skips, since they do not represent real
// storage usage.
var pseudoFS = map[string]bool{
	"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true,
	"devtmpfs": true, "devpts": true, "mqueue": true, "hugetlbfs": true,
	"debugfs": true, "tracefs": true, "securityfs": true, "pstore": true,
	"bpf": true, "configfs": true, "fusectl": true, "autofs": true,
	"binfmt_misc": true, "sysfs2": true, "ramfs": true, "nsfs": true,
	"overlay": false, // overlay is real enough to report
}

// toolDisk returns a df-style table of filesystem usage, read purely from /proc/mounts
// and syscall.Statfs.
func toolDisk(ctx context.Context, lim Limits) (string, bool, error) {
	mounts, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return "", false, err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%-24s %8s %8s %8s %5s  %s\n",
		"Filesystem", "Size", "Used", "Avail", "Use%", "Mounted on")

	seen := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(mounts))
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return "", false, err
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

	out, capTrunc := capOutput(b.String(), lim.MaxOutput)
	return out, capTrunc, nil
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

// toolJournal returns recent systemd journal lines by invoking journalctl with a fixed,
// validated argument vector. It never constructs a shell command string.
func toolJournal(ctx context.Context, lim Limits, journalctlPath string, args protocol.JournalArgs) (string, bool, error) {
	if journalctlPath == "" {
		return "", false, fmt.Errorf("journalctl is not available on this host")
	}

	lines := args.Lines
	if lines <= 0 {
		lines = journalDefaultLines
	}
	if lines > journalMaxLines {
		lines = journalMaxLines
	}

	argv := []string{"--no-pager", "-n", strconv.Itoa(lines), "-o", "short-iso"}
	if args.Unit != "" {
		if !unitPattern.MatchString(args.Unit) {
			return "", false, fmt.Errorf("invalid unit name")
		}
		argv = append(argv, "-u", args.Unit)
	}

	out, err := runJournalctl(ctx, journalctlPath, argv)
	if err != nil {
		return "", false, err
	}

	capped, trunc := capOutput(string(out), lim.MaxOutput)
	return capped, trunc, nil
}
