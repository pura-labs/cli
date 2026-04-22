package commands

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/pura-labs/cli/internal/api"
	"github.com/pura-labs/cli/internal/auth"
	"github.com/pura-labs/cli/internal/config"
	"github.com/pura-labs/cli/internal/detect"
	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

func newPushCmd() *cobra.Command {
	var (
		flagTitle     string
		flagSubstrate string
		flagKind      string
		flagTheme     string
		flagStdin     bool
		flagOpen      bool
	)

	cmd := &cobra.Command{
		Use:   "push [file]",
		Short: "Publish a document",
		Long:  "Create and publish a new document. Reads from file or stdin.",
		Example: `  pura push report.md
  pura push data.csv --title "Q1 Data"
  echo "# Hello" | pura push --stdin
  cat api.json | pura push --stdin --substrate json
  pura push rows.csv --kind sheet`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()

			var content string
			var filename string

			if flagStdin || len(args) == 0 {
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					w.Error("read_error", "Failed to read stdin", "")
					return err
				}
				content = string(data)
			} else {
				filename = args[0]
				data, err := os.ReadFile(filename)
				if err != nil {
					w.Error("read_error", fmt.Sprintf("Cannot read file: %s", filename), "Check the file path")
					return err
				}
				content = string(data)
			}

			content = strings.TrimSpace(content)
			if len(content) == 0 {
				w.Error("validation", "Content is empty", "Provide non-empty content")
				return fmt.Errorf("empty content")
			}

			// --substrate wins over auto-detect; --kind is a separate signal
			// the server uses to pick the in-family substrate when
			// --substrate is absent. detect.Type() returns a substrate string
			// by filename+content.
			docSubstrate := flagSubstrate
			if docSubstrate == "" && flagKind == "" {
				docSubstrate = detect.Type(filename, content)
			}

			client := newClient(cmd, cfg)
			resp, err := client.Create(api.CreateRequest{
				Content:   content,
				Kind:      flagKind,
				Substrate: docSubstrate,
				Title:     flagTitle,
				Theme:     flagTheme,
			})
			if err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}

			publishedURL := resp.URL
			if publishedURL == "" {
				publishedURL = client.DocumentURL(resp.Slug)
			}

			// Auto-save token if not already configured (anon publish flow).
			if cfg.Token == "" {
				if err := auth.NewStore().SetToken(resolvedProfile(cfg), resp.Token); err != nil {
					fmt.Fprintf(w.Err, "Warning: failed to save token: %v\n", err)
				}
			}
			if flagHandle == "" && cfg.Handle == "" {
				if handle, ok := api.HandleFromURL(publishedURL); ok && handle != api.AnonymousHandle {
					if err := config.Set("handle", handle); err != nil {
						fmt.Fprintf(w.Err, "Warning: failed to save handle: %v\n", err)
					}
				}
			}

			// Breadcrumbs: the three most likely next actions for a just-
			// published doc. Agents can pick the intent; humans see a subtle
			// stderr footer.
			w.OK(resp,
				output.WithSummary("Published %s (%s)", publishedURL, kindSubstrateLabel(resp.Kind, resp.Substrate)),
				output.WithBreadcrumb("view", "pura open "+resp.Slug, "Open in browser"),
				output.WithBreadcrumb("edit", fmt.Sprintf("pura chat %s \"<instruction>\"", resp.Slug), "AI-edit this doc"),
				output.WithBreadcrumb("history", "pura versions ls "+resp.Slug, "See version history"),
			)
			w.Print("  Published: %s\n", publishedURL)
			w.Print("  Token:     %s  (save this to edit/delete)\n", resp.Token)
			w.Print("  Kind:      %s\n", resp.Kind)
			if resp.Substrate != "" {
				w.Print("  Substrate: %s\n", resp.Substrate)
			}
			if resp.Title != "" {
				w.Print("  Title:     %s\n", resp.Title)
			}

			if flagOpen {
				openBrowser(publishedURL)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&flagTitle, "title", "t", "", "Document title")
	cmd.Flags().StringVar(&flagSubstrate, "substrate", "", "Wire format (markdown|html|csv|json|svg|canvas|ascii) — auto-detected from content when omitted")
	cmd.Flags().StringVar(&flagKind, "kind", "", "Primitive kind (doc|sheet|page|slides|canvas|image|file|book) — overrides the kind derived from --substrate")
	cmd.Flags().StringVar(&flagTheme, "theme", "", "Theme preset")
	cmd.Flags().BoolVar(&flagStdin, "stdin", false, "Read content from stdin")
	cmd.Flags().BoolVarP(&flagOpen, "open", "o", false, "Open in browser after push")

	return cmd
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	}
	if cmd != nil {
		_ = cmd.Start()
	}
}

func absPath(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	wd, _ := os.Getwd()
	return filepath.Join(wd, name)
}
