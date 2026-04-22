// `pura book` — typed ergonomic wrappers over the book primitive tools
// (book.read / book.add_chapter / book.remove_chapter / book.reorder /
// book.export), plus a `create` verb that thin-wraps POST /api/p with
// kind=book so users never have to remember that empty content is legal
// for this kind.
//
// Verbs shipped:
//
//   pura book create <handle>/<slug> [--title STR] [--subtitle STR]
//       → POST /api/p (kind=book, empty content)
//   pura book read <ref>
//       → book.read  (TOC + metadata)
//   pura book add <book-ref> <child-ref>
//           [--position start|end|before|after] [--anchor <ref>]
//       → book.add_chapter
//   pura book rm <book-ref> <child-ref>
//       → book.remove_chapter
//   pura book reorder <book-ref> <ref1> <ref2> <ref3> ...
//       → book.reorder (declarative full order)
//   pura book export <book-ref> [--format markdown|json] [--out PATH]
//       → book.export (writes stdout by default)
//
// Less-common verbs (clone, set_meta) remain available as
// `pura tool call book.clone` etc.

package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pura-labs/cli/internal/api"
	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

// ─── flags ───────────────────────────────────────────────────────────────

var (
	bookCreateTitle    string
	bookCreateSubtitle string
	bookCreateAuthor   string
	bookCreateTheme    string

	bookAddPosition string
	bookAddAnchor   string

	bookExportFormat string
	bookExportOut    string
)

func resetBookFlags() {
	bookCreateTitle = ""
	bookCreateSubtitle = ""
	bookCreateAuthor = ""
	bookCreateTheme = ""
	bookAddPosition = ""
	bookAddAnchor = ""
	bookExportFormat = ""
	bookExportOut = ""
}

// ─── command tree ────────────────────────────────────────────────────────

func newBookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "book",
		Short: "Curate books — ordered collections of any item (create · add · read · rm · reorder · export)",
		Long: `A book is an ordered collection of references to other items
(doc / sheet / page / slides / canvas / image / file). Chapters keep
their own URLs and may appear in multiple books.

Book refs accept the canonical forms parsed by the dispatcher:

  @alice/manual
  pura://book/@alice/manual
  alice/manual                 (@ optional)

Anon books live under the handle ` + "`_`" + ` — e.g. ` + "`@_/notes-book`" + `.

Less-common verbs (clone, set-meta) run through ` + "`pura tool call book.<name>`" + `.`,
	}
	cmd.AddCommand(
		newBookCreateCmd(),
		newBookReadCmd(),
		newBookAddCmd(),
		newBookRmCmd(),
		newBookReorderCmd(),
		newBookExportCmd(),
	)
	return cmd
}

// ─── `pura book create <handle>/<slug>` ─────────────────────────────────

func newBookCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <handle>/<slug>",
		Short: "Create an empty book (kind=book, substrate=refs)",
		Long: `Publishes a fresh book shell. Chapters are added separately via
` + "`pura book add`" + `. The book's metadata (subtitle, author, theme)
can be set inline here or later via ` + "`pura tool call book.set_meta`" + `.

Examples:
  pura book create @_/my-notes --title "My Knowledge Base"
  pura book create @alice/faq --title FAQ --subtitle "Product answers" --author Alice`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			slug, handleOverride, err := splitRef(args[0])
			if err != nil {
				return err
			}

			client := newClient(cmd, cfg)
			if handleOverride != "" {
				client.Handle = api.NormalizeHandle(handleOverride)
			}

			metadata := map[string]any{}
			if bookCreateSubtitle != "" {
				metadata["subtitle"] = bookCreateSubtitle
			}
			if bookCreateAuthor != "" {
				metadata["author"] = bookCreateAuthor
			}

			req := api.CreateRequest{
				Content: "",
				Kind:    "book",
				Title:   bookCreateTitle,
				Slug:    slug,
				Theme:   bookCreateTheme,
			}
			if len(metadata) > 0 {
				req.Metadata = metadata
			}
			resp, err := client.Create(req)
			if err != nil {
				return err
			}

			ref := fmt.Sprintf("@%s/%s", strings.TrimPrefix(client.Handle, "@"), resp.Slug)
			if !(flagJSON || flagJQ != "" || !w.IsTTY) {
				w.Print("  created book → %s\n", ref)
				w.Print("  view:        %s\n", resp.URL)
				w.Print("  next:        pura book add %s <chapter-ref>\n\n", ref)
			}
			w.OK(resp, output.WithSummary("created %s", ref))
			return nil
		},
	}
	cmd.Flags().StringVar(&bookCreateTitle, "title", "", "Book title")
	cmd.Flags().StringVar(&bookCreateSubtitle, "subtitle", "", "Subtitle (stored in metadata)")
	cmd.Flags().StringVar(&bookCreateAuthor, "author", "", "Author byline (stored in metadata)")
	cmd.Flags().StringVar(&bookCreateTheme, "theme", "", "Theme preset")
	return cmd
}

