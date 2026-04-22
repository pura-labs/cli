// `pura mcp uninstall` — remove a client's pura entry and revoke the key.
//
// Default is destructive-but-recoverable: the entry disappears from the
// config file (after a .bak-<ts> backup) and the server-side key is
// revoked. Use --keep-key to leave the key active (rare — typically
// when migrating the same key between clients manually).
//
// Orphan handling: if the config entry has no "__puraKeyId" marker (pre-
// T5 install or user edited manually), we drop the entry but can't revoke
// anything — report that clearly in the envelope so the user knows.

package commands

import (
	"errors"
	"fmt"

	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	mcpUninstallClient  string
	mcpUninstallScope   string
	mcpUninstallKeepKey bool
)

func newMcpUninstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall <client>",
		Short: "Remove Pura's MCP entry from a client and revoke the bound key",
		Long: `Drops the ` + "`pura`" + ` entry from the target client's config file and
revokes the server-side API key that was created by ` + "`pura mcp install`" + `.

  • The rest of the config is preserved (other MCP servers, comments).
  • A timestamped backup (.bak-<ts>) is written first, so a hand-edit
    accident is recoverable.
  • --keep-key leaves the server-side key active. Rare; usually you want
    the default.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runMcpUninstall,
	}
	cmd.Flags().StringVar(&mcpUninstallClient, "client", "", "Target client id (alternative to positional arg)")
	cmd.Flags().StringVar(&mcpUninstallScope, "scope", "", "user | project")
	cmd.Flags().BoolVar(&mcpUninstallKeepKey, "keep-key", false, "Do not revoke the server-side key")
	return cmd
}

func runMcpUninstall(cmd *cobra.Command, args []string) error {
	w := newWriter()
	clientID := argOrFlag(args, mcpUninstallClient)
	if clientID == "" {
		return errors.New("client is required (positional arg or --client); see `pura mcp ls`")
	}
	c := findClient(clientID)
	if c == nil {
		return fmt.Errorf("unknown client %q", clientID)
	}
	scope, err := resolveScope(c, mcpUninstallScope)
	if err != nil {
		return err
	}
	path, err := c.resolvePath(scope)
	if err != nil {
		return err
	}
	cfg := loadConfig()

	tree, originalBytes, err := loadConfigFile(path, c.format)
	if err != nil {
		return err
	}
	if len(tree) == 0 {
		w.OK(map[string]any{"client": c.id, "path": path, "removed": false, "key_revoked": false},
			output.WithSummary("No config at %s — nothing to remove.", path))
		return nil
	}

	rootPath := pathAt(c.rootKey)
	dict, ok := getServerDict(tree, rootPath)
	if !ok {
		w.OK(map[string]any{"client": c.id, "path": path, "removed": false, "key_revoked": false},
			output.WithSummary("No %s block in %s — nothing to remove.", c.rootKey, path))
		return nil
	}
	existingEntry, had := dict["pura"].(map[string]any)
	if !had {
		w.OK(map[string]any{"client": c.id, "path": path, "removed": false, "key_revoked": false},
			output.WithSummary("No `pura` entry in %s — nothing to remove.", path))
		return nil
	}

	// Remove + write.
	keyID := extractPuraKeyID(existingEntry)
	removeServerDictKey(tree, rootPath, "pura")

	encoded, err := encodeFormat(tree, c.format, originalBytes)
	if err != nil {
		return err
	}
	if _, err := writeConfigAtomic(path, encoded); err != nil {
		return err
	}

	// Revoke server-side key.
	keyRevoked := false
	var revokeErr error
	if keyID != "" && !mcpUninstallKeepKey {
		if err := revokeKey(cfg, keyID); err != nil {
			revokeErr = err
		} else {
			keyRevoked = true
		}
	}

	payload := map[string]any{
		"client":      c.id,
		"scope":       scope,
		"path":        path,
		"removed":     true,
		"key_id":      keyID,
		"key_revoked": keyRevoked,
	}
	opts := []output.Option{
		output.WithSummary("Removed Pura MCP entry from %s", c.label),
		output.WithBreadcrumb("restart", c.label, "Restart "+c.label+" to drop the connection."),
	}
	if revokeErr != nil {
		// Don't fail the whole op — the config is clean; report it.
		payload["revoke_error"] = revokeErr.Error()
	}
	if keyID == "" {
		payload["note"] = "No __puraKeyId marker — server-side key not touched."
	}
	w.OK(payload, opts...)
	return nil
}
