# diag — remote read-only diagnostic tool

One small Go binary that lets an operator inspect a remote server **read-only**, without
copy-pasting logs and configs by hand.

- **`diag serve`** — the server. Copied onto the remote host and run there. Listens on
  TLS, authenticates callers with a token it prints at startup, and exposes a fixed set
  of read-only diagnostic tools implemented directly in Go (no shell, no command
  parsing).
- **`diag <tool>`** — the client. A one-shot CLI run on the operator's machine. Each
  invocation dials the server, authenticates, runs one tool, prints the result, exits.

The same binary is both roles; the first argument selects one. It is **read-only by
design**. There are no write/mutate operations and no "run command" tool. The only
external program ever executed is `journalctl`, and only with a fixed, validated
argument vector.

## Design principles

1. **Read-only.** No tool modifies the host. Tools are hand-written Go that read state;
   they never spawn a shell.
2. **Path jailing.** File-reading tools are confined to configurable root directories.
   No path may escape them — no `..` traversal, no symlink escape (symlinks are resolved
   before the membership check).
3. **Token auth on every request.** No token, no service. Compared in constant time.
4. **TLS for confidentiality.** Ad-hoc self-signed cert; the client does not verify it.
   The token authenticates; TLS encrypts.
5. **Fail closed.** Bad token, bad path, unknown tool, malformed request → error.
6. **Bounded output.** Every tool caps its output size and respects a timeout.

## Build

```sh
CGO_ENABLED=0 go build -ldflags "-s -w" -o diag ./cmd/diag

# cross-compile for the target host, e.g.:
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o diag ./cmd/diag
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o diag ./cmd/diag
```

No third-party dependencies — standard library only.

## Run the server

`scp` `diag` to the remote host and run `diag serve` there, naming the directories file
tools may read within (the **jail roots**). With no roots, it defaults to the current
directory.

```sh
diag serve /var/log /etc       # two jail roots
diag serve                     # one jail root: the current working directory
```

Startup prints a banner including the token:

```
diag serve — read-only diagnostic server
listen : 0.0.0.0:7017
jails  : /var/log, /etc   (file tools may read within these)
token  : 9f2a5c1e...      (pass to the client via --token or DIAG_TOKEN)
ttl    : 30m (shuts down ~14:52)
tls    : ad-hoc self-signed; client skips verification by design
tools  : list read grep tail stat ps disk journal   (READ-ONLY)
```

Server flags (all follow `serve`):

| Flag | Default | Meaning |
| --- | --- | --- |
| `--host` | `0.0.0.0` | Bind address, `host` or `host:port` (port defaults to `7017`). Use `127.0.0.1` to restrict to local (e.g. an SSH tunnel). Falls back to `DIAG_HOST`. |
| `--token` | *(generated)* | Fixed token instead of a generated one. Falls back to `DIAG_TOKEN`. |
| `--ttl` | `30m` | Auto-shutdown after this duration. `0` disables (with a warning). |
| `--max-output` | `1048576` | Global output byte cap applied by tools. |
| `--timeout` | `15s` | Per-tool wall-clock timeout. |

> Passing `--token` on the command line makes the token visible in the host's process
> list (including via this tool's own `ps`). Prefer the generated token for real use.

> `DIAG_HOST` is read as the *bind* address by `serve` and as the *server* address by a
> tool subcommand. Exporting it for querying and then running `serve` in the same shell
> binds to that address — usually harmless, but it will fail loudly if the address is not
> local.

## Run the client

A tool subcommand is one-shot: connection details come from `--host` / `--token` or the
`DIAG_HOST` / `DIAG_TOKEN` environment variables (an explicit flag overrides the env
var). The host may omit the port, in which case `7017` is used. Connection flags may
appear before the subcommand or after it (interleaved with the tool's own arguments in
any order); given in both places, the one after the tool name wins. Run `diag help` for
the tool list and `diag help <tool>` (or `diag <tool> --help`) for a tool's arguments.

```sh
export DIAG_HOST=10.0.0.5          # or 10.0.0.5:7017
export DIAG_TOKEN=9f2a5c1e...

diag list  /etc
diag read  /var/log/syslog --max-bytes 20000 --offset 0
diag grep  /var/log --pattern "ERROR" --ignore-case --max-matches 500
diag tail  /var/log/nginx/access.log --lines 200
diag stat  /etc/hosts
diag ps
diag disk
diag journal --unit nginx --lines 100
```

The client does no formatting: the server produces CLI-style text and the client relays
`stdout` verbatim. If output was capped, `... (truncated)` is written to `stderr`.

Exit codes: `0` success, `1` protocol/transport error, `2` server-returned error,
`3` usage error.

## Tools (all READ-ONLY)

| Tool | Purpose | Notes |
| --- | --- | --- |
| `list` | Directory listing, `ls -l` style | Skips dotfiles unless `--all`. |
| `read` | File contents, byte-capped | `--max-bytes`, `--offset`; regular files only. |
| `grep` | RE2 search of a file or directory tree | `--pattern`, `--ignore-case`, `--max-matches`. |
| `tail` | Last N lines of a file | `--lines`; reads only the file's tail. |
| `stat` | Path metadata | name, path, size, mode, modtime, uid, gid. |
| `ps` | Process snapshot from `/proc` | PID, PPID, USER, RSS, CMD. |
| `disk` | Filesystem usage, `df` style | from `/proc/mounts` + `statfs`; skips pseudo FS. |
| `journal` | systemd journal lines | execs `journalctl` with a validated argv; `--unit` is allowlisted. |

## Security model and its limits

TLS encryption protects against **passive eavesdropping**. Because the client does not
verify the certificate, an **active man-in-the-middle** who can intercept the connection
is not stopped by TLS alone. The token prevents an interceptor from *using* the service,
but defeating MITM would require certificate verification, which this MVP omits by
design. This is fine for trusted networks or SSH-tunneled use; do not expose the server
on a hostile network expecting TLS to authenticate the endpoint.

The path jail is the most security-sensitive component. A client always addresses files
by their real filesystem path; a path is allowed only if, after cleaning and symlink
resolution, it lands inside a configured root. See `internal/tools/jail.go` and its
tests.

## Agent integration

The tool subcommands are a clean target for an AI agent (e.g. Claude Code) to drive,
because each call is one-shot, read-only, path-jailed, and bounded. The
`skills/remote-diag/SKILL.md` file teaches an agent when and how to use it. No
server-side changes are needed; the skill just runs `diag <tool>` as a human would.

## Layout

```
cmd/diag/           single binary: subcommand dispatch, serve, and tool client
internal/tools/     the tools (one file each), the Tool registry, and the path jail
internal/protocol/  shared wire envelope (newline-delimited JSON)
internal/tlsutil/   ad-hoc server cert + non-verifying client config
internal/server/    accept loop, auth, dispatch to the tools registry
internal/client/    dial + one request/response round trip
internal/netutil/   shared address helpers (default port)
skills/remote-diag/ operator-side agent skill
```

Each tool is a self-contained type in `internal/tools` that owns both faces of its
operation: the client-side CLI flags that build its request, and the server-side
execution that produces its output. Adding a tool is a new `tool_*.go` file plus one
line in the registry.

A future "write tier" is deliberately out of scope. The `Tool` interface already carries
a `ReadOnly` seam so write tools could be added and gated behind a flag without a
rewrite, but no such tool exists today.
