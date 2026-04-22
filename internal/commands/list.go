package commands

import (
	"fmt"
	"strings"

	"github.com/pura-labs/cli/internal/api"
	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List your documents",
		Long:    "List all documents associated with your token.",
		Example: `  pura ls
  pura ls --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()

			if cfg.Token == "" {
				w.Error("unauthorized",
					"No token configured",
					"Sign in via device flow, or set a token directly.",
					output.WithBreadcrumb("retry", "pura auth login", "Sign in via device flow"),
				)
				return fmt.Errorf("no token")
			}

			client := newClient(cmd, cfg)
			var (
				items []api.DocListItem
				err   error
			)
			if strings.HasPrefix(cfg.Token, "sk_pura_") {
				items, err = client.ListForUser()
			} else {
				items, err = client.List(cfg.Token)
			}
			if err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}

			if len(items) == 0 {
				w.OK(items,
					output.WithSummary("No documents yet"),
					output.WithBreadcrumb("publish", "pura push <file>", "Publish your first document"),
				)
				w.Print("  No documents found.\n")
				return nil
			}

			// Offer the most-recent doc as the natural next-target for edit/view.
			recent := items[0].Slug
			w.OK(items,
				output.WithSummary("%d document(s)", len(items)),
				output.WithBreadcrumb("view", "pura get "+recent, "Show details for the most-recent doc"),
				output.WithBreadcrumb("open", "pura open "+recent, "Open it in a browser"),
				output.WithBreadcrumb("publish", "pura push <file>", "Publish another document"),
			)
			w.Print("  %-10s %-16s %-30s %s\n", "SLUG", "KIND/SUBSTRATE", "TITLE", "CREATED")
			w.Print("  %-10s %-16s %-30s %s\n", "────", "──────────────", "─────", "───────")
			for _, item := range items {
				kind := kindSubstrateLabel(item.Kind, item.Substrate)
				title := item.Title
				if title == "" {
					title = "(untitled)"
				}
				if len(title) > 28 {
					title = title[:28] + "…"
				}
				created := item.CreatedAt
				if len(created) >= 10 {
					created = created[:10]
				}
				if created == "" {
					created = "-"
				}
				w.Print("  %-10s %-16s %-30s %s\n", item.Slug, kind, title, created)
			}
			w.Print("\n  %d documents\n", len(items))

			return nil
		},
	}

	return cmd
}
