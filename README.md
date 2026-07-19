<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-wordmark-dark.svg">
    <img alt="Choragos" src="assets/logo-wordmark-light.svg" width="360">
  </picture>
</p>

<p align="center">
  <strong>
    <a href="#quick-start">Getting Started</a>
    &nbsp;&nbsp;&bull;&nbsp;&nbsp;
    <a href="CONTRIBUTING.md">Contributing</a>
    &nbsp;&nbsp;&bull;&nbsp;&nbsp;
    <a href="https://github.com/sphragis-oss/choragos/issues">Get In Touch</a>
  </strong>
</p>

<p align="center">
  <a href="https://github.com/sphragis-oss/choragos/actions/workflows/ci.yml?query=branch%3Amain">
    <img alt="Build Status" src="https://img.shields.io/github/actions/workflow/status/sphragis-oss/choragos/ci.yml?branch=main&style=for-the-badge&label=tests">
  </a>
  <a href="https://github.com/sphragis-oss/choragos/releases">
    <img alt="Latest Release" src="https://img.shields.io/github/v/release/sphragis-oss/choragos?include_prereleases&style=for-the-badge">
  </a>
  <a href="https://codecov.io/gh/sphragis-oss/choragos">
    <img alt="Coverage" src="https://img.shields.io/codecov/c/github/sphragis-oss/choragos?style=for-the-badge">
  </a>
  <a href="LICENSE">
    <img alt="License" src="https://img.shields.io/badge/License-Apache%202.0-blue.svg?style=for-the-badge">
  </a>
</p>

---

Choragos is a multi-agent development orchestrator. You define the team in a single config file, the roles, the CLI agent each one runs, and the model behind it, and choragos runs the chorus: owned PTY panes per agent, a delegate/work-done protocol over a local socket, approval and judge gates, checkpoints, and live telemetry. It can also route every agent's LLM traffic through [Sphragis](https://github.com/sphragis-oss/sphragis), an EU AI Act compliance gateway, for local PII redaction and a tamper-evident audit log. The gateway is optional: choragos works standalone, and every feature degrades cleanly without it.

> The name is the Greek χορηγός (*choragos*), the one who led and funded the chorus. Here it leads a chorus of agents.

<p align="center">
  <img alt="Choragos deck demo: tiling window manager over agent panes" src="assets/demo.gif" width="900">
</p>

## Why Choragos?

