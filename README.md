# rpeek — remote read-only diagnostic tool

One small Go binary that lets an agent inspect a remote server **read-only**, without
copy-pasting logs and configs by hand.

- **`rpeek serve`** — the server. Copied onto the remote host and run there. Listens on
  TLS, authenticates callers with a token it prints at startup, and exposes a fixed set
  of read-only diagnostic tools implemented directly in Go (no shell, no command
  parsing).
- **`rpeek <tool>`** — the client. A one-shot CLI run on the operator's machine. Each
  invocation dials the server, authenticates, runs one tool, prints the result, exits.

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

No third-party dependencies — standard library only.

## Run the server

`scp` `rpeek` to the remote host and run `rpeek serve` there, naming the directories file
tools may read within (the **jail roots**). With no roots, it defaults to the current
directory.

```sh
rpeek serve /var/log /etc       # two jail roots
rpeek serve                     # one jail root: the current working directory
```

Startup prints a banner including the token.

Listen address and token may be overridden with the `--host` and `--token` flags or the `RPEEK_HOST` and `RPEEK_TOKEN` environment variables. The server runs until killed or until the `--ttl` expires.

Run `rpeek serve --help` for all flags.

## Run the client

Supply connection details via `--host` / `--token` or the `RPEEK_HOST` / `RPEEK_TOKEN` environment
variables (token must match the server's) and the tool name plus its arguments. Output is printed to STDOUT, errors to STDERR.

Run `rpeek help` for the tool list and `rpeek help <tool>`  for a tool's arguments.

Examples:

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

Exit codes: `0` success, `1` protocol/transport error, `2` server-returned error,
`3` usage error.

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
`skills/rpeek/SKILL.md` file teaches an agent when and how to use it.
