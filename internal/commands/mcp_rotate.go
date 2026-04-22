// `pura mcp rotate` — atomic key swap for an already-installed client.
//
// Sequence (safe to Ctrl-C between any two steps — the worst case leaves
// BOTH the old and the new key active, which is recoverable by listing +
// revoking the spare):
//
//  1. read the config entry; extract the existing __puraKeyId
//  2. create a new key (origin="mcp:<client>")
//       defer: on failure below, revoke the new key
//  3. render + write the new entry (backup taken)
//  4. handshake test with the new key
//  5. on success: revoke the OLD key
//  6. on failure after write: restore backup, (defer) revoke new key,
//     keep old key active — user is back where they started

package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	mcpRotateClient string
	mcpRotateScope  string
)

func newMcpRotateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rotate <client>",
		Short: "Swap the API key for an installed MCP client (atomic)",
		Long: `Replace the API key bound to a client's pura entry. Creates a new
scoped key, rewrites the config atomically, handshake-tests, and only
then revokes the old key. On any failure, rolls back completely — the
old key stays active and the config file is restored from backup.

Use this when:
  • You suspect the key was exposed.
  • You're migrating the install between machines / accounts.
  • A periodic rotation policy fires.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runMcpRotate,
	}
	cmd.Flags().StringVar(&mcpRotateClient, "client", "", "Target client id (alternative to positional arg)")
	cmd.Flags().StringVar(&mcpRotateScope, "scope", "", "user | project")
	return cmd
}

func runMcpRotate(cmd *cobra.Command, args []string) error {
	w := newWriter()
	clientID := argOrFlag(args, mcpRotateClient)
	if clientID == "" {
		return errors.New("client is required; see `pura mcp ls`")
	}
	c := findClient(clientID)
	if c == nil {
		return fmt.Errorf("unknown client %q", clientID)
	}
	scope, err := resolveScope(c, mcpRotateScope)
	if err != nil {
		return err
	}
	path, err := c.resolvePath(scope)
	if err != nil {
		return err
	}
	cfg := loadConfig()
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	tree, originalBytes, err := loadConfigFile(path, c.format)
	if err != nil {
		return err
	}
	rootPath := pathAt(c.rootKey)
	dict, ok := getServerDict(tree, rootPath)
	if !ok {
		return fmt.Errorf("no %s block in %s; run `pura mcp install %s` first", c.rootKey, path, c.id)
	}
	existing, ok := dict["pura"].(map[string]any)
	if !ok {
		return fmt.Errorf("no pura entry in %s; run `pura mcp install %s` first", path, c.id)
	}

	oldKeyID := extractPuraKeyID(existing)
	// We detect the currently-installed transport by looking at whether
	// the existing entry carries a URL or a command field — that's the
	// contract rotate preserves.
	transport := transportStdio
	if _, hasURL := existing["url"]; hasURL {
		transport = transportURL
	}

	// Create the new key.
	newKeyID, newKeyPrefix, newKeyToken, err := createMcpKey(cfg, c.id, nil)
	if err != nil {
		return fmt.Errorf("mint new key: %w", err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = revokeKey(cfg, newKeyID)
		}
	}()

	block, err := buildBlockForTransport(cfg.APIURL, newKeyToken, c.id, transport)
	if err != nil {
		return err
	}
	newEntry := c.renderEntry(block, newKeyID)
	dict["pura"] = newEntry
	setServerDict(tree, rootPath, dict)

	encoded, err := encodeFormat(tree, c.format, originalBytes)
	if err != nil {
		return err
	}
	backup, err := writeConfigAtomic(path, encoded)
	if err != nil {
		return err
	}

	// Handshake with the NEW key.
	if err := handshakeCheck(ctx, cfg.APIURL, newKeyToken); err != nil {
		_ = restoreBackup(path, backup)
		return fmt.Errorf("post-rotate handshake failed (rolled back, old key still active): %w", err)
	}

	// Now it's safe to revoke the old key.
	revokeErr := revokeKey(cfg, oldKeyID)
	succeeded = true

	payload := map[string]any{
		"client":         c.id,
		"scope":          scope,
		"path":           path,
		"transport":      transport,
		"old_key_id":     oldKeyID,
		"new_key_id":     newKeyID,
		"new_key_prefix": newKeyPrefix,
	}
	if revokeErr != nil {
		payload["old_key_revoke_error"] = revokeErr.Error()
	}

	w.OK(payload,
		output.WithSummary("Rotated %s key for %s", c.label, path),
		output.WithBreadcrumb("restart", c.label, "Restart "+c.label+" so it picks up the new key."),
	)
	return nil
}