// ─── `pura book read <ref>` ─────────────────────────────────────────────

func newBookReadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "read <ref>",
		Short: "Read a book's chapter list + metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			env, err := callBookTool(cmd.Context(), "book.read",
				map[string]any{"book_ref": args[0]},
			)
			if err != nil {
				return err
			}
			result, _ := env["result"].(map[string]any)
			title, _ := result["title"].(string)
			count, _ := result["chapter_count"].(float64)
			chapters, _ := result["chapters"].([]any)
			meta, _ := result["metadata"].(map[string]any)

			if !(flagJSON || flagJQ != "" || !w.IsTTY) {
				w.Print("  %s · %g chapter%s\n", args[0], count, pluralS(int(count)))
				if title != "" {
					w.Print("    title:    %s\n", title)
				}
				if sub, _ := meta["subtitle"].(string); sub != "" {
					w.Print("    subtitle: %s\n", sub)
				}
				if auth, _ := meta["author"].(string); auth != "" {
					w.Print("    author:   %s\n", auth)
				}
				if len(chapters) > 0 {
					w.Print("\n")
					for i, c := range chapters {
						m, _ := c.(map[string]any)
						ref, _ := m["ref"].(string)
						kind, _ := m["kind"].(string)
						ctitle, _ := m["title"].(string)
						if ctitle == "" {
							ctitle = "(untitled)"
						}
						w.Print("    %2d. [%s] %s — %s\n", i+1, kind, ref, ctitle)
					}
				}
				w.Print("\n")
			}
			w.OK(env["result"], output.WithSummary("%g chapter%s", count, pluralS(int(count))))
			return nil
		},
	}
}

// ─── `pura book add <book> <child>` ─────────────────────────────────────

func newBookAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <book-ref> <child-ref>",
		Short: "Add a chapter (ref to an existing item) to a book",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			payload := map[string]any{
				"book_ref":  args[0],
				"child_ref": args[1],
			}
			if bookAddPosition != "" {
				payload["position"] = bookAddPosition
			}
			if bookAddAnchor != "" {
				payload["anchor_ref"] = bookAddAnchor
			}
			env, err := callBookTool(cmd.Context(), "book.add_chapter", payload)
			if err != nil {
				return err
			}
			result, _ := env["result"].(map[string]any)
			count, _ := result["chapter_count"].(float64)
			pos, _ := result["position_score"].(float64)
			if !(flagJSON || flagJQ != "" || !w.IsTTY) {
				w.Print("  added → %s  (position=%g, total=%g)\n\n", args[1], pos, count)
			}
			w.OK(env["result"], output.WithSummary("added %s → %s", args[1], args[0]))
			return nil
		},
	}
	cmd.Flags().StringVar(&bookAddPosition, "position", "",
		"'start' | 'end' (default) | 'before' | 'after'")
	cmd.Flags().StringVar(&bookAddAnchor, "anchor", "",
		"Anchor chapter ref — required when --position is 'before' or 'after'")
	return cmd
}

// ─── `pura book rm <book> <child>` ──────────────────────────────────────

func newBookRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <book-ref> <child-ref>",
		Short: "Remove a chapter reference from a book (the chapter item stays)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			env, err := callBookTool(cmd.Context(), "book.remove_chapter",
				map[string]any{"book_ref": args[0], "child_ref": args[1]},
			)
			if err != nil {
				return err
			}
			result, _ := env["result"].(map[string]any)
			removed, _ := result["removed"].(bool)
			count, _ := result["chapter_count"].(float64)
			if !(flagJSON || flagJQ != "" || !w.IsTTY) {
				if removed {
					w.Print("  removed %s  (book now has %g chapter%s)\n\n",
						args[1], count, pluralS(int(count)))
				} else {
					w.Print("  %s was not in %s (nothing to remove)\n\n", args[1], args[0])
				}
			}
			w.OK(env["result"])
			return nil
		},
	}
}

// ─── `pura book reorder <book> <ref1> <ref2> ...` ───────────────────────

func newBookReorderCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reorder <book-ref> <chapter-ref>...",
		Short: "Rewrite a book's chapter order (declarative — pass the full new order)",
		Long: `Atomic reorder: the list of refs must be a permutation of the book's
current chapters. Call ` + "`pura book read <ref>`" + ` first to see the current
list.

Example:
  pura book reorder @_/manual @_/intro @_/install @_/usage`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			payload := map[string]any{
				"book_ref":           args[0],
				"ordered_child_refs": args[1:],
			}
			env, err := callBookTool(cmd.Context(), "book.reorder", payload)
			if err != nil {
				return err
			}
			result, _ := env["result"].(map[string]any)
			count, _ := result["reordered"].(float64)
			if !(flagJSON || flagJQ != "" || !w.IsTTY) {
				w.Print("  reordered %g chapter%s in %s\n\n",
					count, pluralS(int(count)), args[0])
			}
			w.OK(env["result"], output.WithSummary("reordered %g chapters", count))
			return nil
		},
	}
}

// ─── `pura book export <ref>` ───────────────────────────────────────────

func newBookExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export <ref>",
		Short: "Export a book to markdown or json",
		Long: `Concatenates all chapters into one artifact. 'markdown' inlines doc
bodies and link-placeholders non-doc chapters; 'json' returns a
structured dump.

Writes to stdout by default; supply --out to save a file.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			format := bookExportFormat
			if format == "" {
				format = "markdown"
			}
			env, err := callBookTool(cmd.Context(), "book.export",
				map[string]any{"book_ref": args[0], "format": format},
			)
			if err != nil {
				return err
			}
			result, _ := env["result"].(map[string]any)
			body, _ := result["content"].(string)
			byteCount, _ := result["bytes"].(float64)
			chapterCount, _ := result["chapter_count"].(float64)

			if bookExportOut != "" {
				if err := os.WriteFile(bookExportOut, []byte(body), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", bookExportOut, err)
				}
				w.OK(map[string]any{
					"path":          bookExportOut,
					"format":        format,
					"bytes":         byteCount,
					"chapter_count": chapterCount,
				}, output.WithSummary("wrote %g bytes to %s", byteCount, bookExportOut))
				return nil
			}

			if !(flagJSON || flagJQ != "") {
				_, _ = os.Stdout.Write([]byte(body))
				if !strings.HasSuffix(body, "\n") {
					_, _ = os.Stdout.Write([]byte("\n"))
				}
				return nil
			}
			w.OK(env["result"], output.WithSummary("%g bytes · %g chapters", byteCount, chapterCount))
			return nil
		},
	}
	cmd.Flags().StringVar(&bookExportFormat, "format", "markdown", "Export format: markdown · json")
	cmd.Flags().StringVar(&bookExportOut, "out", "", "Write output to this file instead of stdout")
	return cmd
}

// ─── helpers ────────────────────────────────────────────────────────────

// splitRef parses "@handle/slug" or "handle/slug" or "slug" into (slug, handle).
// A bare slug yields ("slug", "") — caller falls back to client.Handle.
func splitRef(ref string) (slug string, handle string, err error) {
	s := strings.TrimSpace(ref)
	if s == "" {
		return "", "", errors.New("ref is empty")
	}
	s = strings.TrimPrefix(s, "@")
	if !strings.Contains(s, "/") {
		return s, "", nil
	}
	parts := strings.SplitN(s, "/", 2)
	if parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("malformed ref %q — expected handle/slug", ref)
	}
	return parts[1], parts[0], nil
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// callBookTool mirrors callSheetTool — a local helper that POSTs to
// /api/tool/<name> and maps dispatcher error envelopes to api.Error so
// exit codes stay consistent across the CLI.
func callBookTool(
	ctx context.Context,
	name string,
	payload map[string]any,
) (map[string]any, error) {
	cfg := loadConfig()
	base := strings.TrimRight(cfg.APIURL, "/")
	if base == "" {
		return nil, errors.New("api_url not set; run `pura config set api_url https://pura.so`")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode args: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/tool/"+name, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	req.Header.Set("X-Pura-Agent", fmt.Sprintf("pura-cli/%s (session:%d)", versionStr, os.Getpid()))

	httpC := &http.Client{Timeout: 60 * time.Second}
	resp, err := httpC.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", name, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("malformed response (HTTP %d): %s", resp.StatusCode, string(raw))
	}
	if ok, _ := env["ok"].(bool); !ok {
		errObj, _ := env["error"].(map[string]any)
		code, _ := errObj["code"].(string)
		sugg, _ := errObj["suggestion"].(string)
		field, _ := errObj["field"].(string)
		msg := code
		if field != "" {
			msg += " (field=" + field + ")"
		}
		return nil, &api.Error{
			Status:  resp.StatusCode,
			Code:    code,
			Message: msg,
			Hint:    sugg,
		}
	}
	return env, nil
}
