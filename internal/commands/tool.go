// `pura tool` — uniform dispatcher surface.
//
// `pura tool call <name>` is the generic escape hatch. Every registered
// Pura tool (sheet.list_rows, sheet.append_row, …) is reachable from the
// CLI through this single command — no per-tool code generation needed.
//
// Surface:
//
//   pura tool ls [--refresh]             List available tools (cache-backed)
//   pura tool inspect <name> [--refresh] Show a tool's input schema + docs
//   pura tool call <name> [--args JSON]  POST to /api/tool/:name
//                          [--dry-run]
//                          [--idempotency-key KEY]
//
// Cache: tool catalog is fetched via GET /openapi.json at the server (fast,
// no auth needed) and cached to ~/.config/pura/tools.json. Refresh with
// `pura tool ls --refresh`. The cache lets `pura tool inspect` respond
// offline once primed.

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
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pura-labs/cli/internal/api"
	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

// ─── flags ───────────────────────────────────────────────────────────────

var (
	toolCallArgs           string
	toolCallDryRun         bool
	toolCallIdempotencyKey string
	toolListRefresh        bool
	toolInspectRefresh     bool
)

func resetToolFlags() {
	toolCallArgs = ""
	toolCallDryRun = false
	toolCallIdempotencyKey = ""
	toolListRefresh = false
	toolInspectRefresh = false
}

// ─── command tree ────────────────────────────────────────────────────────

func newToolCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tool",
		Short: "Call any Pura tool via /api/tool/:name (dispatcher escape hatch)",
		Long: `Every registered Pura primitive tool is reachable here without any
per-tool CLI subcommand. The dispatcher routes every call through the same
middleware chain agents use via /mcp, so behaviour (propose-gate, audit,
idempotency, quota) is identical.

Quick look:

    pura tool ls                             # known tool catalog
    pura tool inspect sheet.append_row       # arg schema + docs
    pura tool call sheet.append_row \\
        --args '{"sheet_ref":"@alice/crm","values":{"name":"Alice"}}'

The catalog is cached after first fetch; refresh with --refresh.`,
	}
	cmd.AddCommand(
		newToolLsCmd(),
		newToolInspectCmd(),
		newToolCallCmd(),
	)
	return cmd
}

// ─── `pura tool ls` ──────────────────────────────────────────────────────

func newToolLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List available tools (cached; --refresh to force a fetch)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := newWriter()
			cfg := loadConfig()
			cat, src, err := loadToolCatalog(cmd.Context(), cfg.APIURL, toolListRefresh)
			if err != nil {
				return err
			}
			if !(flagJSON || flagJQ != "" || !w.IsTTY) {
				w.Print("  Pura tools (%s · %d)\n", src, len(cat.Tools))
				w.Print("  ────────────\n")
				for _, t := range cat.Tools {
					w.Print("  %-28s %s\n", t.Name, t.ShortDescription)
				}
				w.Print("\n")
			}
			w.OK(map[string]any{
				"source": src,
				"tools":  cat.Tools,
			})
			return nil
		},
	}
	cmd.Flags().BoolVar(&toolListRefresh, "refresh", false, "Re-fetch the catalog from the server")
	return cmd
}

// ─── `pura tool inspect` ─────────────────────────────────────────────────

func newToolInspectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inspect <name>",
		Short: "Show a tool's input schema and docs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			cat, _, err := loadToolCatalog(cmd.Context(), cfg.APIURL, toolInspectRefresh)
			if err != nil {
				return err
			}
			name := args[0]
			for _, t := range cat.Tools {
				if t.Name == name {
					if !(flagJSON || flagJQ != "" || !w.IsTTY) {
						w.Print("  %s\n", t.Name)
						w.Print("  %s\n\n", strings.Repeat("─", len(t.Name)))
						if t.ShortDescription != "" {
							w.Print("  %s\n\n", t.ShortDescription)
						}
						if t.LongDescription != "" {
							w.Print("  %s\n\n", t.LongDescription)
						}
						pretty, _ := json.MarshalIndent(t.InputSchema, "  ", "  ")
						w.Print("  Input schema:\n\n  %s\n\n", string(pretty))
					}
					w.OK(t)
					return nil
				}
			}
			return fmt.Errorf("tool %q not found — see `pura tool ls`", name)
		},
	}
	cmd.Flags().BoolVar(&toolInspectRefresh, "refresh", false, "Re-fetch the catalog before looking up")
	return cmd
}

// ─── `pura tool call` ────────────────────────────────────────────────────

func newToolCallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "call <name>",
		Short: "POST to /api/tool/:name — dispatcher escape hatch",
		Long: `Executes a registered tool by name via the Pura dispatcher.

Args are passed as a JSON object via --args. Use --dry-run to preview
(when the tool supports it). --idempotency-key forwards an Idempotency-Key
header so repeated calls with the same args replay the cached result.

The exit code maps structured dispatcher errors to CLI conventions:
  0  ok / proposal / replayed
  4  not_found
  5  validation_failed · unsupported · schema_conflict
  6  concurrent_write · pending_exists · idempotency_conflict
  7  quota_exceeded / budget_exceeded  (retry_after surfaced in stderr)
  8  internal / unknown`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runToolCall(cmd, args[0])
		},
	}
	cmd.Flags().StringVar(&toolCallArgs, "args", "", "Tool arguments as JSON object")
	cmd.Flags().BoolVar(&toolCallDryRun, "dry-run", false, "Preview without side effects (requires tool support)")
	cmd.Flags().StringVar(&toolCallIdempotencyKey, "idempotency-key", "", "Forward an Idempotency-Key header")
	return cmd
}

func runToolCall(cmd *cobra.Command, name string) error {
	w := newWriter()
	cfg := loadConfig()
	base := strings.TrimRight(cfg.APIURL, "/")
	if base == "" {
		return errors.New("api_url not set; run `pura config set api_url https://pura.so`")
	}

	// Default args to empty object so the dispatcher's validate step can
	// produce field-precise errors rather than "body not object".
	body := []byte("{}")
	if toolCallArgs != "" {
		// Sanity-parse so we fail client-side with a readable error on
		// malformed JSON (rather than a 400 from the server).
		var probe any
		if err := json.Unmarshal([]byte(toolCallArgs), &probe); err != nil {
			return fmt.Errorf("--args is not valid JSON: %w", err)
		}
		body = []byte(toolCallArgs)
	}

	url := base + "/api/tool/" + name
	if toolCallDryRun {
		url += "?dry_run=1"
	}
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
	}
	if toolCallIdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", toolCallIdempotencyKey)
	}
	req.Header.Set("X-Pura-Agent", fmt.Sprintf("pura-cli/%s (session:%d)", versionStr, os.Getpid()))

	httpC := &http.Client{Timeout: 60 * time.Second}
	resp, err := httpC.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var envelope struct {
		OK         bool   `json:"ok"`
		Result     any    `json:"result,omitempty"`
		Kind       string `json:"kind,omitempty"`
		ProposalID string `json:"proposal_id,omitempty"`
		Preview    any    `json:"preview,omitempty"`
		Replayed   bool   `json:"replayed,omitempty"`
		AuditID    any    `json:"audit_id,omitempty"`
		Error      *struct {
			Code       string `json:"code"`
			Field      any    `json:"field,omitempty"`
			Suggestion any    `json:"suggestion,omitempty"`
			RetryAfter any    `json:"retry_after,omitempty"`
		} `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("unexpected non-JSON response (HTTP %d): %s", resp.StatusCode, string(raw))
	}

	if !envelope.OK && envelope.Error != nil {
		errCode := envelope.Error.Code
		suggestion, _ := envelope.Error.Suggestion.(string)
		field, _ := envelope.Error.Field.(string)
		w.OK(envelope, output.WithSummary("tool call failed: %s", errCode))
		// Return an *api.Error so output.ExitCodeFor routes the exit code
		// through the same table `pura chat`, `pura push`, etc. use.
		msg := errCode
		if field != "" {
			msg += " (field=" + field + ")"
		}
		return &api.Error{
			Status:  resp.StatusCode,
			Code:    errCode,
			Message: msg,
			Hint:    suggestion,
		}
	}

	summary := "tool call ok"
	switch envelope.Kind {
	case "proposal":
		summary = fmt.Sprintf("proposal created: %s", envelope.ProposalID)
	default:
		if envelope.Replayed {
			summary = "tool call replayed (idempotency-key hit)"
		}
	}
	w.OK(envelope, output.WithSummary("%s", summary))
	return nil
}

// ─── catalog fetch + cache ───────────────────────────────────────────────

type toolMeta struct {
	Name             string         `json:"name"`
	ShortDescription string         `json:"short_description,omitempty"`
	LongDescription  string         `json:"long_description,omitempty"`
	Category         string         `json:"category,omitempty"`
	InputSchema      map[string]any `json:"input_schema,omitempty"`
}

type toolCatalog struct {
	FetchedAt string     `json:"fetched_at"`
	APIURL    string     `json:"api_url"`
	Tools     []toolMeta `json:"tools"`
}

func loadToolCatalog(
	ctx context.Context,
	apiURL string,
	forceRefresh bool,
) (toolCatalog, string, error) {
	path := toolCachePath()
	if !forceRefresh {
		if cat, err := readToolCache(path); err == nil && cat.APIURL == apiURL && len(cat.Tools) > 0 {
			return cat, "cache", nil
		}
	}
	cat, err := fetchToolCatalog(ctx, apiURL)
	if err != nil {
		// Fall back to stale cache on network error so `inspect` keeps
		// working offline.
		if cached, cerr := readToolCache(path); cerr == nil {
			return cached, "cache (stale · server unreachable)", nil
		}
		return toolCatalog{}, "", err
	}
	_ = writeToolCache(path, cat)
	return cat, "network", nil
}

// fetchToolCatalog uses MCP's tools/list (POST /mcp) because it returns
// descriptions + JSON-Schema inputs uniformly. /openapi.json could also
// work but its per-tool schemas live under path-specific routes.
//
// Spec-compliant: runs the full MCP handshake before tools/list so
// strict servers don't reject the request during pre-init.
// https://modelcontextprotocol.io/specification/2025-03-26/basic/lifecycle
func fetchToolCatalog(ctx context.Context, apiURL string) (toolCatalog, error) {
	if apiURL == "" {
		return toolCatalog{}, errors.New("api_url not set")
	}
	base := strings.TrimRight(apiURL, "/")
	httpC := &http.Client{Timeout: 10 * time.Second}

	// Handshake — required by MCP 2025-03-26 lifecycle.
	if _, err := doMcpHandshake(ctx, httpC, base, ""); err != nil {
		return toolCatalog{}, fmt.Errorf("mcp handshake: %w", err)
	}

	// tools/list via the shared helper (handles JSON + SSE responses).
	listResp, err := postRpc(ctx, httpC, base, "", 2, "tools/list", map[string]any{})
	if err != nil {
		return toolCatalog{}, err
	}
	// Narrow the dynamic map into our typed shape via re-marshal.
	resultBytes, _ := json.Marshal(map[string]any{"result": listResp["result"]})
	var parsed struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resultBytes, &parsed); err != nil {
		return toolCatalog{}, fmt.Errorf("decode tools/list: %w", err)
	}
	out := toolCatalog{
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		APIURL:    apiURL,
		Tools:     make([]toolMeta, 0, len(parsed.Result.Tools)),
	}
	for _, t := range parsed.Result.Tools {
		// Server concatenates short + long when long is present; split back
		// on the blank line so CLI UIs can render the short form separately.
		short, long := splitDescription(t.Description)
		out.Tools = append(out.Tools, toolMeta{
			Name:             t.Name,
			ShortDescription: short,
			LongDescription:  long,
			InputSchema:      t.InputSchema,
		})
	}
	sort.Slice(out.Tools, func(i, j int) bool { return out.Tools[i].Name < out.Tools[j].Name })
	return out, nil
}

func splitDescription(d string) (short, long string) {
	i := strings.Index(d, "\n\n")
	if i < 0 {
		return d, ""
	}
	return d[:i], d[i+2:]
}

// ─── cache file I/O (~/.config/pura/tools.json) ─────────────────────────

func toolCachePath() string {
	if p := os.Getenv("PURA_TOOL_CACHE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "pura", "tools.json")
}

func readToolCache(path string) (toolCatalog, error) {
	if path == "" {
		return toolCatalog{}, errors.New("no cache path")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return toolCatalog{}, err
	}
	var cat toolCatalog
	if err := json.Unmarshal(b, &cat); err != nil {
		return toolCatalog{}, err
	}
	return cat, nil
}

func writeToolCache(path string, cat toolCatalog) error {
	if path == "" {
		return errors.New("no cache path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Exit-code routing is handled by output.ExitCodeFor via *api.Error. The
// dispatcher's HTTP status (set by web/src/agent/errors.ts#errorCodeToHttpStatus)
// maps uniformly:
//   400 → ExitInvalid   — validation_failed · schema_conflict
//   403 → ExitForbidden — forbidden
//   404 → ExitNotFound  — not_found
//   409 → ExitConflict  — concurrent_write · pending_exists · idempotency_conflict
//   422 → ExitGeneric   — unsupported
//   429 → ExitRateLimit — quota_exceeded · budget_exceeded
//   500 → ExitAPI       — internal · unknown
