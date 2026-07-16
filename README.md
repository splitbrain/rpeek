# rpeek — remote read-only diagnostic tool

One small Go binary that lets an operator inspect a remote server **read-only**, without
copy-pasting logs and configs by hand.

- **`rpeek serve`** — the server. Copied onto the remote host and run there. Listens on
  TLS, authenticates callers with a token it prints at startup, and exposes a fixed set
  of read-only diagnostic tools implemented directly in Go (no shell, no command
  parsing).
- **`rpeek <tool>`** — the client. A one-shot CLI run on the operator's machine. Each
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
make build      # static binary into bin/rpeek, version stamped from git
make help       # list all targets
```

Or build directly with the Go toolchain:

```sh
CGO_ENABLED=0 go build -ldflags "-s -w" -o rpeek ./cmd/rpeek

# cross-compile for the target host, e.g.:
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o rpeek ./cmd/rpeek
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o rpeek ./cmd/rpeek
```

`make build` stamps the version from `git describe` automatically; unstamped builds report
`dev`. With the Go toolchain, pass it via the linker's `-X` flag:

```sh
go build -ldflags "-s -w -X rpeek/internal/version.Version=v1.2.3" -o rpeek ./cmd/rpeek
```

`make dist` cross-compiles the release archives locally for every supported platform.

Tagged releases (`vX.Y.Z`) are built for Linux and macOS on `amd64` and `arm64` by the
GitHub Actions workflow in `.github/workflows/ci.yml`, which also runs the tests on every
push and pull request.

No third-party dependencies — standard library only.

## Run the server

`scp` `rpeek` to the remote host and run `rpeek serve` there, naming the directories file
tools may read within (the **jail roots**). With no roots, it defaults to the current
directory.

```sh
rpeek serve /var/log /etc       # two jail roots
rpeek serve                     # one jail root: the current working directory
```

Startup prints a banner including the token:

```
rpeek serve — read-only diagnostic server
listen : 0.0.0.0:7017
jails  : /var/log, /etc   (file tools may read within these)
token  : 9f2a5c1e...      (pass to the client via --token or RPEEK_TOKEN)
ttl    : 30m (shuts down ~14:52)
tls    : ad-hoc self-signed; client skips verification by design
tools  : hostname list read grep tail stat ps disk journal   (READ-ONLY)
```

Server flags (all follow `serve`):

| Flag | Default | Meaning |
| --- | --- | --- |
| `--host` | `0.0.0.0` | Bind address, `host` or `host:port` (port defaults to `7017`). Use `127.0.0.1` to restrict to local (e.g. an SSH tunnel). Falls back to `RPEEK_HOST`. |
| `--token` | *(generated)* | Fixed token instead of a generated one. Falls back to `RPEEK_TOKEN`. |
| `--ttl` | `30m` | Auto-shutdown after this duration. `0` disables (with a warning). |
| `--max-output` | `1048576` | Global output byte cap applied by tools. |
| `--timeout` | `15s` | Per-tool wall-clock timeout. |

> Passing `--token` on the command line makes the token visible in the host's process
> list (including via this tool's own `ps`). Prefer the generated token for real use.

> `RPEEK_HOST` is read as the *bind* address by `serve` and as the *server* address by a
> tool subcommand. Exporting it for querying and then running `serve` in the same shell
> binds to that address — usually harmless, but it will fail loudly if the address is not
> local.

## Run the client

A tool subcommand is one-shot: connection details come from `--host` / `--token` or the
`RPEEK_HOST` / `RPEEK_TOKEN` environment variables (an explicit flag overrides the env
var). The host may omit the port, in which case `7017` is used. Connection flags may
appear before the subcommand or after it (interleaved with the tool's own arguments in
any order); given in both places, the one after the tool name wins. Run `rpeek help` for
the tool list and `rpeek help <tool>` (or `rpeek <tool> --help`) for a tool's arguments.

```sh
export RPEEK_HOST=10.0.0.5          # or 10.0.0.5:7017
export RPEEK_TOKEN=9f2a5c1e...

rpeek list  /etc
rpeek read  /var/log/syslog --max-bytes 20000 --offset 0
rpeek grep  /var/log --pattern "ERROR" --ignore-case --max-matches 500
rpeek tail  /var/log/nginx/access.log --lines 200
rpeek stat  /etc/hosts
rpeek ps
rpeek disk
rpeek journal --unit nginx --lines 100
```

`rpeek version` prints the local build version. Given both `--host` and `--token` (or
`RPEEK_HOST` / `RPEEK_TOKEN`) it also queries the server and prints its version alongside
the local one — a quick way to confirm which build is deployed on a host:

```sh
rpeek version                                        # local build only
rpeek --host 10.0.0.5 --token 9f2a5c1e... version    # local + remote
```

The client does no formatting: the server produces CLI-style text and the client relays
`stdout` verbatim. The one exception is `version`, which labels the local and remote
builds when it prints both. If output was capped, `... (truncated)` is written to
`stderr`.

Exit codes: `0` success, `1` protocol/transport error, `2` server-returned error,
`3` usage error.

## Tools (all READ-ONLY)

Most tools run on the server and need a connection; `help` and `version` also run in the
client (`help` never touches a server; `version` reports the server's build too when one
is given).

| Tool | Purpose | Notes |
| --- | --- | --- |
| `help` | List the tools, or one tool's usage | Client-only, no server needed; `help <tool>` details one. |
| `hostname` | Server hostname | No args, no jail; cheapest connectivity and auth check. |
| `version` | rpeek build version | No args, no jail; local build, plus the server's when connected. |
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
`skills/rpeek/SKILL.md` file teaches an agent when and how to use it. No
server-side changes are needed; the skill just runs `rpeek <tool>` as a human would.

## Layout

```
cmd/rpeek/          single binary: subcommand dispatch and the tool client
internal/tools/     every subcommand, the registry, the path jail, and the server Runner
internal/protocol/  shared wire envelope (newline-delimited JSON)
internal/tlsutil/   ad-hoc server cert + non-verifying client config
internal/server/    accept loop, auth, request/response envelope; tool-agnostic
internal/client/    dial + one request/response round trip
internal/netutil/   shared address helpers (default port)
skills/rpeek/       operator-side agent skill
```

Each subcommand is a self-contained type in `internal/tools` carrying its flag parsing
plus whichever execution halves it supports: `Local` (runs in the client), `Remote` (runs
on the server), or `Serve` (the server process — only `serve`). `version` has both
client-and-server halves; `help` is client-only; the diagnostic tools are server-only.
Adding one is a new `tool_*.go` file plus one line in the registry.

The server is agnostic to the tool set: it authenticates a request and hands it to a
`ToolRunner` interface, which `internal/tools` supplies. That inversion is why `internal/
tools` imports `internal/server` (so the `serve` tool can stand a server up) and not the
reverse.

A future "write tier" is deliberately out of scope. The `Tool` interface already carries
a `ReadOnly` seam so write tools could be added and gated behind a flag without a
rewrite, but no such tool exists today.
