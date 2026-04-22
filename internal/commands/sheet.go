// `pura sheet` — typed ergonomic wrappers over the most common sheet
// tool calls. Reads are where humans benefit most from first-class
// flags + positional args; mutations remain available as `pura tool call
// sheet.<name>` for the full 11-tool surface.
//
// Verbs shipped:
//
//   pura sheet ls <ref> [--limit N] [--cursor C] [--json]
//       → sheet.list_rows
//   pura sheet schema <ref>
//       → sheet.get_schema
//   pura sheet export <ref> [--format csv|json|xlsx] [--out PATH]
//       → sheet.export (writes to stdout by default; decodes xlsx base64
//       → when --out is given)
//   pura sheet clone <ref> [--to SLUG] [--to-handle HANDLE]
//       → sheet.clone
//
// Each subcommand builds the args object, delegates to the shared
// `runToolCall` helper, then extracts the piece of the envelope it cares
// about for human-readable output. Errors flow through *api.Error so exit
// codes are consistent with `pura tool call` / `pura chat` etc.

package commands

import (
	"bytes"
	"context"
	"encoding/base64"
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
	sheetLsLimit       int
	sheetLsCursor      string
	sheetExportFormat  string
	sheetExportOut     string
	sheetCloneTo       string
	sheetCloneToHandle string
)

func resetSheetFlags() {
	sheetLsLimit = 0
	sheetLsCursor = ""
	sheetExportFormat = ""
	sheetExportOut = ""
	sheetCloneTo = ""
	sheetCloneToHandle = ""
}

// ─── command tree ────────────────────────────────────────────────────────

func newSheetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sheet",
		Short: "Read sheets by reference (ls · schema · export · clone)",
		Long: `Ergonomic wrappers around the most common sheet tool reads.

For every tool beyond these (append_row, patch_row, alter_schema, …) use
` + "`pura tool call sheet.<name>`" + ` — every registered Pura tool is reachable
through that escape hatch with the same auth + exit-code semantics.

Sheet references accept the canonical forms parsed by the dispatcher:

  @alice/leads
  pura://sheet/@alice/leads
  alice/leads                  (@ optional)

Anon sheets live under the handle ` + "`_`" + ` — e.g. ` + "`@_/shared-tracker`" + `.`,
	}
	cmd.AddCommand(
		newSheetLsCmd(),
		newSheetSchemaCmd(),
		newSheetExportCmd(),
		newSheetCloneCmd(),
	)
	return cmd
}

// ─── `pura sheet ls <ref>` ──────────────────────────────────────────────

func newSheetLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls <ref>",
		Short: "List rows from a sheet",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			payload := map[string]any{"sheet_ref": args[0]}
			if sheetLsLimit > 0 {
				payload["limit"] = sheetLsLimit
			}
			if sheetLsCursor != "" {
				payload["cursor"] = sheetLsCursor
			}
			env, err := callSheetTool(cmd.Context(), "sheet.list_rows", payload)
			if err != nil {
				return err
			}
			result, _ := env["result"].(map[string]any)
			rows, _ := result["rows"].([]any)
			total, _ := result["total"].(float64)
			nextCursor, _ := result["next_cursor"].(string)

			if !(flagJSON || flagJQ != "" || !w.IsTTY) {
				w.Print("  %s · %d/%g rows\n", args[0], len(rows), total)
				if len(rows) > 0 {
					// Render first few key/value pairs per row. Keeps it
					// simple — users wanting Lyra Table UX use the web.
					for _, r := range rows {
						m, _ := r.(map[string]any)
						id, _ := m["_id"].(string)
						if id == "" {
							id = "(no _id)"
						}
						w.Print("    %s", id[:minInt(12, len(id))])
						if rest := pickDisplayKeys(m, 3); rest != "" {
							w.Print("  %s", rest)
						}
						w.Print("\n")
					}
				}
				if nextCursor != "" {
					w.Print("  next-cursor: %s\n", nextCursor)
				}
				w.Print("\n")
			}
			w.OK(env["result"], output.WithSummary("%d rows listed", len(rows)))
			return nil
		},
	}
	cmd.Flags().IntVar(&sheetLsLimit, "limit", 0, "Max rows to return (default: server default 50)")
	cmd.Flags().StringVar(&sheetLsCursor, "cursor", "", "Cursor (row _id) to resume listing after")
	return cmd
}

// ─── `pura sheet schema <ref>` ──────────────────────────────────────────

func newSheetSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema <ref>",
		Short: "Show a sheet's column schema and version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			env, err := callSheetTool(cmd.Context(), "sheet.get_schema",
				map[string]any{"sheet_ref": args[0]},
			)
			if err != nil {
				return err
			}
			result, _ := env["result"].(map[string]any)
			schema, _ := result["schema"].([]any)
			sver, _ := result["schema_version"].(float64)
			dver, _ := result["doc_version"].(float64)

			if !(flagJSON || flagJQ != "" || !w.IsTTY) {
				w.Print("  %s · schema_version=%g · doc_version=%g\n", args[0], sver, dver)
				if len(schema) == 0 {
					w.Print("  (no schema set)\n\n")
				} else {
					w.Print("\n")
					for _, f := range schema {
						m, _ := f.(map[string]any)
						name, _ := m["name"].(string)
						kind, _ := m["type"].(string)
						req := ""
						if r, ok := m["required"].(bool); ok && r {
							req = "  required"
						}
						w.Print("    %-20s %-10s%s\n", name, kind, req)
					}
					w.Print("\n")
				}
			}
			w.OK(env["result"])
			return nil
		},
	}
}

