# Sandboxing recipes

Choragos runs agents as regular child processes of your user; it is not a
sandbox and does not claim to be one (see the
[threat model](../SECURITY.md#threat-model)). A role's `command` is
arbitrary, so isolation composes from the outside: wrap a single role in a
sandbox, or run the whole deck inside one. This page collects working
recipes for both.

## What a sandboxed role still needs

A worker inside a sandbox keeps the same contract as one outside it:

- **The workspace.** Task briefs are files in the working directory
  (`.choragos/worker-task-<role>.md`); the injected task line asks the agent
  to read them. Mount or bind the project directory read-write.
- **The control socket and the `choragos` binary.** Workers report with
  `choragos work-done`, which dials the unix socket in `$CHORAGOS_SOCK`.
  The socket path must resolve inside the sandbox and the binary must be on
  its PATH.
- **Egress to its LLM endpoint.** Cutting all network kills the agent
  itself. When the gateway is enabled, the role's endpoint is the address in
  `$ANTHROPIC_BASE_URL` (or your `base_url_env` names) instead.

## Wrapper scripts, not inline args

`args` entries are passed verbatim to `exec`; there is no shell, so
`${PWD}` and friends do not expand. Point `command` at an executable
wrapper script instead; paths with a slash bypass PATH lookup:

```toml
[[roles]]
name = "coder"
command = "./sandbox/coder.sh"
model = "opus"
model_flag = "--model"  # explicit: the wrapper forwards "$@" to an agent that accepts it
```

`choragos doctor` warns when `model` is set and the command is not a known
model-aware CLI; setting `model_flag` explicitly (even to the default)
records that the wrapper handles it and silences the warning.

## Per-role: Docker (Linux)

`sandbox/coder.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
sockdir="$(dirname "$CHORAGOS_SOCK")"
exec docker run --rm -i \
  -v "$PWD:/ws" -w /ws \
  -v "$sockdir:$sockdir" \
  -e CHORAGOS_SOCK \
  -e ANTHROPIC_API_KEY \
  your-agent-image claude "$@"
```

The image must contain both the agent CLI and the `choragos` binary (for
`work-done`). The socket directory is mounted at the same path so
`$CHORAGOS_SOCK` stays valid. Add `-e` lines for whatever your agent needs;
everything else stays outside, which composes with `env_allow` rather than
replacing it.

Unix sockets do not cross the Docker Desktop VM boundary on macOS; on a Mac
run the whole deck inside the container instead (below).

## Per-role: bubblewrap (Linux)

Filesystem containment without images:

```bash
#!/usr/bin/env bash
set -euo pipefail
exec bwrap \
  --ro-bind / / \
  --dev /dev --proc /proc \
  --bind "$PWD" "$PWD" \
  --bind "$(dirname "$CHORAGOS_SOCK")" "$(dirname "$CHORAGOS_SOCK")" \
  --tmpfs "$HOME/.ssh" \
  --unshare-pid \
  claude "$@"
```

Root is read-only, only the workspace and socket directory are writable,
and `~/.ssh` is masked with an empty tmpfs. Network stays shared because
the agent needs its endpoint; add `--unshare-net` only for roles that talk
exclusively to a unix socket.

## Per-role: sandbox-exec (macOS)

`sandbox-exec` is marked deprecated by Apple but still functions and is
what several agent CLIs use themselves. A write-containment profile:

```scheme
; coder.sb: allow reads, confine writes to the workspace and temp
(version 1)
(allow default)
(deny file-write*)
(allow file-write* (subpath (param "WS")) (subpath "/private/tmp")
  (subpath (param "TMP")) (literal "/dev/null") (subpath "/dev/tty"))
```

```bash
#!/usr/bin/env bash
set -euo pipefail
exec sandbox-exec -D WS="$PWD" -D TMP="${TMPDIR:-/tmp}" -f sandbox/coder.sb claude "$@"
```

Reads stay open (deny-read profiles usually break the CLI itself); this
contains file damage, not exfiltration.

## Whole deck: container or VM

The strongest simple boundary: run everything inside, so sockets, task
files, and checkpoints never leave the sandbox.

```bash
docker run --rm -it \
  -v "$PWD:/ws" -w /ws \
  -e ANTHROPIC_API_KEY \
  your-deck-image choragos serve
```

The image needs `choragos`, git (for checkpoints), and your agent CLIs. A
devcontainer works the same way: put `choragos serve` in a terminal of a
devcontainer that already has your agents, and the host filesystem outside
the workspace mount is simply not there. This is also the only clean Docker
recipe on macOS.

## Egress

Choragos does not enforce egress; `base_url_env` redirects cooperating
clients only. If you need real egress control, do it at the sandbox layer:
an allowlisting proxy plus a container network that only reaches it, or an
OS firewall profile. Instance-metadata endpoints (169.254.169.254) deserve
an explicit block anywhere agents run in a cloud VM.
