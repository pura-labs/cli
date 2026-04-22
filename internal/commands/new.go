// `pura new --describe "<prompt>"` — Phase 4 chat-first create.
//
// Streams /api/p/bootstrap to draft a doc, then POSTs /api/p with a
// bootstrap_thread so the doc opens in /edit with its conversational
// origin already in the thread.
//
// TTY flow:
//   1. Stream plan + content + schema to stderr (progress).
//   2. Show inferred shape / media / slug.
//   3. Prompt y/N to publish (skip with --yes).
//
// Non-TTY / --json flow:
//   The stream collects silently; we auto-publish and emit the envelope.
//   This makes `pura new --describe ... --yes --json` script-friendly.

package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pura-labs/cli/internal/api"
	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	newDescribe string
	newStarter  string
	newModel    string
	newYes      bool
	newOpen     bool
)

func resetNewFlags() {
	newDescribe = ""
	newStarter = ""
	newModel = ""
	newYes = false
	newOpen = false
}

func newNewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create a document from a natural-language description (chat-first)",
		Long: `Draft a new document from a describe prompt and an optional starter. The
server infers shape/media/title/schema; you review the draft and publish.

Examples:
  pura new --describe "launch guestbook with name + message"
  pura new --describe "compare next vs remix on 6 axes" --starter table
  pura new --describe "5 slides team standup" --starter slides --yes --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			if strings.TrimSpace(newDescribe) == "" {
				w.Error("validation", "--describe is required",
					"Pass a short description, e.g. --describe \"waitlist form for beta\".",
				)
				return errors.New("empty describe")
			}

			client := newClient(cmd, cfg)

			draft := api.BootstrapDraft{
				Describe:  strings.TrimSpace(newDescribe),
				Starter:   newStarter,
				AISummary: "Drafted.",
			}
			showProgress := w.IsTTY && !flagJSON && flagJQ == ""

			onEvent := func(e api.BootstrapSSEEvent) {
				switch e.Type {
				case "plan":
					draft.Kind = e.Kind
					draft.Substrate = e.Substrate
					if draft.Slug == "" {
						draft.Slug = e.SlugSuggestion
					}
					if showProgress {
						fmt.Fprintf(w.Err, "  ▸ inferred kind=%s substrate=%s slug=%s\n", e.Kind, e.Substrate, e.SlugSuggestion)
					}
				case "content_delta":
					draft.Content += e.Content
				case "content_final":
					draft.Content = e.Content
				case "schema":
					draft.Schema = e.Schema
					if showProgress {
						fmt.Fprintf(w.Err, "  ▸ schema drafted\n")
					}
				case "title_suggestion":
					draft.Title = e.Title
				case "usage":
					draft.PromptTokens = e.PromptTokens
					draft.CompletionTokens = e.CompletionTokens
					draft.Model = e.Model
				case "error":
					// Collect — we surface it after the stream ends.
					if showProgress {
						fmt.Fprintf(w.Err, "  ✗ %s\n", e.Message)
					}
				}
			}

			req := api.BootstrapRequest{
				Describe: draft.Describe,
				Starter:  draft.Starter,
				Model:    newModel,
			}
			if err := client.Bootstrap(req, onEvent); err != nil {
				w.Error("bootstrap_failed", err.Error(), "")
				return err
			}

			if draft.Content == "" {
				w.Error("bootstrap_empty", "Planner returned no content",
					"Retry with a more specific --describe.",
				)
				return errors.New("empty draft")
			}

			// Confirm prompt on TTY unless --yes.
			if !newYes && w.IsTTY && !flagJSON && flagJQ == "" {
				fmt.Fprintf(w.Err, "\n  ▸ title: %s\n", firstNonEmpty(draft.Title, "(none)"))
				fmt.Fprintf(w.Err, "  ▸ %d chars drafted\n  Publish? [y/N] ", len(draft.Content))
				var answer string
				_, _ = fmt.Scanln(&answer)
				a := strings.ToLower(strings.TrimSpace(answer))
				if a != "y" && a != "yes" {
					w.OK(map[string]any{
						"slug":      draft.Slug,
						"kind":      draft.Kind,
						"substrate": draft.Substrate,
						"title":     draft.Title,
						"content":   draft.Content,
						"published": false,
					}, output.WithSummary("Discarded draft"))
					return nil
				}
			}

			createReq := api.CreateRequest{
				Content:   draft.Content,
				Kind:      draft.Kind,
				Substrate: draft.Substrate,
				Title:     draft.Title,
				Slug:      draft.Slug,
				BootstrapThread: &api.BootstrapThread{
					Describe:  draft.Describe,
					AISummary: draft.AISummary,
					Model:     draft.Model,
					TokensIn:  draft.PromptTokens,
					TokensOut: draft.CompletionTokens,
				},
			}
			if draft.Kind == "sheet" && draft.Schema != nil {
				createReq.Metadata = map[string]any{"grid": map[string]any{"schema": draft.Schema}}
			}

			resp, err := client.Create(createReq)
			if err != nil {
				w.Error("publish_failed", err.Error(), "")
				return err
			}

			data := map[string]any{
				"slug":              resp.Slug,
				"token":             resp.Token,
				"url":               resp.URL,
				"raw_url":           resp.RawURL,
				"ctx_url":           resp.CtxURL,
				"kind":              resp.Kind,
				"substrate":         resp.Substrate,
				"title":             resp.Title,
				"prompt_tokens":     draft.PromptTokens,
				"completion_tokens": draft.CompletionTokens,
				"model":             draft.Model,
				"published":         true,
			}
			w.OK(data,
				output.WithSummary("Published %s (%s/%s) → %s", resp.Slug, resp.Kind, resp.Substrate, resp.URL),
				output.WithBreadcrumb("open", "pura open "+resp.Slug, "Open in browser"),
				output.WithBreadcrumb("edit", "pura chat "+resp.Slug+" \"...\"", "Iterate via chat"),
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&newDescribe, "describe", "", "Natural-language description of the doc to draft (required)")
	cmd.Flags().StringVar(&newStarter, "starter", "", "Starter chip id (blog|form|landing|table|slides|canvas)")
	cmd.Flags().StringVar(&newModel, "model", "", "Model id for the planner")
	cmd.Flags().BoolVar(&newYes, "yes", false, "Auto-publish without the TTY confirmation")
	cmd.Flags().BoolVar(&newOpen, "open", false, "Open the published doc in the browser (TTY only)")
	_ = cmd.MarkFlagRequired("describe")
	return cmd
}
