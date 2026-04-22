package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/pura-labs/cli/internal/commands"
	"github.com/pura-labs/cli/internal/output"
)

// Set by goreleaser via -ldflags
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	commands.SetVersion(version, commit, date)
	// Bind Ctrl-C / SIGTERM into a context so HTTP requests and long-running
	// polls (device-flow, chat SSE, events --follow) cancel promptly instead
	// of blocking until their next timeout.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Exit code is derived from the typed api.Error (when present), so
	// scripts can branch on the failure class: 2=auth, 3=forbidden,
	// 4=not found, 5=validation, 6=conflict, 7=rate-limited, 8=server/
	// network. Unclassified failures fall back to 1.
	os.Exit(output.ExitCodeFor(commands.ExecuteContext(ctx)))
}
