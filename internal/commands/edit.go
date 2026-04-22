package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/pura-labs/cli/internal/api"
	"github.com/spf13/cobra"
)

func newEditCmd() *cobra.Command {
	var (
		flagFile  string
		flagStdin bool
		flagTitle string
		flagTheme string
	)

	cmd := &cobra.Command{
		Use:   "edit <slug>",
		Short: "Update a document",
		Long:  "Modify the content or metadata of a published document.",
		Example: `  pura edit k8x2m1 --file updated.md
	  echo "# New content" | pura edit k8x2m1 --stdin
	  pura edit k8x2m1 --title "New Title"
	  pura edit @alice/k8x2m1 --theme paper`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			client := newClient(cmd, cfg)
			slug := args[0]

			if cfg.Token == "" {
				w.Error("unauthorized", "No token configured", "Run: pura config set token <your_token>")
				return fmt.Errorf("no token")
			}

			req := api.UpdateRequest{
				Title: flagTitle,
				Theme: flagTheme,
			}

			if flagStdin {
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					w.Error("read_error", "Failed to read stdin", "")
					return err
				}
				req.Content = string(data)
			} else if flagFile != "" {
				data, err := os.ReadFile(flagFile)
				if err != nil {
					w.Error("read_error", fmt.Sprintf("Cannot read file: %s", flagFile), "")
					return err
				}
				req.Content = string(data)
			}

			if req.Content == "" && req.Title == "" && req.Theme == "" {
				w.Error("validation", "Nothing to update", "Use --file, --stdin, --title, or --theme")
				return fmt.Errorf("nothing to update")
			}

			doc, err := client.Update(slug, req)
			if err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}

			w.OK(doc)
			w.Print("  Updated:   %s\n", client.DocumentURL(slug))
			w.Print("  Kind:      %s\n", doc.Kind)
			if doc.Substrate != "" {
				w.Print("  Substrate: %s\n", doc.Substrate)
			}
			if doc.Title != "" {
				w.Print("  Title:     %s\n", doc.Title)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&flagFile, "file", "", "Replace content from file")
	cmd.Flags().BoolVar(&flagStdin, "stdin", false, "Replace content from stdin")
	cmd.Flags().StringVar(&flagTitle, "title", "", "Update title")
	cmd.Flags().StringVar(&flagTheme, "theme", "", "Update theme")

	return cmd
}
