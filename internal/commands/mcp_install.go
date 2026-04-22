// `pura mcp install` — wire a client's config file to Pura.
//
// Flow (also documented in web/PLAN-mcp-install.md §3.7):
//
//  1. resolve client + scope + transport
//  2. connectivity preflight (HEAD /mcp)
//  3. create scoped API key (origin = "mcp:<client>")
//       defer: if anything below fails, revoke that key
//  4. read existing config file (format-aware, preserves comments)
//  5. build server block for the chosen transport + render per-client entry
//  6. diff against any existing "pura" entry → short-circuit if identical
//     (and revoke the just-created key), prompt if different, respect --yes
//  7. atomic write: backup → tmp → rename
//  8. handshake test against the just-written entry
//  9. on step 7 or 8 failure: restore backup + revoke key + non-zero exit

package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	mcpInstallClient      string
	mcpInstallScope       string
	mcpInstallTransport   string
	mcpInstallName        string
	mcpInstallPermissions []string
	mcpInstallYes         bool
	mcpInstallPrint       bool
)

func newMcpInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install <client>",
		Short: "Wire an MCP client's config to Pura (creates a scoped key)",
		Long: `Write a "pura" entry into the target client's config file. The entry
carries a freshly-minted scoped API key — not your session token — so
rotating your session never silently breaks the install, and
"pura mcp uninstall" can cleanly revoke just this entry.

Supported clients ("pura mcp ls" for details): claude-desktop,
claude-code, cursor, vscode, windsurf, zed, opencode, codex, goose,
gemini-cli.

Behavior:

  --transport=auto (default) picks URL for dev-tool clients (Claude Code,
    Cursor, VS Code, OpenCode, Gemini CLI) and stdio for Electron desktop
    apps (Claude Desktop, Windsurf, Zed) and format-specific clients
    (Codex TOML, Goose YAML). Override with --transport=url|stdio.
  --scope=user (default) writes to the user-wide config. --scope=project
    writes to a cwd-local config (supported by claude-code, cursor,
    vscode, opencode, gemini-cli).
  --yes overwrites an existing pura entry without prompting.
  --print prints the computed diff without writing or creating a key.

Failure modes (rollback guaranteed):

  Config file unwritable     -> no key created, no file change.
  Handshake test fails       -> backup restored, key revoked, exit 2.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runMcpInstall,
	}
	cmd.Flags().StringVar(&mcpInstallClient, "client", "", "Target client id (alternative to positional arg)")
	cmd.Flags().StringVar(&mcpInstallScope, "scope", "", "user | project (client-dependent default)")
	cmd.Flags().StringVar(&mcpInstallTransport, "transport", "auto", "auto | url | stdio")
	cmd.Flags().StringVar(&mcpInstallName, "name", "", "Key name (default: <client>@<host>)")
	cmd.Flags().StringSliceVar(&mcpInstallPermissions, "permissions", nil, "Key scopes (default: docs:read,docs:write)")
	cmd.Flags().BoolVarP(&mcpInstallYes, "yes", "y", false, "Skip diff confirmation")
	cmd.Flags().BoolVar(&mcpInstallPrint, "print", false, "Dry run: print diff, do not write or create key")
	return cmd
}

func runMcpInstall(cmd *cobra.Command, args []string) error {
	w := newWriter()

	// Resolve client — positional arg wins over --client.
	clientID := argOrFlag(args, mcpInstallClient)
	if clientID == "" {
		return errors.New("client is required (positional arg or --client); see `pura mcp ls`")
	}
	c := findClient(clientID)
	if c == nil {
		return fmt.Errorf("unknown client %q; see `pura mcp ls`", clientID)
	}

	scope, err := resolveScope(c, mcpInstallScope)
	if err != nil {
		return err
	}
	transport, err := resolveTransport(c, mcpInstallTransport)
	if err != nil {
		return err
	}

	path, err := c.resolvePath(scope)
	if err != nil {
		return err
	}

	cfg := loadConfig()
	if cfg.APIURL == "" {
		return errors.New("api_url not set; run `pura auth login` or `pura config set api_url <url>`")
	}
	if cfg.Token == "" {
		return errors.New("no session token; run `pura auth login`")
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	// Preflight — make sure the server is reachable before we mint a key.
	// We hit GET /mcp (cheap metadata route) rather than initialize so we
	// don't burn an MCP session id for a liveness probe.
	if err := preflightMcp(ctx, cfg.APIURL); err != nil {
		return fmt.Errorf("preflight %s/mcp: %w", cfg.APIURL, err)
	}

	// Load existing config (format-aware).
	tree, originalBytes, err := loadConfigFile(path, c.format)
	if err != nil {
		return err
	}

	// Compute what the new entry WOULD look like without a real key
	// (for the --print dry run and for prompt preview).
	rootPath := pathAt(c.rootKey)
	previewBlock, err := buildBlockForTransport(cfg.APIURL, "__token_preview__", c.id, transport)
	if err != nil {
		return err
	}
	previewEntry := c.renderEntry(previewBlock, "__key_preview__")

	// Existing pura entry?
	existing := map[string]any(nil)
	if dict, ok := getServerDict(tree, rootPath); ok {
		if raw, had := dict["pura"]; had {
			existing, _ = raw.(map[string]any)
		}
	}

	// Idempotency short-circuit: if the non-secret shape of the entry
	// already matches, there's nothing to do. We compare transport +
	// url/command + (header / env key set) rather than the Authorization
	// bearer value itself, because the stored key is opaque and differs
	// on every install.
	if existing != nil && entriesEqual(existing, previewEntry) {
		w.OK(map[string]any{
			"client":    c.id,
			"scope":     scope,
			"transport": transport,
			"path":      path,
			"changed":   false,
		}, output.WithSummary("%s already configured at %s — no change", c.label, path))
		return nil
	}

	// Conflicting non-identical entry → require --yes.
	if existing != nil && !mcpInstallYes && !mcpInstallPrint {
		if !w.IsTTY {
			return fmt.Errorf(
				"a `pura` entry already exists in %s and differs from the one we'd write; "+
					"rerun with --yes to overwrite (non-TTY)",
				path,
			)
		}
		// TTY prompt — keep it short. Future: full diff renderer.
		fmt.Fprintf(os.Stderr,
			"\n  A different `pura` entry exists in %s.\n"+
				"  Overwrite? [y/N] ", path)
		var ans string
		fmt.Fscanln(os.Stderr, &ans)
		if !strings.EqualFold(strings.TrimSpace(ans), "y") {
			return errors.New("aborted")
		}
	}

	if mcpInstallPrint {
		// Dry run — emit the diff without creating a key or writing.
		w.OK(map[string]any{
			"client":    c.id,
			"scope":     scope,
			"transport": transport,
			"path":      path,
			"existing":  existing,
			"proposed":  previewEntry,
			"changed":   true,
		}, output.WithSummary("DRY RUN: would write %s entry to %s", c.label, path))
		return nil
	}

	// ─── Live install ─────────────────────────────────────────────────
	keyID, keyPrefix, keyToken, err := createMcpKey(cfg, c.id, mcpInstallPermissions)
	if err != nil {
		return fmt.Errorf("mint api key: %w", err)
	}

	// Defer: on any subsequent error, revoke the key we just created.
	succeeded := false
	defer func() {
		if succeeded {
			return
		}
		_ = revokeKey(cfg, keyID)
	}()

	block, err := buildBlockForTransport(cfg.APIURL, keyToken, c.id, transport)
	if err != nil {
		return err
	}
	entry := c.renderEntry(block, keyID)

	// Splice into tree.
	dict, ok := getServerDict(tree, rootPath)
	if !ok {
		dict = map[string]any{}
	}
	dict["pura"] = entry
	setServerDict(tree, rootPath, dict)

	encoded, err := encodeFormat(tree, c.format, originalBytes)
	if err != nil {
		return fmt.Errorf("encode %s: %w", c.format, err)
	}
	backup, err := writeConfigAtomic(path, encoded)
	if err != nil {
		return err
	}

	// Handshake test — did we just configure something that actually works?
	// This is the last line of defense against typo'd apiURL / revoked
	// session / wrong token scope.
	if err := handshakeCheck(ctx, cfg.APIURL, keyToken); err != nil {
		// Rollback: restore the backup + revoke the key (via defer).
		_ = restoreBackup(path, backup)
		return fmt.Errorf("post-install handshake failed (rolled back): %w", err)
	}

	succeeded = true

	w.OK(map[string]any{
		"client":     c.id,
		"scope":      scope,
		"transport":  transport,
		"path":       path,
		"key_id":     keyID,
		"key_prefix": keyPrefix,
		"changed":    true,
	},
		output.WithSummary("Installed Pura into %s at %s", c.label, path),
		output.WithBreadcrumb(
			"restart",
			c.label,
			"Restart "+c.label+" to load the new config.",
		),
	)
	return nil
}

// preflightMcp GETs <base>/mcp with a short timeout. Any 2xx / 405 is
// treated as healthy (MCP spec doesn't strictly require GET support).
// 4xx/5xx / network errors trip a fail-fast before we create a key.
func preflightMcp(ctx context.Context, apiURL string) error {
	base := strings.TrimRight(apiURL, "/")
	httpC := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/mcp", nil)
	if err != nil {
		return err
	}
	resp, err := httpC.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// handshakeCheck runs the full initialize + tools/list handshake against
// the just-written entry's token. Verifies both auth and protocol.
func handshakeCheck(ctx context.Context, apiURL, token string) error {
	base := strings.TrimRight(apiURL, "/")
	httpC := httpJsonClient()
	if _, err := doMcpHandshake(ctx, httpC, base, token); err != nil {
		return err
	}
	tools, err := listMcpTools(ctx, httpC, base, token)
	if err != nil {
		return err
	}
	if len(tools) == 0 {
		return errors.New("tools/list returned empty — key may be under-scoped")
	}
	return nil
}

// argOrFlag returns args[0] when present, else the flag value. Shared by
// every `pura mcp <verb> <client>` subcommand that accepts the client id
// either positionally or via --client.
func argOrFlag(args []string, flagValue string) string {
	if len(args) > 0 && args[0] != "" {
		return args[0]
	}
	return flagValue
}
