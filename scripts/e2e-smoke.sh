#!/usr/bin/env bash
# End-to-end smoke test: drives the real deck in tmux against examples/demo.toml.
set -euo pipefail
cd "$(dirname "$0")/.."

SOCK="/tmp/chor-e2e-$$.sock"
TMUX="tmux -L chor-e2e-$$"
export TERM="${TERM:-xterm-256color}"

go build -o choragos ./cmd/choragos
cleanup() { $TMUX kill-server 2>/dev/null || true; rm -f "$SOCK"; }
trap cleanup EXIT

$TMUX new-session -d -x 200 -y 50 \
  "CHORAGOS_SOCK=$SOCK ./choragos serve --config examples/demo.toml --sphragis=false; echo EXIT=\$?; sleep 30"

capture() { $TMUX capture-pane -p; }
wait_for() { # pattern [timeout-seconds]
  local pat="$1" t="${2:-30}" i
  for ((i = 0; i < t * 2; i++)); do
    capture | grep -q "$pat" && return 0
    sleep 0.5
  done
  echo "TIMEOUT waiting for: $pat"
  capture
  return 1
}
send() { $TMUX send-keys "$@"; sleep 0.6; }

echo "-- deck boots with status cards"
wait_for "1 orchestrator"
wait_for "ctrl+b wm"

echo "-- help overlay opens and closes"
send C-b
send '?'
wait_for "press any key to close" 5
send q

echo "-- delegate over IPC lands on the task board"
CHORAGOS_SOCK="$SOCK" ./choragos delegate --to coder --task "e2e smoke task"
sleep 1
send C-b
send t
wait_for "delegate → coder" 5
send q

echo "-- split + broadcast reaches every pane"
send C-b
send v
send C-b
send a
send "E2E-MARKER"
wait_for "E2E-MARKER" 10
n=$(capture | grep -c "E2E-MARKER" || true)
if [ "$n" -lt 2 ]; then
  echo "broadcast marker seen only $n time(s)"
  capture
  exit 1
fi

echo "-- graceful quit"
send C-q
wait_for "EXIT=0" 15
echo "e2e smoke: OK"
