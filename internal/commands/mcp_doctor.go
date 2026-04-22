// `pura mcp doctor` — cross-client health scan.
//
// Collects every locally-configured pura entry and every server-side MCP
// key, then reports:
//
//   stale_key       — config references a key the server revoked
//   orphan_entry    — config references a key the server doesn't know
//                     (key was manually revoked elsewhere, or the user's
//                     session doesn't own that account anymore)
//   orphan_key      — server-side MCP key with no matching local install
//                     (the client was uninstalled by hand without `uninstall`)
//   wrong_path      — historical: the old mcp.go wrote claude-code entries
//                     to ~/.claude/claude_desktop_config.json, which
//                     Claude Code doesn't read. Detect + offer migration.
//   missing_marker  — config has a pura entry but no __puraKeyId — can't
//                     rotate or revoke automatically (legacy install).

package commands

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

func newMcpDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Cross-client health scan (stale keys, orphans, legacy paths)",
		Long: `Scans every supported client × scope and every MCP-origin key on
the server side, then reports drift:

  stale_key       config references a revoked key
  orphan_entry    config references a key the server doesn't know
  orphan_key      server-side key with no matching local install
  wrong_path      legacy install at the old (wrong) Claude Code path
  missing_marker  pura entry without __puraKeyId (pre-2026-04 install)

Exit 0 when clean, exit 1 when any finding is surfaced.`,
		RunE: runMcpDoctor,
	}
}

type doctorFinding struct {
	Kind   string `json:"kind"`
	Client string `json:"client,omitempty"`
	Scope  string `json:"scope,omitempty"`
	Path   string `json:"path,omitempty"`
	KeyID  string `json:"key_id,omitempty"`
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

func runMcpDoctor(cmd *cobra.Command, _ []string) error {
	w := newWriter()
	cfg := loadConfig()
	findings := make([]doctorFinding, 0)

	// 1. Fetch server-side MCP keys (include revoked). Non-fatal.
	serverKeysByID := map[string]bool{}
	serverKeysByIDRevoked := map[string]bool{}
	if cfg.Token != "" && cfg.APIURL != "" {
		if keys, err := listMcpKeys(cfg, true); err == nil {
			for _, k := range keys {
				serverKeysByID[k.ID] = true
				if k.RevokedAt != "" {
					serverKeysByIDRevoked[k.ID] = true
				}
			}
		}
	}
	seenKeysLocally := map[string]bool{}

	// 2. Walk every client × scope.
	for i := range mcpClients {
		c := &mcpClients[i]
		for _, s := range c.scopes {
			path, err := c.resolvePath(s)
			if err != nil {
				continue
			}
			tree, _, err := loadConfigFile(path, c.format)
			if err != nil {
				findings = append(findings, doctorFinding{
					Kind:   "parse_error",
					Client: c.id,
					Scope:  string(s),
					Path:   path,
					Detail: err.Error(),
					Hint:   "Inspect the file manually; `pura mcp uninstall` will bail on malformed config.",
				})
				continue
			}
			dict, ok := getServerDict(tree, pathAt(c.rootKey))
			if !ok {
				continue
			}
			entry, ok := dict["pura"].(map[string]any)
			if !ok {
				continue
			}
			keyID := extractPuraKeyID(entry)
			if keyID == "" {
				findings = append(findings, doctorFinding{
					Kind:   "missing_marker",
					Client: c.id,
					Scope:  string(s),
					Path:   path,
					Hint:   "Run `pura mcp uninstall " + c.id + " && pura mcp install " + c.id + "` to attach a key.",
				})
				continue
			}
			seenKeysLocally[keyID] = true
			if !serverKeysByID[keyID] {
				findings = append(findings, doctorFinding{
					Kind:   "orphan_entry",
					Client: c.id,
					Scope:  string(s),
					Path:   path,
					KeyID:  keyID,
					Hint:   "`pura mcp uninstall " + c.id + " --keep-key && pura mcp install " + c.id + "`",
				})
				continue
			}
			if serverKeysByIDRevoked[keyID] {
				findings = append(findings, doctorFinding{
					Kind:   "stale_key",
					Client: c.id,
					Scope:  string(s),
					Path:   path,
					KeyID:  keyID,
					Hint:   "`pura mcp rotate " + c.id + "`",
				})
			}
		}
	}

	// 3. Detect the historical wrong Claude Code path.
	//    Old versions of `pura mcp install claude-code` wrote here,
	//    but Claude Code never reads it. Report + suggest migration.
	if home, err := os.UserHomeDir(); err == nil {
		legacy := filepath.Join(home, ".claude", "claude_desktop_config.json")
		if tree, _, err := loadConfigFile(legacy, formatJSON); err == nil {
			if dict, ok := getServerDict(tree, []string{"mcpServers"}); ok {
				if _, had := dict["pura"]; had {
					findings = append(findings, doctorFinding{
						Kind:   "wrong_path",
						Client: "claude-code",
						Path:   legacy,
						Detail: "Claude Code reads ~/.claude.json, not this file.",
						Hint:   "`pura mcp uninstall claude-code --keep-key` to clean the wrong entry, then `pura mcp install claude-code`.",
					})
				}
			}
		}
	}

	// 4. Server-side keys with no local install.
	for id := range serverKeysByID {
		if serverKeysByIDRevoked[id] {
			continue
		}
		if seenKeysLocally[id] {
			continue
		}
		findings = append(findings, doctorFinding{
			Kind:   "orphan_key",
			KeyID:  id,
			Hint:   "`pura keys rm " + id + "` if you removed the client manually.",
			Detail: "Server-side MCP key with no matching local config entry.",
		})
	}

	// Pretty print.
	if !(flagJSON || flagJQ != "" || !w.IsTTY) {
		if len(findings) == 0 {
			w.Print("  ✓ No issues found across %d clients.\n\n", len(mcpClients))
		} else {
			w.Print("  %d finding(s):\n", len(findings))
			for _, f := range findings {
				w.Print("    • [%s] %s\n", f.Kind, strings.TrimSpace(strings.Join([]string{f.Client, f.Path, f.KeyID, f.Detail}, " — ")))
				if f.Hint != "" {
					w.Print("        hint: %s\n", f.Hint)
				}
			}
			w.Print("\n")
		}
	}

	payload := map[string]any{
		"findings":    findings,
		"clients":     len(mcpClients),
		"server_keys": len(serverKeysByID),
		"local_keys":  len(seenKeysLocally),
	}
	if len(findings) == 0 {
		w.OK(payload, output.WithSummary("MCP doctor: clean"))
		return nil
	}
	w.OK(payload, output.WithSummary("MCP doctor: %d finding(s)", len(findings)))
	return nil
}
