# VHS tape for the README demo GIF. Render with: vhs assets/demo.tape
# Requires: brew install vhs && make build

Output assets/demo.gif
Set FontSize 14
Set Width 1600
Set Height 900
Set Theme "Catppuccin Mocha"

# queue a delegation that arrives mid-demo; the reviewer role is gated (approve = true)
Hide
Type "(sleep 12; ./choragos delegate --to reviewer --task 'T1: review the auth middleware diff') >/dev/null 2>&1 &"
Enter
Show

Type "./choragos serve --config examples/demo.toml --sphragis=false"
Enter
Sleep 5s

# tile the team: split twice, walk focus
Ctrl+B
Type "v"
Sleep 2s
Ctrl+B
Type "-"
Sleep 2s
Ctrl+B
Type "h"
Sleep 2s

# the delegation lands gated: approval overlay, approve it
Sleep 4s
Type "y"
Sleep 3s

# task board shows the delivered task
Ctrl+B
Type "t"
Sleep 3.5s
Type "q"
Sleep 1.5s

Ctrl+Q
Sleep 2s