- **Owned PTY panes:** Choragos spawns each agent in a pseudo-terminal it owns and parses (`hinshun/vt10x`), so it knows real input readiness instead of polling a status that lies. This removes the boot races that plague multiplexer-driven orchestrators.
- **Delegate/work-done protocol:** The orchestrator agent hands work to workers via a local UNIX socket with `choragos delegate --to <role> --task "..."`; workers report back with `choragos work-done`.
- **Sphragis in the data path, fail-closed when you ask for it:** With the gateway enabled, every worker is launched with its LLM base URL pointed at a local Sphragis gateway, and delegation is refused while it is down. When you never asked for it and it is not installed, the deck simply runs with the gateway off and says so.
- **Live token and cost telemetry:** With the gateway in the path, each role's status card shows its model and live token counts, and dollar cost once you set a `[pricing]` table. No SDK hooks, no vendor lock: the gateway counts what the provider reports. Set `budget = "5.00"` on a role to be notified, or have it paused, the moment its session cost crosses the cap. After the run, `choragos report` summarizes tasks, durations, token burn, and cost per role from the event log.
- **Runtime control:** Per-role delegation timeouts catch a worker stuck in a loop (`timeout = "45m"`, notify or restart), and `prefix+p` freezes a role (SIGSTOP) so you can inspect its work mid-flight and resume without losing the agent's context.
- **Least privilege per role (opt-in):** By default roles inherit the parent environment. Set `env_allow` on a role to switch it to an allowlist (baseline vars like `PATH`/`HOME`/`TERM` plus the names or `PREFIX_*` patterns you list), or `env_deny` to strip specific variables, so a reviewer never sees your `AWS_*` credentials. Choragos is not a sandbox: agents run as your user, and OS-level isolation composes from outside (containers, VMs, a wrapper as the role's `command`). See the [threat model](SECURITY.md#threat-model).

## Architecture

```mermaid
graph TD
    User([User]) -->|Tasks| Deck[Choragos Deck TUI]
    Deck <-->|Unix Socket IPC| IPC
    
    subgraph Agents
        Orchestrator[Orchestrator Agent]
        Coder[Coder Agent]
        Reviewer[Reviewer Agent]
    end
    
    Deck -->|PTY| Orchestrator
    Deck -->|PTY| Coder
    Deck -->|PTY| Reviewer
    
    Orchestrator -->|delegate| IPC
    Coder -->|work-done| IPC
    Reviewer -->|work-done| IPC
    
    Orchestrator -.->|LLM Traffic| Sphragis
    Coder -.->|LLM Traffic| Sphragis
    Reviewer -.->|LLM Traffic| Sphragis
    
    Sphragis[Sphragis Gateway] -.->|Redacted| Anthropic[Upstream APIs]
```

## Quick Start

### Prerequisites
- Go 1.26+
- Supported CLI agents installed (e.g. `claude`, `agy`)
- [Sphragis](https://github.com/sphragis-oss/sphragis) installed in PATH (optional, for compliance routing and token telemetry)

### Installation

Via Homebrew (macOS / Linux):
```bash
brew install sphragis-oss/sphragis/choragos
```

Prefer a GUI? Choragos Desktop is a native macOS app with the matching
CLI bundled inside, no terminal needed:
```bash
brew install --cask sphragis-oss/sphragis/choragos-desktop
```
(or grab the `.dmg` from the [`desktop/v*` releases](https://github.com/sphragis-oss/choragos/releases))

Or from source:
```bash
git clone https://github.com/sphragis-oss/choragos.git
cd choragos
make build
```

### Usage
```bash
# Write a starter .choragos.toml (roles, keybindings, UI options)
./choragos init

# Or start from a team template: starter, claude-team, mixed-team, review
./choragos init --template review

# Or let it detect the project (go.mod, package.json, Cargo.toml, pyproject.toml)
# and write a team with language-specific roles
./choragos init --auto

# Start the TUI
./choragos serve

# After a run: per-role tasks, durations, and token usage
./choragos report
```

Choragos will start the agents and, when Sphragis is installed or explicitly enabled, start the gateway and route all traffic through it automatically; otherwise it runs standalone with the gateway off.

The deck is a tiling window manager over the role panes, driven tmux-style behind a prefix key (default `ctrl+b`): split (`v`, `-`), move focus (`h/j/k/l`, `1..9`), zoom (`z`), live resize (`r`), restart a role (`R`), pause/resume a role (`p`), broadcast input to all agents (`a`), task board (`t`), scrollback search (`/`), and a help overlay (`?`). Closing a tile never kills its agent, the mouse focuses tiles and scrolls history, and the terminal bell rings when an agent blocks waiting for input. All bindings are configurable under `[keys]` in `.choragos.toml`.

Sessions detach like tmux does: `choragos serve --detach` runs the crew headless, `choragos attach` brings the TUI back with screens, tasks, gates, and layout restored, and `prefix+d` leaves the agents running when you go. `choragos ls` and `choragos kill` manage sessions across projects.

### Documentation

- [Keybindings](docs/keybindings.md) - the full keymap and window-manager modes
- [Configuration reference](docs/configuration.md) - every `.choragos.toml` key, including per-role env isolation
- [Building your own team](docs/teams.md) - roles, per-role models, briefs, and a worked pipeline example
- [Control protocol](docs/protocol.md) - the delegate/work-done wire contract for integrators
- [Troubleshooting](docs/troubleshooting.md) - and run `choragos doctor` for automated checks
- [Long-running sessions](docs/long-running-sessions.md) - native detach/attach and session management
- [Sandboxing recipes](docs/sandboxing.md) - wrapping roles or the whole deck in Docker, bubblewrap, or sandbox-exec
- [Verifying releases](SECURITY.md#verifying-releases) - cosign signatures, checksums, provenance

## Configuration & Roles

The team is completely configurable via `.choragos.toml`. The default team looks like this:

| Role | Default agent | Job |
|------|---------------|-----|
| orchestrator | claude (opus) | plans and delegates, never implements |
| coder | claude (opus) | implements changes |
| reviewer | agy (Gemini) | reviews diffs, reports only |
| auditor | claude (sonnet) | security audit, reports only |
| release | claude (haiku) | runs the release flow after human sign-off |

Every role's agent binary and model is user-overridable.

## Development

- `make build`: Build the binary.
- `make demo`: Run the UI with placeholder cat panes (with Sphragis off).
- `make test`: Run tests with the race detector.

## Contributing

We welcome contributions! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for details on how to set up your dev environment, formatting rules, and PR guidelines. Note that this project requires a Developer Certificate of Origin (DCO) sign-off on every commit.

## Community

- [Code of Conduct](CODE_OF_CONDUCT.md)
- [Governance](GOVERNANCE.md)
- [Maintainers](MAINTAINERS.md)
- [Adopters](ADOPTERS.md)

## License

Licensed under the [Apache 2.0 License](LICENSE).
