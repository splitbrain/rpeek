---
name: remote-diag
description: >
  Inspect a remote host that is running the read-only `diagd` diagnostic server.
  Use when the user wants to read logs, configs, process state, disk usage, or
  journald on a remote machine reachable via `diagctl` — e.g. "check why nginx is
  down on 10.0.0.5", "tail the app log on the prod box", "what's eating disk there".
  Do NOT use for the local machine (use normal shell tools) or for anything that
  writes/mutates — this server is read-only by design.
---

# Remote diagnostics via diagctl

## Preconditions
- `diagctl` is on PATH.
- Connection details are in the environment: `DIAG_HOST` (host, or host:port — the
  port defaults to 7017) and `DIAG_TOKEN`. If either is missing, ask the user for it
  — never guess a host or fabricate a token.
- Confirm reachability with a cheap call (`diagctl disk`) before a long investigation.

## Discovering the tools
The toolset is self-describing — do not rely on a hardcoded list, since it can change
between versions:
- `diagctl help` — lists the available tools, each with a one-line summary.
- `diagctl help <tool>` (or `diagctl <tool> --help`) — prints that tool's arguments and
  flags with their defaults and meanings.

Run `diagctl help` first whenever you are unsure what is available or what a tool
accepts; it is the authoritative reference. Every tool is READ-ONLY, and `help` needs no
host or token.

## How to use
Every invocation is one-shot: `diagctl <tool> [args]`. For path-taking tools the path
and any flags may appear in any order (`diagctl read /p --max-bytes 200` and
`diagctl read --max-bytes 200 /p` are equivalent). Read `stdout` as if it were the output
of the equivalent Unix command; it is already formatted server-side. On a non-zero exit,
read `stderr` and adapt — do not retry the identical command.

Representative calls (see `diagctl help <tool>` for each tool's full argument set):

```
diagctl list /var/log
diagctl read /etc/nginx/nginx.conf --max-bytes 20000
diagctl grep /var/log --pattern "ERROR" --ignore-case
diagctl tail /var/log/nginx/error.log --lines 200
diagctl stat /etc/hosts
diagctl ps
diagctl disk
diagctl journal --unit nginx --lines 100
```

## Rules
- Use only the tools `diagctl help` reports. Do not invent flags or tools; there is no
  write, delete, restart, or "run command" capability, by design.
- Paths are the host's **real** paths and must fall inside a jail root the operator
  granted. A "not within any allowed root" error means the directory was not granted
  — report that to the user; do not try to tunnel around it.
- Patterns are RE2 (Go `regexp`), not shell globs.
- If output ends with `... (truncated)` (stderr notes truncation), narrow the query
  (tighter `--pattern`, larger `--max-bytes` with `--offset` paging, fewer `--lines`)
  rather than assuming you saw everything.
- Never print or log the token. Do not pass `--token` on the command line if
  `DIAG_TOKEN` is set (avoids leaking it into shell history / transcripts).

## Exit codes
`0` success · `1` protocol/transport error · `2` server-returned error (bad path,
unauthorized, tool error) · `3` usage error. A `2` with "unauthorized" means the token
is wrong — stop and ask the user; do not brute-force.

## Working a problem
1. **Establish context** — read `DIAG_HOST`/`DIAG_TOKEN`; sanity-check with `disk` or
   `ps`. Missing/rejected token → stop and ask the user.
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
diagctl ps                                  # is nginx running? -> not in the table
diagctl journal --unit nginx --lines 50     # why did it stop? -> "bind() to :443 failed"
diagctl grep /etc/nginx --pattern "listen"  # what claims 443? -> two server blocks
diagctl disk                                 # rule out a full disk -> plenty free
# -> report: duplicate `listen 443` in <file:line>; nginx aborts on bind conflict.
```
