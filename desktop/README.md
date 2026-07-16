# Choragos Desktop

The macOS GUI from [docs/design-macos-gui.md](../docs/design-macos-gui.md).
It lists live sessions, attaches over the same wire protocol as
`choragos attach` (`internal/wire`), and renders the roster cards plus each
role's live terminal with xterm.js. Interactive since phase 2: typing goes
to the focused pane's PTY, panes resize with the window, approval gates pop
a dialog (approve / view brief / reject), the task board lists delegations,
and roles can be restarted or paused per card. "Quit, keep agents working"
detaches; "Stop everything" shuts the session down.

## Build and run

```sh
cd desktop && make build      # build/choragos-desktop
choragos serve --detach       # in some project directory
./build/choragos-desktop      # pick the session, or double-click it later
```

The hello handshake requires the exact server version. `make build` produces
a `dev` client, matching sessions started by a dev-built `choragos`. To
attach to sessions started by a released install (e.g. Homebrew), build
with the same version string:

```sh
choragos version              # e.g. "choragos 0.11.1"
make build VERSION=0.11.1
```

## Layout

- `main.go`, `app.go`: Wails v2 backend; the only protocol logic is
  `internal/wire` calls (imported from the parent module via `replace`).
- `frontend/dist/`: static vanilla JS/CSS, embedded at build time. No node
  toolchain; xterm.js and its fit addon are vendored under
  `frontend/dist/vendor/` (see `VENDOR.md` there).

Dev/test hook: `CHORAGOS_DESKTOP_AUTOATTACH=<dir>` attaches to that
directory's session on startup, skipping the picker.
