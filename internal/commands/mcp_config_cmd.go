// `pura mcp config` — print the MCP connection JSON for a client.
//
// Three modes:
//
//   • no args, default             — generic (works with any mcpServers
//                                    client), stdio transport, current
//                                    token (from loaded config).
//   • --client=<id>                — the exact block the named client
//                                    would receive via `install`.
//   • --client=<id> --for-copy     — same block with a placeholder where
//                                    the token would go. Safe to drop
//                                    into a README without leaking the
//                                    actual token.

package commands

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	mcpConfigClient    string
	mcpConfigTransport string
	mcpConfigScope     string
	mcpConfigForCopy   bool
)

func newMcpConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config [<client>]",
		Short: "Print the MCP connection JSON for a named client",
		Long: `Prints the exact JSON block ` + "`install`" + ` would write, so you can
paste it manually into any client not covered by the registry or into
a team-wide README. Pass --for-copy to replace the token with a
placeholder.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runMcpConfig,
	}
	cmd.Flags().StringVar(&mcpConfigClient, "client", "", "Target client id")
	cmd.Flags().StringVar(&mcpConfigTransport, "transport", "auto", "auto | url | stdio")
	cmd.Flags().StringVar(&mcpConfigScope, "scope", "", "user | project (for per-client rendering)")
	cmd.Flags().BoolVar(&mcpConfigForCopy, "for-copy", false, "Replace the token with a placeholder")
	return cmd
}

func runMcpConfig(cmd *cobra.Command, args []string) error {
	w := newWriter()
	cfg := loadConfig()
	if cfg.APIURL == "" {
		return errors.New("api_url not set; run `pura config set api_url <url>`")
	}

	clientID := argOrFlag(args, mcpConfigClient)

	// Generic mode — no client specified.
	if clientID == "" {
		sb, err := buildStdioBlock(cfg.APIURL, tokenOrPlaceholder(cfg.Token), "generic")
		if err != nil {
			return err
		}
		payload := map[string]any{
			"mcpServers": map[string]any{"pura": renderStandardEntry(sb, "")},
		}
		pretty, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return err
		}
		if !(flagJSON || flagJQ != "") {
			w.Print("%s\n", pretty)
		}
		w.OK(payload,
			output.WithSummary("MCP config for: generic (stdio)"),
			output.WithBreadcrumb(
				"install",
				"pura mcp install <client>",
				"Write this entry directly into a client's config.",
			),
		)
		return nil
	}

	// Per-client mode.
	c := findClient(clientID)
	if c == nil {
		return fmt.Errorf("unknown client %q; see `pura mcp ls`", clientID)
	}
	transport, err := resolveTransport(c, mcpConfigTransport)
	if err != nil {
		return err
	}

	block, err := buildBlockForTransport(cfg.APIURL, tokenOrPlaceholder(cfg.Token), c.id, transport)
	if err != nil {
		return err
	}
	entry := c.renderEntry(block, "")
	payload := map[string]any{c.rootKey: map[string]any{"pura": entry}}
	pretty, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if !(flagJSON || flagJQ != "") {
		w.Print("%s\n", pretty)
	}
	w.OK(payload,
		output.WithSummary("MCP config for: %s (%s)", c.label, transport),
		output.WithBreadcrumb(
			"install",
			"pura mcp install "+c.id,
			"Write this entry into "+c.label+"'s config file.",
		),
	)
	return nil
}

func tokenOrPlaceholder(token string) string {
	if mcpConfigForCopy || token == "" {
		return "<PURA_API_KEY>"
	}
	return token
}
