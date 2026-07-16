# Choragos Desktop

The macOS GUI, phase 1 of [docs/design-macos-gui.md](../docs/design-macos-gui.md):
a **read-only mirror** of a running choragos session. It lists live sessions,
attaches over the same wire protocol as `choragos attach` (`internal/wire`),
and renders the roster cards plus each role's live terminal with xterm.js.
No input, no gates yet; those are phase 2.

## Build and run

```sh
make build          # build/choragos-desktop (see Makefile for the CGO flags)
choragos serve --detach   # in some project directory
./build/choragos-desktop
```

The hello handshake requires the exact server version. Dev builds are `dev`
on both sides; pass `VERSION=x.y.z` to mirror a session started by a
released binary.

## Layout

- `main.go`, `app.go`: Wails v2 backend; the only protocol logic is
  `internal/wire` calls (imported from the parent module via `replace`).
- `frontend/dist/`: static vanilla JS/CSS, embedded at build time. No node
  toolchain; xterm.js is vendored under `frontend/dist/vendor/` (see
  `VENDOR.md` there).

Dev/test hook: `CHORAGOS_DESKTOP_AUTOATTACH=<dir>` attaches to that
directory's session on startup, skipping the picker.
