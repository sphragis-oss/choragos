# VHS tape for the README demo GIF. Render with: vhs assets/demo.tape
# Requires: brew install vhs && make build

Output assets/demo.gif
Set FontSize 14
Set Width 1600
Set Height 900
Set Theme "Catppuccin Mocha"

Type "./choragos serve --config examples/demo.toml --sphragis=false"
Enter
Sleep 4s

# split twice, walk focus, zoom
Ctrl+B
Type "v"
Sleep 1.5s
Ctrl+B
Type "-"
Sleep 1.5s
Ctrl+B
Type "h"
Sleep 1s
Ctrl+B
Type "z"
Sleep 1.5s
Ctrl+B
Type "z"
Sleep 1s

# resize, help overlay, quit
Ctrl+B
Type "r"
Sleep 500ms
Type "h"
Sleep 300ms
Type "h"
Sleep 300ms
Escape
Sleep 500ms
Ctrl+B
Type "?"
Sleep 2.5s
Type "q"
Sleep 1s
Ctrl+Q
Sleep 2s
