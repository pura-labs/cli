package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

func newGetCmd() *cobra.Command {
	var (
		flagFormat string
		flagOutput string
	)

	cmd := &cobra.Command{
		Use:   "get <slug>",
		Short: "Get a document",
		Long:  "Retrieve a published document by its slug.",
		Example: `  pura get k8x2m1
  pura get k8x2m1 -f raw
  pura get k8x2m1 -f ctx
  pura get k8x2m1 -f raw -o report.md`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			client := newClient(cmd, cfg)
			slug := args[0]

			switch flagFormat {
			case "raw":
				content, err := client.GetRaw(slug)
				if err != nil {
					w.Error("api_error", err.Error(), "")
					return err
				}
				if flagOutput != "" {
					if err := os.WriteFile(flagOutput, []byte(content), 0644); err != nil {
						w.Error("write_error", err.Error(), "")
						return err
					}
					w.Print("  Saved to: %s\n", flagOutput)
				} else {
					switch {
					case flagQuiet:
						fmt.Fprint(w.Out, content)
					case flagJSON || flagJQ != "" || !w.IsTTY:
						w.OK(map[string]string{
							"slug":    slug,
							"format":  "raw",
							"content": content,
						})
					default:
						fmt.Fprint(w.Out, content)
					}
				}

			case "ctx":
				ctx, err := client.GetContext(slug)
				if err != nil {
					w.Error("api_error", err.Error(), "")
					return err
				}
				if flagOutput != "" {
					if err := os.WriteFile(flagOutput, ctx, 0644); err != nil {
						w.Error("write_error", err.Error(), "")
						return err
					}
					w.Print("  Saved to: %s\n", flagOutput)
				} else {
					var parsed any
					if err := json.Unmarshal(ctx, &parsed); err != nil {
						w.Error("api_error", "Invalid JSON returned from ctx endpoint", "")
						return err
					}

					switch {
					case flagQuiet:
						fmt.Fprint(w.Out, string(ctx))
					case flagJSON || flagJQ != "" || !w.IsTTY:
						w.OK(map[string]any{
							"slug":    slug,
							"format":  "ctx",
							"context": parsed,
						})
					default:
						enc := json.NewEncoder(w.Out)
						enc.SetIndent("", "  ")
						_ = enc.Encode(parsed)
					}
				}

			case "meta", "":
				doc, err := client.Get(slug)
				if err != nil {
					w.Error("api_error", err.Error(), "Check the slug is correct",
						output.WithBreadcrumb("list", "pura ls", "See your documents"),
					)
					return err
				}
				summary := fmt.Sprintf("%s (%s, updated %s)", slug, kindSubstrateLabel(doc.Kind, doc.Substrate), doc.UpdatedAt)
				w.OK(doc,
					output.WithSummary("%s", summary),
					output.WithBreadcrumb("raw", fmt.Sprintf("pura get %s -f raw", slug), "Fetch raw content"),
					output.WithBreadcrumb("view", "pura open "+slug, "Open in browser"),
					output.WithBreadcrumb("edit", fmt.Sprintf("pura chat %s \"<instruction>\"", slug), "AI-edit this doc"),
					output.WithBreadcrumb("history", "pura versions ls "+slug, "Version history"),
				)
				w.Print("  Slug:      %s\n", doc.Slug)
				w.Print("  Kind:      %s\n", doc.Kind)
				if doc.Substrate != "" {
					w.Print("  Substrate: %s\n", doc.Substrate)
				}
				if doc.Title != "" {
					w.Print("  Title:     %s\n", doc.Title)
				}
				w.Print("  Theme:     %s\n", doc.Theme)
				w.Print("  Created:   %s\n", doc.CreatedAt)
				w.Print("  Updated:   %s\n", doc.UpdatedAt)

			default:
				w.Error("validation", fmt.Sprintf("Unknown format: %s", flagFormat), "Use: raw, ctx, or meta")
				return fmt.Errorf("unknown format: %s", flagFormat)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&flagFormat, "format", "f", "", "Output format (raw|ctx|meta)")
	cmd.Flags().StringVarP(&flagOutput, "output", "o", "", "Save to file")

	return cmd
}