// ─── `pura sheet export <ref>` ──────────────────────────────────────────

func newSheetExportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "export <ref>",
		Short: "Serialize a sheet to csv / json / xlsx",
		Long: `Calls sheet.export and writes the body to stdout or --out.

For --format csv|json the body is written as UTF-8 text.
For --format xlsx the body is base64-decoded into binary bytes.
Without --out, xlsx is NOT written to stdout (binary + TTY don't mix); use
--out to save the .xlsx file.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			format := sheetExportFormat
			if format == "" {
				format = "csv"
			}
			env, err := callSheetTool(cmd.Context(), "sheet.export",
				map[string]any{"sheet_ref": args[0], "format": format},
			)
			if err != nil {
				return err
			}
			result, _ := env["result"].(map[string]any)
			body, _ := result["body"].(string)
			encoding, _ := result["encoding"].(string)
			mime, _ := result["mime"].(string)
			filename, _ := result["filename"].(string)

			var payload []byte
			switch encoding {
			case "base64":
				decoded, derr := base64.StdEncoding.DecodeString(body)
				if derr != nil {
					return fmt.Errorf("decode base64 body: %w", derr)
				}
				payload = decoded
			default: // utf8 or unset
				payload = []byte(body)
			}

			if sheetExportOut != "" {
				if err := os.WriteFile(sheetExportOut, payload, 0o644); err != nil {
					return fmt.Errorf("write %s: %w", sheetExportOut, err)
				}
				w.OK(map[string]any{
					"path":     sheetExportOut,
					"mime":     mime,
					"filename": filename,
					"bytes":    len(payload),
				}, output.WithSummary("Wrote %d bytes to %s", len(payload), sheetExportOut))
				return nil
			}

			// Binary to stdout is dangerous — refuse with a hint.
			if encoding == "base64" {
				return errors.New("binary export (xlsx); supply --out <path> to write a file")
			}

			// Text to stdout — when --json or non-TTY we let the normal
			// envelope carry it; when TTY, print the text directly so
			// `pura sheet export foo --format csv > data.csv` does what
			// users expect.
			if !(flagJSON || flagJQ != "") {
				_, _ = os.Stdout.Write(payload)
				return nil
			}
			w.OK(env["result"], output.WithSummary("%d bytes · %s", len(payload), mime))
			return nil
		},
	}
	cmd.Flags().StringVar(&sheetExportFormat, "format", "csv", "Export format: csv · json · xlsx")
	cmd.Flags().StringVar(&sheetExportOut, "out", "", "Write output to this file instead of stdout")
	return cmd
}

// ─── `pura sheet clone <ref>` ───────────────────────────────────────────

func newSheetCloneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clone <ref>",
		Short: "Duplicate a sheet into a new doc",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			payload := map[string]any{"sheet_ref": args[0]}
			if sheetCloneTo != "" {
				payload["target_slug"] = sheetCloneTo
			}
			if sheetCloneToHandle != "" {
				payload["target_handle"] = sheetCloneToHandle
			}
			env, err := callSheetTool(cmd.Context(), "sheet.clone", payload)
			if err != nil {
				return err
			}
			result, _ := env["result"].(map[string]any)
			handle, _ := result["handle"].(string)
			slug, _ := result["slug"].(string)
			docID, _ := result["doc_id"].(string)

			ref := fmt.Sprintf("@%s/%s", handle, slug)
			if !(flagJSON || flagJQ != "" || !w.IsTTY) {
				w.Print("  cloned → %s  (doc_id=%s)\n\n", ref, docID)
			}
			w.OK(env["result"], output.WithSummary("cloned to %s", ref))
			return nil
		},
	}
	cmd.Flags().StringVar(&sheetCloneTo, "to", "", "Target slug (default: auto-assigned)")
	cmd.Flags().StringVar(&sheetCloneToHandle, "to-handle", "", "Target handle (default: same as source)")
	return cmd
}

// ─── shared dispatcher call ─────────────────────────────────────────────

// callSheetTool POSTs to /api/tool/<name> and returns the parsed envelope.
// On dispatcher failure it returns an *api.Error so output.ExitCodeFor
// routes the exit code through the same table as `pura tool call`. This
// helper is intentionally local — duplication is cheap, a shared "toolkit"
// abstraction is premature until a third primitive joins sheet.
func callSheetTool(
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

// ─── tiny helpers (local to this file) ──────────────────────────────────

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// pickDisplayKeys joins up to `n` non-_id keys from row as "k=v".
// Order is deterministic (header insertion order isn't available here;
// we sort for consistency).
func pickDisplayKeys(row map[string]any, n int) string {
	keys := make([]string, 0, len(row))
	for k := range row {
		if k == "_id" {
			continue
		}
		keys = append(keys, k)
	}
	sortStrings(keys)
	var parts []string
	for _, k := range keys {
		if len(parts) >= n {
			break
		}
		parts = append(parts, fmt.Sprintf("%s=%v", k, row[k]))
	}
	return strings.Join(parts, " ")
}

// Local sort so we don't pull in sort package repeatedly.
func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j-1] > ss[j]; j-- {
			ss[j-1], ss[j] = ss[j], ss[j-1]
		}
	}
}
