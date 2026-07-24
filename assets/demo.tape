# VHS tape for the README demo GIF. Render with: vhs assets/demo.tape
# Requires: brew install vhs, and a prepared workspace (see comments below).
#
# Workspace prep (throwaway dir, shims instead of real agents):
#   WS=/tmp/chor-demo-ws; rm -rf $WS; mkdir -p $WS/bin
#   go build -o $WS/bin/choragos ./cmd/choragos
#   for c in claude agy; do printf '#!/bin/sh\nprintf "agent ready\\n"; exec cat\n' > $WS/bin/$c; chmod +x $WS/bin/$c; done
#   cd $WS; PATH=$WS/bin:$PATH choragos init --template defects-flow
#   printf 'open: BUG-1 login crashes on empty password\n' > defects.md

Output assets/demo.gif
Set FontSize 14
Set Width 1600
Set Height 900
Set Theme "Catppuccin Mocha"

Hide
Type "cd /tmp/chor-demo-ws && export PATH=/tmp/chor-demo-ws/bin:$PATH && clear"
Enter
# queue the story: the coder gets a bug, fixes it, and closes it in the QA-owned ledger
Type "(sleep 14; choragos delegate --to coder --task 'Fix BUG-1: login crashes on empty password'; sleep 6; echo 'closed: BUG-1' > defects.md; choragos work-done --id T1 --task 'Fixed and closed BUG-1') >/dev/null 2>&1 &"
Enter
Show

Type "choragos serve --sphragis=false"
Enter
Sleep 6s

# tile the team while the crew boots
Ctrl+B
Type "v"
Sleep 2s
Ctrl+B
Type "h"
Sleep 2s

# the delegation lands on the coder
Sleep 5s

# the coder closes its own bug in defects.md; the ownership gate catches it
Sleep 7s
Sleep 4s

# the human rules: accept the result; the orchestrator hears the ruling
Type "y"
Sleep 3.5s

# task board: delegate, work-done, and the gated outcome
Ctrl+B
Type "t"
Sleep 4s
Type "q"
Sleep 1.5s

Ctrl+Q
Sleep 2s
