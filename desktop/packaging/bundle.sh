#!/usr/bin/env bash
# Assembles Choragos.app (CLI bundled in Resources) and optionally a dmg.
set -euo pipefail

usage() { echo "usage: VERSION=x.y.z $0 app|dmg" >&2; exit 2; }
[ "$#" -eq 1 ] || usage
cmd="$1"
[ "$cmd" = "app" ] || [ "$cmd" = "dmg" ] || usage
: "${VERSION:?set VERSION (must match the CLI release the app attaches to)}"

pkg_dir="$(cd "$(dirname "$0")" && pwd)"
desktop_dir="$(dirname "$pkg_dir")"
repo_dir="$(dirname "$desktop_dir")"
build_dir="$desktop_dir/build"
app="$build_dir/Choragos.app"
identity="${CODESIGN_IDENTITY:--}"
read -ra arches <<< "${ARCHES:-arm64 amd64}"

rm -rf "$app" "$build_dir/pkg"
mkdir -p "$build_dir/pkg"

app_slices=() cli_slices=()
for arch in "${arches[@]}"; do
  echo "building $arch" >&2
  (cd "$desktop_dir" && CGO_ENABLED=1 GOARCH="$arch" CGO_LDFLAGS="-framework UniformTypeIdentifiers" \
    go build -tags desktop,production -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "$build_dir/pkg/app-$arch" .)
  (cd "$repo_dir" && CGO_ENABLED=0 GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "$build_dir/pkg/cli-$arch" ./cmd/choragos)
  app_slices+=("$build_dir/pkg/app-$arch")
  cli_slices+=("$build_dir/pkg/cli-$arch")
done

mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
if [ "${#arches[@]}" -eq 1 ]; then
  cp "${app_slices[0]}" "$app/Contents/MacOS/choragos-desktop"
  cp "${cli_slices[0]}" "$app/Contents/Resources/choragos"
else
  lipo -create -output "$app/Contents/MacOS/choragos-desktop" "${app_slices[@]}"
  lipo -create -output "$app/Contents/Resources/choragos" "${cli_slices[@]}"
fi
sed "s/__VERSION__/$VERSION/g" "$pkg_dir/Info.plist" > "$app/Contents/Info.plist"
cp "$pkg_dir/choragos.icns" "$app/Contents/Resources/choragos.icns"

# sign inside out; hardened runtime only with a real identity (required for notarization)
sign_flags=(--force --timestamp)
[ "$identity" = "-" ] || sign_flags+=(--options runtime)
codesign "${sign_flags[@]}" -s "$identity" "$app/Contents/Resources/choragos"
codesign "${sign_flags[@]}" -s "$identity" "$app"
codesign --verify --strict "$app"
echo "built $app (version $VERSION, arches: ${arches[*]}, identity: $identity)" >&2

[ "$cmd" = "dmg" ] || exit 0
dmg="$build_dir/Choragos-$VERSION.dmg"
stage="$build_dir/pkg/dmg"
rm -f "$dmg"
mkdir -p "$stage"
cp -R "$app" "$stage/"
ln -sf /Applications "$stage/Applications"
hdiutil create -volname Choragos -srcfolder "$stage" -format UDZO -ov "$dmg" >&2
echo "built $dmg" >&2
