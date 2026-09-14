#!/usr/bin/env bash
# End-to-end smoke test: drives the real deck in tmux against examples/demo.toml.
set -euo pipefail
cd "$(dirname "$0")/.."

SOCK="/tmp/chor-e2e-$$.sock"
CFG="/tmp/chor-e2e-$$.toml"
METRICS="127.0.0.1:19464"
TMUX="tmux -L chor-e2e-$$"
export TERM="${TERM:-xterm-256color}"

go build -o choragos ./cmd/choragos
cleanup() { $TMUX kill-server 2>/dev/null || true; rm -f "$SOCK" "$CFG"; }
trap cleanup EXIT

# the demo team plus a metrics endpoint, so the scrape is part of the smoke
{ cat examples/demo.toml; printf '\n[metrics]\nlisten = "%s"\n' "$METRICS"; } > "$CFG"

$TMUX new-session -d -x 200 -y 50 \
  "CHORAGOS_SOCK=$SOCK ./choragos serve --config $CFG --sphragis=false; echo EXIT=\$?; sleep 30"

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

echo "-- metrics endpoint serves one state line per role"
n=0
for _ in $(seq 1 20); do
  n=$(curl -sf "http://$METRICS/metrics" | grep -c '^choragos_role_state{.*} 1$' || true)
  [ "$n" -eq 5 ] && break
  sleep 0.5
done
if [ "$n" -ne 5 ]; then
  echo "expected 5 role state lines, got $n"
  curl -s "http://$METRICS/metrics" || true
  exit 1
fi

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
