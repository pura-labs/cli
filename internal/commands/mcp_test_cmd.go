// `pura mcp test` — probe the Pura /mcp endpoint.
//
// Three checks:
//   1. GET /mcp                 → server metadata reachable
//   2. POST initialize           → protocol handshake negotiates version
//   3. POST tools/list           → tool catalog non-empty
//
// --client=<id> reads the token from the currently-installed entry so
// the probe exercises the exact auth the client uses, not the CLI session.
// --url=<override> points at a different /mcp endpoint (e.g. staging).

package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	mcpTestClient string
	mcpTestScope  string
	mcpTestURL    string
)

func newMcpTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Probe Pura's /mcp endpoint from this machine",
		Long: `Runs three live checks against the Pura /mcp endpoint:

  1. GET  /mcp                 — server metadata reachable
  2. POST /mcp initialize      — protocol handshake
  3. POST /mcp tools/list      — tool catalog non-empty

By default uses the current CLI session token. Pass --client=<id> to use
the token from an installed client's config — useful for verifying the
install actually works end-to-end. Pass --url=<override> to probe a
different /mcp endpoint without changing the configured api_url.`,
		RunE: runMcpTest,
	}
	cmd.Flags().StringVar(&mcpTestClient, "client", "", "Use the token from an installed client's config")
	cmd.Flags().StringVar(&mcpTestScope, "scope", "", "Scope for --client lookup (user|project)")
	cmd.Flags().StringVar(&mcpTestURL, "url", "", "Override the /mcp URL (absolute URL to /mcp)")
	return cmd
}

func runMcpTest(cmd *cobra.Command, _ []string) error {
	w := newWriter()
	cfg := loadConfig()

	base := strings.TrimRight(cfg.APIURL, "/")
	token := cfg.Token

	if mcpTestURL != "" {
		base = strings.TrimSuffix(strings.TrimRight(mcpTestURL, "/"), "/mcp")
	}
	if mcpTestClient != "" {
		c := findClient(mcpTestClient)
		if c == nil {
			return fmt.Errorf("unknown client %q", mcpTestClient)
		}
		scope, err := resolveScope(c, mcpTestScope)
		if err != nil {
			return err
		}
		path, err := c.resolvePath(scope)
		if err != nil {
			return err
		}
		tree, _, err := loadConfigFile(path, c.format)
		if err != nil {
			return err
		}
		dict, ok := getServerDict(tree, pathAt(c.rootKey))
		if !ok {
			return fmt.Errorf("no %s block in %s", c.rootKey, path)
		}
		entry, ok := dict["pura"].(map[string]any)
		if !ok {
			return fmt.Errorf("no pura entry in %s", path)
		}
		// Extract token from the entry: URL transport stores it in
		// headers["Authorization"]; stdio transport stores it in
		// env["PURA_API_KEY"].
		token = ""
		if headers, ok := entry["headers"].(map[string]any); ok {
			if a, ok := headers["Authorization"].(string); ok {
				token = strings.TrimPrefix(a, "Bearer ")
			}
		}
		if env, ok := entry["env"].(map[string]any); ok {
			if k, ok := env["PURA_API_KEY"].(string); ok && token == "" {
				token = k
			}
		}
		if url, ok := entry["url"].(string); ok && mcpTestURL == "" {
			base = strings.TrimSuffix(strings.TrimRight(url, "/"), "/mcp")
		}
		if token == "" {
			return errors.New("no token found in the client's pura entry; re-install")
		}
	}

	if !cfg.HasExplicitAPIURL() && mcpTestURL == "" && mcpTestClient == "" {
		return errors.New("api_url not configured; run `pura auth login` or `pura config set api_url https://pura.so`")
	}
	if base == "" {
		return errors.New("resolved base URL is empty")
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	httpC := httpJsonClient()

	// (1) GET /mcp
	g, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/mcp", nil)
	if err != nil {
		return err
	}
	gResp, err := httpC.Do(g)
	if err != nil {
		return fmt.Errorf("GET /mcp: %w", err)
	}
	_ = gResp.Body.Close()
	if gResp.StatusCode >= 400 {
		return fmt.Errorf("GET /mcp returned HTTP %d", gResp.StatusCode)
	}

	// (2) initialize
	protoVersion, err := doMcpHandshake(ctx, httpC, base, token)
	if err != nil {
		return err
	}

	// (3) tools/list
	tools, err := listMcpTools(ctx, httpC, base, token)
	if err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	sort.Strings(tools)

	if !(flagJSON || flagJQ != "" || !w.IsTTY) {
		w.Print("  Pura MCP probe\n")
		w.Print("  ──────────────\n")
		w.Print("  ✓ GET /mcp\n")
		w.Print("  ✓ initialize      protocolVersion=%s\n", protoVersion)
		w.Print("  ✓ tools/list      %d tools\n", len(tools))
		limit := 10
		if len(tools) < limit {
			limit = len(tools)
		}
		if limit > 0 {
			w.Print("      sample: %s\n", strings.Join(tools[:limit], ", "))
		}
		w.Print("\n")
	}

	w.OK(map[string]any{
		"api_url":          base,
		"protocol_version": protoVersion,
		"tool_count":       len(tools),
		"tools":            tools,
	}, output.WithSummary("MCP probe: %d tools available", len(tools)))
	return nil
}
