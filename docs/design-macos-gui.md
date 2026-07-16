# Design: macOS GUI as a second wire client

A point-and-click deck for people who are not comfortable in a terminal,
macOS first. The CLI and the TUI stay exactly as they are: the GUI is an
additive frontend, not a replacement.

Status: phase 0 (the `internal/wire` extraction and shared attach
client, PR #111) is shipped; the protocol now lives in `internal/wire`,
not `internal/deck/wire.go` as the inventory below says. Phases 1-3 are
not started.

## The one-line architecture

The hard part already shipped. Since the session split
(docs/design-session-server.md), the deck core runs headless
(`choragos serve --detach`, `RunServer` in `internal/deck/server.go`) and
the TUI attaches to it over a framed unix-socket protocol
(`internal/deck/wire.go`, `internal/deck/client.go`). The GUI is simply
the second client of that protocol:

```
choragos serve --detach          macOS .app (Wails)
  session core ──ui socket──►      Go backend speaks the wire
  raw PTY bytes + JSON events      webview renders panes via xterm.js
```

No new backend, no daemon rewrite, and for v1 no wire change at all:
`proto = 1` already carries everything the GUI needs.

## What exists today (inventory)

Verified against the tree at the time of writing:

- Session core with zero UI imports: roster, PTYs, tasks, gates, IPC,
  supervision (`internal/deck/session.go`).
- Headless server, one attachable client, exact version match or refuse
  (`internal/deck/server.go`).
- Wire protocol v1: length-prefixed frames, two kinds. `output` ships raw
  PTY chunks per role index; `event` is one JSON envelope for everything
  else. Client to server: `input`, `resize`, `gate`, `restart`, `pause`,
  `reload`, `sphragis`, `layout`, `detach`, `quit`. Server to client:
  `welcome` (config, roster, board, gates, layout blob), ring replay,
  `ready`, then `roster`/`board`/`gates`/`status`/`bell`/`focus`/`reset`
  (`internal/deck/wire.go`).
- Attach handshake with `busy {pid}` and `mismatch {version}` refusals,
  ring replay to rebuild each pane's screen (`internal/deck/client.go`).
- Rendering is deliberately client-side: the server ships bytes, never
  frames. That decision is what makes a web-terminal client possible.

## Decisions

### Same repo, nested Go module

The GUI lives at `desktop/` in this repo as its own Go module
(`github.com/sphragis-oss/choragos/desktop`).

- The Go `internal` rule is import-path based: a package whose path
  starts with `github.com/sphragis-oss/choragos/` may import
  `github.com/sphragis-oss/choragos/internal/...` even from a nested
  module. This is the `golang.org/x/tools` + `gopls` pattern. A separate
  repo would force a public API commitment we do not want yet.
- The wire contract will churn while the GUI grows; one repo means one
  atomic PR per contract change instead of cross-repo version dances.
- The CLI is untouched: its `go.mod` gains nothing (Wails and the
  frontend toolchain live entirely under `desktop/`), its goreleaser and
  CI keep running as they are. The GUI gets its own workflow, path
  filtered on `desktop/**`, and its own artifact.
- Dev loop: a `go.work` at the repo root ties the two modules together
  locally. Released GUI builds pin a tagged core version.

### Framework: Wails, v2 today

| Option | Why / why not |
|---|---|
| **Wails v2 (chosen)** | Stable, Go-native backend so the wire client is a direct import, ~10 MB `.app`, WKWebView. |
| Wails v3 | Still alpha as of 2026-07 (per the [v3 roadmap](https://v3.wails.io/status/)); re-evaluate at phase 1 start, migration for an app this size is mechanical. |
| SwiftUI + SwiftTerm | Most native feel, but a second language and a hand-rolled wire codec; nothing forces it for v1. |
| Electron | Mature terminal stack but a 150 MB+ runtime and a sidecar process model we do not need since the backend is Go. |
| Tauri | Rust shell adds nothing when the core is Go. |

Panes render with xterm.js inside the webview: the Go backend forwards
raw `output` frames to the frontend, xterm.js does its own VT parsing and
scrollback. The core's vt10x emulators stay TUI-only; the server remains
byte-dumb, exactly as designed.

### Session lifecycle owned by the GUI

The `.app` bundles the `choragos` CLI binary in `Contents/Resources` and
spawns `choragos serve --detach` itself for whatever folder the user
picks. That guarantees the exact-version handshake always passes for
GUI-started sessions. Attaching to a session started by a different
install (e.g. Homebrew) can legitimately hit `mismatch`; the GUI surfaces
it in plain words ("this session was started by another choragos
version") instead of a stack trace.

Session discovery reuses the `choragos ls` mechanism (socket dir walk +
meta sidecars); kill reuses the `shutdown` control verb.

### Single client stays the rule

One attached client per session, GUI or TUI, first come first served;
the other gets `busy {pid}` and the GUI renders it as "already attached
in a terminal (pid N)". Concurrent read-only mirrors remain the same
protocol extension deferred in the session-server design.

## Core changes required (small, all behavior-neutral)

1. **Extract the wire protocol into `internal/wire`.** `wireConn`,
   `wireEvent`, `wireRole`, `wireTask`, `wireGate`, the frame kinds,
   `wireProto`, and the handshake constants are unexported inside
   `package deck` today; the desktop module cannot use them. Pure
   move-and-export refactor: `deck` server and TUI client switch to the
   new package, tests keep passing, no wire bytes change.
2. **Export a thin attach client.** The dial/hello/replay loop in
   `RunAttach` is TUI-flavored; factor the transport half (connect,
   handshake, replay callback, event pump) into `internal/wire` so TUI
   and GUI share it and only rendering differs.
3. **Nothing else.** Token/cost telemetry needs no wire support: the
   remote TUI already polls the gateway over HTTP from the client side,
   and the GUI backend does the same. Briefs and reports are files in the
   session's working directory; the GUI reads them directly, same host,
   same user.

Known non-blockers, recorded for honesty: the `output` frame encodes the
role index as one byte (255 roles per session) and the layout blob stays
an opaque client checkpoint the GUI simply ignores in favor of its own
persisted layout.

## GUI v1 scope (the non-tech user journey)

- **Onboarding:** folder picker, then a form over the existing
  `choragos init` templates: role list, command per role (with a "found
  in PATH" check, reusing the `doctor` logic), approve toggle, model.
  Writes a plain `.choragos.toml`, so the escape hatch to the CLI is
  always open and both frontends read the same file.
- **Session view:** sidebar of role cards (status dot, model, live
  tokens/cost), terminal area for the focused role plus a grid option,
  approval dialog when a gate arrives (approve / reject / view brief),
  task board panel, sphragis status pill with the on/off toggle.
- **Lifecycle in plain words:** "Quit and keep agents working" (detach)
  vs "Stop everything" (quit), restart/pause per role as buttons.
- **After the run:** render the `choragos report` summary.

Deliberately out of v1: broadcast input, scrollback search (xterm.js
find addon can come later), config file text editing (Open in Editor
button instead), Windows/Linux builds, remote-over-network sessions.

## Packaging and release

- `wails build` -> `Choragos.app` with the CLI binary bundled, then
  codesign with a Developer ID certificate, notarize, staple, and ship a
  `.dmg` on GitHub releases. A Homebrew cask can follow once the dmg is
  routine.
- CI: a separate macOS workflow triggered by `desktop/**` paths and
  `desktop/v*` tags. Core CI is not touched.
- Secrets: signing identity and notarization credentials as repo
  secrets; the workflow pins actions by SHA like the existing ones.

## Staging

| Phase | Ships | Verify |
|---|---|---|
| 0 | `internal/wire` extraction + shared attach client; no behavior change | `go test ./...` green; manual `serve --detach` + `attach` round trip unchanged |
| 1 | `desktop/` skeleton: connect to a running session, read-only mirror (roster cards + live xterm.js panes) | attach to a real session, watch agents work |
| 2 | Interactivity: keyboard input, resize, gate approve/reject, task board, restart/pause, lifecycle buttons | drive a full delegate/work-done cycle from the GUI |
| 3 | Onboarding form, session start/stop from the GUI, signed + notarized `.dmg`, release workflow | fresh Mac, no Homebrew, no terminal: install dmg, run a session |

Phase 0 is a core PR reviewable on its own. Phases 1-3 live under
`desktop/` and cannot break the CLI by construction.

## Open questions

1. Wails v2 vs v3 at phase 1 start: v3 may have reached beta by then;
   check its status page before scaffolding.
2. GUI version scheme: same tag train as the CLI or independent
   `desktop/v*` tags? Proposal: independent, the handshake already
   enforces compatibility per session.
3. Does phase 3 onboarding create the config with `choragos init`
   underneath, or write TOML itself? Proposal: shell out to the bundled
   binary; one template source of truth.
4. Issue tracker hygiene: `area/desktop` and `area/cli` labels from day
   one, since both live in one tracker.
