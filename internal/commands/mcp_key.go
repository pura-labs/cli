// MCP key lifecycle helpers.
//
// `pura mcp install` mints an "mcp-origin" API key for each install and
// writes that key (never the session token) into the client's config.
// `uninstall` revokes it. `rotate` creates a new one, rewrites the config,
// then revokes the old. `ls` + `doctor` read the key back to report status.
//
// The server-side gate is plain `/api/auth/keys` — nothing MCP-specific.
// We identify MCP keys by the `origin` field we set at creation time
// (origin="mcp:<client>"), which we filter on when the CLI lists keys.

package commands

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/pura-labs/cli/internal/api"
	"github.com/pura-labs/cli/internal/config"
)

// mcpKeyScopes is the minimum-privilege scope set attached to every
// CLI-minted MCP key. Covers read + write of primitives — the common case
// for agent tool calls. Users can override with `--permissions` on install
// (validated against the session's own scopes server-side).
var mcpKeyScopes = []string{"docs:read", "docs:write"}

// originPattern must match the server-side validator in src/api/keys.ts.
// Keep in lockstep — regression tests on both sides.
var originPattern = regexp.MustCompile(`^[a-z0-9:_-]{1,40}$`)

// mcpKeyOrigin is the origin tag we set for every MCP-minted key.
// Format: "mcp:<client-id>" (e.g. "mcp:cursor").
func mcpKeyOrigin(clientID string) string {
	return "mcp:" + clientID
}

// mcpKeyName is the display name we set for every MCP-minted key.
// Format: "<client-id>@<host>" (e.g. "cursor@pura.so").
// Collisions are allowed — rotate/uninstall address by key id, not name.
func mcpKeyName(clientID, apiURL string) string {
	host := hostnameFromURL(apiURL)
	return clientID + "@" + host
}

func hostnameFromURL(apiURL string) string {
	u := strings.TrimSpace(apiURL)
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	if u == "" {
		u = "unknown"
	}
	return u
}

// createMcpKey mints a new key and returns its id + prefix + plaintext
// token. The plaintext is never persisted anywhere except the target
// client's config file (caller's job).
func createMcpKey(cfg *config.Config, clientID string, scopes []string) (keyID, prefix, token string, err error) {
	client := api.NewClient(cfg.APIURL, cfg.Token)
	req := api.CreateKeyRequest{
		Name:   mcpKeyName(clientID, cfg.APIURL),
		Scopes: scopesOrDefault(scopes),
		Origin: mcpKeyOrigin(clientID),
	}
	if !originPattern.MatchString(req.Origin) {
		return "", "", "", fmt.Errorf("invalid origin for clientID %q", clientID)
	}
	resp, err := client.CreateKey(req)
	if err != nil {
		return "", "", "", fmt.Errorf("create mcp key: %w", err)
	}
	return resp.ID, resp.Prefix, resp.Token, nil
}

// revokeKey wraps the API. Returns nil if the key is already revoked
// (404 → soft success) so rollback paths are idempotent.
func revokeKey(cfg *config.Config, keyID string) error {
	if keyID == "" {
		return nil
	}
	client := api.NewClient(cfg.APIURL, cfg.Token)
	if err := client.RevokeKey(keyID); err != nil {
		// 404 when the key is already gone — idempotent success.
		if strings.Contains(err.Error(), "not_found") || strings.Contains(err.Error(), "404") {
			return nil
		}
		return fmt.Errorf("revoke key %s: %w", keyID, err)
	}
	return nil
}

// listMcpKeys returns only the keys minted by `pura mcp install`
// (filtered on origin prefix "mcp:"). Revoked keys are included when
// includeRevoked=true so `pura mcp doctor` can surface broken installs.
func listMcpKeys(cfg *config.Config, includeRevoked bool) ([]api.ApiKeyListItem, error) {
	client := api.NewClient(cfg.APIURL, cfg.Token)
	all, err := client.ListKeys()
	if err != nil {
		return nil, err
	}
	out := make([]api.ApiKeyListItem, 0, len(all))
	for _, k := range all {
		if !strings.HasPrefix(k.Origin, "mcp:") {
			continue
		}
		if !includeRevoked && k.RevokedAt != "" {
			continue
		}
		out = append(out, k)
	}
	return out, nil
}

// findKeyByID looks up a specific key by its id. Returns nil (no error)
// when the id doesn't belong to this user — callers interpret this as
// "orphaned" (config references a key the server doesn't know).
func findKeyByID(cfg *config.Config, keyID string) (*api.ApiKeyListItem, error) {
	if keyID == "" {
		return nil, nil
	}
	client := api.NewClient(cfg.APIURL, cfg.Token)
	all, err := client.ListKeys()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == keyID {
			return &all[i], nil
		}
	}
	return nil, nil
}

func scopesOrDefault(scopes []string) []string {
	if len(scopes) == 0 {
		return mcpKeyScopes
	}
	return scopes
}

// keyStatus is a small enum the ls/doctor commands emit.
type keyStatus string

const (
	keyStatusActive   keyStatus = "active"
	keyStatusRevoked  keyStatus = "revoked"
	keyStatusOrphaned keyStatus = "orphaned" // config references a key the server doesn't know
	keyStatusUnknown  keyStatus = "unknown"  // couldn't reach the server
	keyStatusMissing  keyStatus = "missing"  // config has no __puraKeyId marker
)

// classifyKey classifies a server-side key lookup result.
func classifyKey(k *api.ApiKeyListItem, lookupErr error) keyStatus {
	if lookupErr != nil {
		return keyStatusUnknown
	}
	if k == nil {
		return keyStatusOrphaned
	}
	if k.RevokedAt != "" {
		return keyStatusRevoked
	}
	return keyStatusActive
}
