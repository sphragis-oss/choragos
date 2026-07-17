# Choragos Desktop

The macOS GUI from [docs/design-macos-gui.md](../docs/design-macos-gui.md).
It attaches to running sessions over the same wire protocol as
`choragos attach` (`internal/wire`) and renders roster cards plus each
role's live terminal with xterm.js. Typing goes to the focused pane's PTY,
panes resize with the window, approval gates pop a dialog (approve / view
brief / reject), the task board lists delegations, and roles can be
restarted or paused per card. "Quit, keep agents working" detaches; "Stop
everything" shuts the session down.

Onboarding (phase 3): "Open project folder…" picks a directory, offers a
starting team when no `.choragos.toml` exists (auto-detect or a template,
via the CLI's `init`), starts `choragos serve --detach` there, and
attaches. The CLI is resolved from the app bundle's Resources first, then
PATH.

## Build and run

```sh
cd desktop && make build      # build/choragos-desktop
./build/choragos-desktop
```

The hello handshake requires the exact server version, so `make build`
defaults VERSION to the installed CLI's version (falling back to `dev`
when none is found). Override it explicitly when needed:

```sh
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
