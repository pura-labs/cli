package commands

import (
	"fmt"

	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

func newRmCmd() *cobra.Command {
	var flagYes bool

	cmd := &cobra.Command{
		Use:   "rm <slug>",
		Short: "Delete a document",
		Long:  "Permanently delete a published document.",
		Example: `  pura rm k8x2m1
  pura rm k8x2m1 --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			slug := args[0]

			if cfg.Token == "" {
				w.Error("unauthorized",
					"No token configured",
					"Sign in via device flow, or set a token directly.",
					output.WithBreadcrumb("retry", "pura auth login", "Sign in via device flow"),
				)
				return fmt.Errorf("no token")
			}

			ok, err := confirmMutation(
				w,
				flagYes,
				"--yes",
				fmt.Sprintf("Delete %s?", slug),
				"This cannot be undone.",
				"Delete",
			)
			if err != nil {
				w.Error("confirmation_required",
					"Refusing to delete without confirmation",
					"Re-run with --yes in non-interactive mode.",
				)
				return err
			}
			if !ok {
				w.Print("  Cancelled.\n")
				return nil
			}

			client := newClient(cmd, cfg)
			if err := client.Delete(slug); err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}

			w.OK(map[string]string{"slug": slug, "status": "deleted"},
				output.WithSummary("Deleted %s", slug),
				output.WithBreadcrumb("list", "pura ls", "See remaining documents"),
			)
			w.Print("  Deleted: %s\n", slug)

			return nil
		},
	}

	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Skip confirmation")
	cmd.Flags().BoolVar(&flagYes, "force", false, "Alias for --yes (deprecated)")
	_ = cmd.Flags().MarkHidden("force")

	return cmd
}
