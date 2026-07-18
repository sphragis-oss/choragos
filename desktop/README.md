# Choragos Desktop

The macOS GUI from [docs/design-macos-gui.md](../docs/design-macos-gui.md).
It attaches to running sessions over the same wire protocol as
`choragos attach` (`internal/wire`) and renders roster cards plus each
role's live terminal with xterm.js. Typing goes to the focused pane's PTY,
panes resize with the window, approval gates pop a dialog (approve / view
brief / reject), the task board lists delegations, and roles can be
restarted or paused per card. "Quit, keep agents working" detaches; "Stop
everything" shuts the session down. While the window is in the background,
a new approval gate or an agent blocking on input raises a native macOS
notification (posted via osascript, so macOS attributes it to Script
Editor and may ask for notification permission once).

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

## Install

```sh
brew install --cask sphragis-oss/sphragis/choragos-desktop
```

or the `.dmg` from a `desktop/v*` GitHub release. The matching CLI is
bundled inside the app; no separate install needed. The cask strips the
quarantine flag in postflight, so unsigned builds open without the
Gatekeeper detour; the raw dmg needs System Settings > Privacy &
Security > Open Anyway (until releases are notarized).

## Package

`make bundle` builds a universal (arm64 + x86_64) `build/Choragos.app`
with the matching CLI bundled at `Contents/Resources/choragos`, and
`make dmg` wraps it in `build/Choragos-<version>.dmg`. Signing is ad-hoc
by default; set `CODESIGN_IDENTITY="Developer ID Application: ..."` for
a real signature. Both call `packaging/bundle.sh`.

Releases: push a `desktop/vX.Y.Z` tag, where X.Y.Z matches the CLI
release the app is built from (the attach handshake requires the exact
version). The `desktop` workflow then builds the dmg, signs it when the
`MACOS_CERT_P12`/`MACOS_CERT_PASSWORD` secrets exist, notarizes and
staples when `APPLE_ID`/`APPLE_TEAM_ID`/`APPLE_APP_PASSWORD` also
exist, and attaches it to a GitHub release. Without secrets the dmg is
ad-hoc signed and Gatekeeper needs right-click then Open on first
launch. The same workflow smoke-builds an unsigned bundle on PRs that
touch `desktop/**`.

App icon: `packaging/choragos-icon.svg` is the source. Regenerate
`packaging/choragos.icns` with rsvg-convert, sips, and iconutil:

```sh
rsvg-convert -w 1024 -h 1024 packaging/choragos-icon.svg -o /tmp/i1024.png
mkdir -p /tmp/choragos.iconset
for s in 16 32 128 256 512; do
  sips -z "$s" "$s" /tmp/i1024.png --out "/tmp/choragos.iconset/icon_${s}x${s}.png"
  sips -z "$((s*2))" "$((s*2))" /tmp/i1024.png --out "/tmp/choragos.iconset/icon_${s}x${s}@2x.png"
done
iconutil -c icns /tmp/choragos.iconset -o packaging/choragos.icns
```

## Layout

- `main.go`, `app.go`: Wails v2 backend; the only protocol logic is
  `internal/wire` calls (imported from the parent module via `replace`).
- `frontend/dist/`: static vanilla JS/CSS, embedded at build time. No node
  toolchain; xterm.js and its fit addon are vendored under
  `frontend/dist/vendor/` (see `VENDOR.md` there).

Dev/test hook: `CHORAGOS_DESKTOP_AUTOATTACH=<dir>` attaches to that
directory's session on startup, skipping the picker.
