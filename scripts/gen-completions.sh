#!/usr/bin/env sh
# Generate shell-completion scripts for bundling in release archives.
#
# Invoked from goreleaser's before-hooks (and from `make completions`).
# We use the CLI's own `completion` subcommand (Cobra-provided), so the
# scripts are guaranteed to match the current flag set.
#
# The output directory (./completions) is gitignored — these files are
# build artifacts, regenerated on every release.

set -eu

DIR="completions"
BINARY=".bin-for-completions"

mkdir -p "$DIR"

# Build a throwaway binary into the repo root so we never race with a
# parallel `make build` that might be writing to ./pura.
go build -o "$BINARY" ./cmd/pura

trap 'rm -f "$BINARY"' EXIT INT TERM

"./$BINARY" completion bash > "$DIR/pura.bash"
"./$BINARY" completion zsh  > "$DIR/pura.zsh"
"./$BINARY" completion fish > "$DIR/pura.fish"

echo "Wrote:"
ls -1 "$DIR"
