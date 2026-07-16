# diag — remote read-only diagnostic tool

Two small Go binaries that let an operator inspect a remote server **read-only**,
without copy-pasting logs and configs by hand.

- **`diagd`** — the server. Copied onto the remote host and run there. Listens on TLS,
  authenticates callers with a token it prints at startup, and exposes a fixed set of
  read-only diagnostic tools implemented directly in Go (no shell, no command parsing).
- **`diagctl`** — the client. A one-shot CLI run on the operator's machine. Each
  invocation dials the server, authenticates, runs one tool, prints the result, exits.

It is **read-only by design**. There are no write/mutate operations and no
"run command" tool. The only external program ever executed is `journalctl`, and only
with a fixed, validated argument vector.

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
CGO_ENABLED=0 go build -ldflags "-s -w" -o diagd   ./cmd/diagd
CGO_ENABLED=0 go build -ldflags "-s -w" -o diagctl ./cmd/diagctl

# cross-compile diagd for the target host, e.g.:
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o diagd ./cmd/diagd
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o diagd ./cmd/diagd
```

No third-party dependencies — standard library only.

## Run the server

`scp` `diagd` to the remote host and run it there, naming the directories file tools may
read within (the **jail roots**). With no roots, it defaults to the current directory.

```sh
diagd /var/log /etc            # two jail roots
diagd                          # one jail root: the current working directory
```

Startup prints a banner including the token:

```
diagd — read-only diagnostic server
listen : 0.0.0.0:7017
jails  : /var/log, /etc   (file tools may read within these)
token  : 9f2a5c1e...      (pass to diagctl via --token or DIAG_TOKEN)
ttl    : 30m (shuts down ~14:52)
tls    : ad-hoc self-signed; client uses --insecure by design
tools  : list read grep tail stat ps disk journal   (READ-ONLY)
```

Server flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--listen` | `0.0.0.0` | Bind address, `host` or `host:port` (port defaults to `7017`). Use `127.0.0.1` to restrict to local (e.g. an SSH tunnel). |
| `--token` | *(generated)* | Fixed token instead of a generated one. |
| `--ttl` | `30m` | Auto-shutdown after this duration. `0` disables (with a warning). |
| `--max-output` | `1048576` | Global output byte cap applied by tools. |
| `--timeout` | `15s` | Per-tool wall-clock timeout. |

> Passing `--token` on the command line makes the token visible in the host's process
> list (including via this tool's own `ps`). Prefer the generated token for real use.

## Run the client

`diagctl` is one-shot: connection details come from flags or the `DIAG_HOST` /
`DIAG_TOKEN` environment variables (an explicit flag overrides the env var). The host
may omit the port, in which case `7017` is used. For path-taking tools the path and any
flags may appear in any order. Run `diagctl help` for the tool list and
`diagctl help <tool>` (or `diagctl <tool> --help`) for a tool's arguments.

```sh
export DIAG_HOST=10.0.0.5          # or 10.0.0.5:7017
export DIAG_TOKEN=9f2a5c1e...

diagctl list  /etc
diagctl read  /var/log/syslog --max-bytes 20000 --offset 0
diagctl grep  /var/log --pattern "ERROR" --ignore-case --max-matches 500
diagctl tail  /var/log/nginx/access.log --lines 200
diagctl stat  /etc/hosts
diagctl ps
diagctl disk
diagctl journal --unit nginx --lines 100
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
design. This is fine for trusted networks or SSH-tunneled use; do not expose `diagd` on a
hostile network expecting TLS to authenticate the endpoint.

The path jail is the most security-sensitive component. A client always addresses files
by their real filesystem path; a path is allowed only if, after cleaning and symlink
resolution, it lands inside a configured root. See `internal/server/jail.go` and its
tests.

## Agent integration

`diagctl` is a clean target for an AI agent (e.g. Claude Code) to drive, because it is
one-shot, read-only, path-jailed, and bounded. The `skills/remote-diag/SKILL.md` file
teaches an agent when and how to use it. No server-side changes are needed; the skill
just calls `diagctl` as a human would.

## Layout

```
cmd/diagd/       server binary
cmd/diagctl/     client binary
internal/protocol/  shared wire types (newline-delimited JSON)
internal/tlsutil/   ad-hoc server cert + insecure client config
internal/server/    accept loop, auth, dispatch, jail, tools
internal/client/    dial + one request/response round trip
skills/remote-diag/ operator-side agent skill
```

A future "write tier" is deliberately out of scope. The dispatch table already carries a
per-tool `ReadOnly` seam so write tools could be added and gated behind a flag without a
rewrite, but no such tool exists today.
