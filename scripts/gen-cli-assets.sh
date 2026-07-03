#!/usr/bin/env bash
# Generates shell completions and man pages for release archives.
set -euo pipefail
cd "$(dirname "$0")/.."
rm -rf completions manpages
mkdir -p completions manpages
go run ./cmd/choragos completion bash >completions/choragos.bash
go run ./cmd/choragos completion zsh >completions/_choragos
go run ./cmd/choragos completion fish >completions/choragos.fish
go run ./cmd/choragos gen-man manpages
echo "generated: $(ls completions | wc -l | tr -d ' ') completions, $(ls manpages | wc -l | tr -d ' ') man pages"
