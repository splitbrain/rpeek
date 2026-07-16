---
name: remote-diag
description: >
  Inspect a remote host that is running the read-only `diag serve` diagnostic server.
  Use when the user wants to read logs, configs, process state, disk usage, or
  journald on a remote machine reachable via the `diag` client — e.g. "check why nginx
  is down on 10.0.0.5", "tail the app log on the prod box", "what's eating disk there".
  Do NOT use for the local machine (use normal shell tools) or for anything that
  writes/mutates — this server is read-only by design.
---

# Remote diagnostics via diag

## Preconditions
- `diag` is on PATH.
- Connection details — a host (or host:port; the port defaults to 7017) and a token,
  both required — come from one of two sources, in this precedence:
  1. Positional args to this skill, invoked as `/remote-diag <host> <token>`. Both are
     mandatory; the token is never optional.
  2. The environment: `DIAG_HOST` and `DIAG_TOKEN`, if already exported.
  If a host or token is missing from both, ask the user — never guess a host or
  fabricate a token.
- Confirm reachability and authorization with a cheap call (`diag hostname`) before a
  long investigation; it needs no jail and its output names the host that answered.

## Applying the connection details
Shell state does not persist between calls and no token file is used, so the host and
token must accompany every `diag` invocation:
- Given as positional args to the skill: prefix each call inline via environment
  variables — `DIAG_HOST=<host> DIAG_TOKEN=<token> diag <tool> [args]` — reusing the
  same values for every call this session.
- Already exported in the environment: call `diag <tool> [args]` directly.
Pass the token only through `DIAG_TOKEN`, never via `--token`.

## Discovering the tools
The toolset is self-describing — do not rely on a hardcoded list, since it can change
between versions:
- `diag help` — lists the available tools, each with a one-line summary.
- `diag help <tool>` (or `diag <tool> --help`) — prints that tool's arguments and
  flags with their defaults and meanings.

Run `diag help` first whenever you are unsure what is available or what a tool
accepts; it is the authoritative reference. Every tool is READ-ONLY, and `help` needs no
host or token.

## How to use
Every invocation is one-shot: `diag <tool> [args]`, carrying the connection details as
described above. A tool's own flags and its path may appear in any order (`diag read /p
--max-bytes 200` and `diag read --max-bytes 200 /p` are equivalent). Read `stdout` as if
it were the output of the equivalent Unix command; it is already formatted server-side. On
a non-zero exit, read `stderr` and adapt — do not retry the identical command.

Representative calls (connection prefix omitted for brevity; see `diag help <tool>` for
each tool's full argument set):

```
diag hostname
diag list /var/log
diag read /etc/nginx/nginx.conf --max-bytes 20000
diag grep /var/log --pattern "ERROR" --ignore-case
diag tail /var/log/nginx/error.log --lines 200
diag stat /etc/hosts
diag ps
diag disk
diag journal --unit nginx --lines 100
```

## Rules
- Use only the tools `diag help` reports. Do not invent flags or tools; there is no
  write, delete, restart, or "run command" capability, by design.
- Paths are the host's **real** paths and must fall inside a jail root the operator
  granted. A "not within any allowed root" error means the directory was not granted
  — report that to the user; do not try to tunnel around it.
- Patterns are RE2 (Go `regexp`), not shell globs.
- If output ends with `... (truncated)` (stderr notes truncation), narrow the query
  (tighter `--pattern`, larger `--max-bytes` with `--offset` paging, fewer `--lines`)
  rather than assuming you saw everything.
- Never print the token in prose or reports, and never pass it via `--token`; supply it
  only through the `DIAG_TOKEN` environment variable.

## Exit codes
`0` success · `1` protocol/transport error · `2` server-returned error (bad path,
unauthorized, tool error) · `3` usage error. A `2` with "unauthorized" means the token
is wrong — stop and ask the user; do not brute-force.

## Working a problem
1. **Establish context** — resolve the host and token (skill positional args or the
   environment); sanity-check with `hostname` (or `disk`/`ps`). Missing/rejected token →
   stop and ask the user.
2. **Map the question to tools** — "is the service up?" → `ps` / `journal --unit`;
   "why is it erroring?" → `tail` then `grep` the log; "is the box full?" → `disk`.
3. **Run, read, refine** — each call returns pre-formatted text; interpret it and choose
   the next call. Calls are cheap and read-only, so breadth-first probing is fine.
4. **Respect the boundaries** — jail/auth/truncation errors are signals to change
   approach or ask the user, never to escalate. There is nothing to escalate to.
5. **Report** — synthesize findings, citing the concrete evidence (`tail` lines, `grep`
   hits) rather than raw dumps.

Example (user: "nginx is down on the prod box, why?"):

```
diag ps                                  # is nginx running? -> not in the table
diag journal --unit nginx --lines 50     # why did it stop? -> "bind() to :443 failed"
diag grep /etc/nginx --pattern "listen"  # what claims 443? -> two server blocks
diag disk                                # rule out a full disk -> plenty free
# -> report: duplicate `listen 443` in <file:line>; nginx aborts on bind conflict.
```
