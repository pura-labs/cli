// `pura mcp ls` — enumerate installed pura MCP entries and their status.
//
// For each (client × scope) we can resolve a path for, we:
//   - check whether the config file has a `pura` entry
//   - pull the __puraKeyId marker
//   - look it up server-side to classify the key (active/revoked/orphaned)
//
// Output is a human table by default, JSON envelope with --json or
// when piped / --jq specified.

package commands

import (
	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	mcpLsScope   string
	mcpLsAllKeys bool
)

func newMcpLsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List installed MCP entries and their key status",
		Long: `Scans every known client × scope for a ` + "`pura`" + ` entry and reports:
  • the resolved config path
  • the active transport (url | stdio)
  • the bound key id + status (active | revoked | orphaned | missing)

Use --scope=user|project|all to narrow the scan (default: all).`,
		RunE: runMcpLs,
	}
	cmd.Flags().StringVar(&mcpLsScope, "scope", "all", "user | project | all")
	cmd.Flags().BoolVar(&mcpLsAllKeys, "all-keys", false, "Include every MCP-origin key on the server (not just installed ones)")
	return cmd
}

type mcpLsRow struct {
	ID        string    `json:"id"`        // client id
	Label     string    `json:"label"`     // human label
	Scope     string    `json:"scope"`     // user | project
	Path      string    `json:"path"`      // resolved path (may be "(unsupported)")
	Installed bool      `json:"installed"` // config has a pura entry
	Transport string    `json:"transport"` // "url" | "stdio" | ""
	KeyID     string    `json:"key_id"`    // server-side key id (may be "")
	KeyPrefix string    `json:"key_prefix"`
	KeyStatus keyStatus `json:"key_status"`
	Note      string    `json:"note,omitempty"`
}

func runMcpLs(cmd *cobra.Command, _ []string) error {
	w := newWriter()
	cfg := loadConfig()

	// One server-side fetch for the whole pass; lets us classify keys
	// without N+1 roundtrips.
	var keyIndex map[string]*struct {
		revoked bool
		prefix  string
	}
	if cfg.Token != "" && cfg.APIURL != "" {
		// Best-effort: missing session isn't a fatal error for `ls`.
		if keys, err := listMcpKeys(cfg, true); err == nil {
			keyIndex = make(map[string]*struct {
				revoked bool
				prefix  string
			}, len(keys))
			for i := range keys {
				keyIndex[keys[i].ID] = &struct {
					revoked bool
					prefix  string
				}{revoked: keys[i].RevokedAt != "", prefix: keys[i].Prefix}
			}
		}
	}

	scopes := []mcpScope{scopeUser, scopeProject}
	if mcpLsScope == "user" {
		scopes = []mcpScope{scopeUser}
	} else if mcpLsScope == "project" {
		scopes = []mcpScope{scopeProject}
	}

	rows := make([]mcpLsRow, 0, len(mcpClients)*2)
	for i := range mcpClients {
		c := &mcpClients[i]
		for _, s := range scopes {
			if !c.hasScope(s) {
				continue
			}
			row := mcpLsRow{
				ID:        c.id,
				Label:     c.label,
				Scope:     string(s),
				Note:      c.note,
				KeyStatus: keyStatusMissing,
			}
			path, err := c.resolvePath(s)
			if err != nil {
				row.Path = "(unavailable: " + err.Error() + ")"
				rows = append(rows, row)
				continue
			}
			row.Path = path

			tree, _, err := loadConfigFile(path, c.format)
			if err != nil {
				row.Note = "parse error: " + err.Error()
				rows = append(rows, row)
				continue
			}
			dict, ok := getServerDict(tree, pathAt(c.rootKey))
			if !ok {
				rows = append(rows, row)
				continue
			}
			entry, ok := dict["pura"].(map[string]any)
			if !ok {
				rows = append(rows, row)
				continue
			}
			row.Installed = true
			// Detect transport.
			if _, hasURL := entry["url"]; hasURL {
				row.Transport = string(transportURL)
			} else if _, hasCmd := entry["command"]; hasCmd {
				row.Transport = string(transportStdio)
			}
			row.KeyID = extractPuraKeyID(entry)
			if row.KeyID == "" {
				row.KeyStatus = keyStatusMissing
			} else if keyIndex == nil {
				row.KeyStatus = keyStatusUnknown
			} else if info, hasKey := keyIndex[row.KeyID]; hasKey {
				row.KeyPrefix = info.prefix
				if info.revoked {
					row.KeyStatus = keyStatusRevoked
				} else {
					row.KeyStatus = keyStatusActive
				}
			} else {
				row.KeyStatus = keyStatusOrphaned
			}
			rows = append(rows, row)
		}
	}

	// Human pretty-print when we're a TTY and not in JSON mode.
	if !(flagJSON || flagJQ != "" || !w.IsTTY) {
		w.Print("  Installed Pura MCP entries\n")
		w.Print("  ──────────────────────────\n")
		any := false
		for _, r := range rows {
			if !r.Installed {
				continue
			}
			any = true
			w.Print("  ✓ %s (%s) — %s\n", r.ID, r.Scope, r.Path)
			if r.Transport != "" {
				w.Print("      transport=%s  key=%s  status=%s\n", r.Transport, shortKey(r.KeyPrefix, r.KeyID), r.KeyStatus)
			}
		}
		if !any {
			w.Print("  (no installs)\n")
		}
		w.Print("\n")
	}

	w.OK(map[string]any{"rows": rows},
		output.WithSummary("%d MCP installs listed", countInstalled(rows)))
	return nil
}

func countInstalled(rows []mcpLsRow) int {
	n := 0
	for _, r := range rows {
		if r.Installed {
			n++
		}
	}
	return n
}

func shortKey(prefix, id string) string {
	if prefix != "" {
		return prefix + "…"
	}
	if id != "" {
		if len(id) > 12 {
			return id[:12] + "…"
		}
		return id
	}
	return "(none)"
}
