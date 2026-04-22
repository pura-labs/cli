package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newPresentCmd opens a Pura slides deck in presentation mode in the
// user's default browser. `?present=1` flips the reader-mode long-scroll
// into the full-screen canvas + keyboard-nav present mode.
func newPresentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "present <slug>",
		Short: "Open a slides deck in presentation mode",
		Long: `Open a Pura slides deck in its browser presentation mode.
Keyboard navigation: ←/→ Space PgUp/PgDn Home/End  ·  F fullscreen  ·
P speaker notes toggle  ·  Esc exit.`,
		Example: `  pura present @alice/pitch
  pura present pitch-short`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			client := newClient(cmd, cfg)
			slug := args[0]

			presentURL := client.DocumentURL(slug) + "?present=1"

			w.Print("  Opening present mode: %s\n", presentURL)

			if err := openInBrowser(presentURL); err != nil {
				w.Error("open_error", fmt.Sprintf("Failed to open browser: %v", err), "Try opening the URL manually")
				return err
			}

			result := map[string]string{
				"slug": slug,
				"url":  presentURL,
				"mode": "present",
			}
			w.OK(result)
			return nil
		},
	}
}
