package commands

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

// execCommand is a variable for testing — defaults to exec.Command.
var execCommand = exec.Command

func newOpenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "open <slug>",
		Short: "Open document in browser",
		Long:  "Open a published document in your default web browser.",
		Example: `  pura open k8x2m1
	  pura open @alice/abc123`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			client := newClient(cmd, cfg)
			slug := args[0]

			docURL := client.DocumentURL(slug)

			w.Print("  Opening: %s\n", docURL)

			if err := openInBrowser(docURL); err != nil {
				w.Error("open_error", fmt.Sprintf("Failed to open browser: %v", err), "Try opening the URL manually")
				return err
			}

			result := map[string]string{
				"slug": slug,
				"url":  docURL,
			}
			w.OK(result)
			return nil
		},
	}
}

// openInBrowser opens a URL in the default browser based on the OS.
func openInBrowser(url string) error {
	var args []string
	switch runtime.GOOS {
	case "darwin":
		args = []string{"open", url}
	case "linux":
		args = []string{"xdg-open", url}
	case "windows":
		args = []string{"rundll32", "url.dll,FileProtocolHandler", url}
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	cmd := execCommand(args[0], args[1:]...)
	return cmd.Start()
}
