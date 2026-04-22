// `pura mcp` — Model Context Protocol integration for agent clients.
//
// Brings Pura into any MCP-speaking agent (Claude Desktop, Claude Code,
// Cursor, VS Code, Windsurf, Zed, OpenCode, Codex, Goose, Gemini CLI) by
// writing an entry into the client's config file and, by default, minting
// a scoped API key bound to that install.
//
// Surface (see each subcommand's help text for flags):
//
//   pura mcp                     # interactive wizard (huh)
//   pura mcp ls                  # list installs + key status
//   pura mcp config <client>     # print the connection JSON
//   pura mcp install <client>    # write config + create scoped key
//   pura mcp uninstall <client>  # remove config + revoke key
//   pura mcp rotate <client>     # atomically replace the key
//   pura mcp test                # probe /mcp endpoint
//   pura mcp doctor              # cross-client health scan
//   pura mcp proxy               # stdio bridge (hidden; invoked by clients)
//
// Architecture:
//
//   mcp_clients.go      — registry of 9 clients: paths, formats, renderEntry
//   mcp_transport.go    — URL / stdio block builders, auto-default policy
//   mcp_format.go       — JSON / JSONC / YAML / TOML codecs
//   mcp_config_io.go    — atomic write, backup, canonical compare
//   mcp_key.go          — MCP-origin API key lifecycle
//   mcp_rpc.go          — shared JSON-RPC 2.0 helpers for /mcp
//
//   mcp_install.go · mcp_uninstall.go · mcp_rotate.go · mcp_ls.go ·
//   mcp_doctor.go · mcp_test_cmd.go · mcp_proxy.go · mcp_wizard.go ·
//   mcp_config_cmd.go — one file per user-facing subcommand.
//
// Key lifecycle:
//
//   - install writes a fresh API key (origin="mcp:<client>", scopes=
//     docs:read/docs:write) into the client's config. The session token
//     is never embedded — so `pura keys rotate` can't silently break the
//     install, and `pura mcp uninstall` cleanly revokes the entry.
//   - Every install writes a sibling "__puraKeyId" field next to the
//     server entry so ls/rotate/uninstall/doctor can find the key id
//     without grepping for the bearer token.

package commands

import (
	"github.com/spf13/cobra"
)

// resetMcpFlags zeros every mcp-subcommand flag. Wired from
// resetCommandGlobals so cobra.Execute doesn't carry state between tests.
// Declared here so the whole group is visible in one place.
func resetMcpFlags() {
	mcpConfigClient = ""
	mcpConfigTransport = ""
	mcpConfigScope = ""
	mcpConfigForCopy = false

	mcpInstallClient = ""
	mcpInstallScope = ""
	mcpInstallTransport = ""
	mcpInstallName = ""
	mcpInstallPermissions = nil
	mcpInstallYes = false
	mcpInstallPrint = false

	mcpUninstallClient = ""
	mcpUninstallScope = ""
	mcpUninstallKeepKey = false

	mcpRotateClient = ""
	mcpRotateScope = ""

	mcpLsScope = ""
	mcpLsAllKeys = false

	mcpTestClient = ""
	mcpTestScope = ""
	mcpTestURL = ""
}

func newMcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Connect MCP clients (Claude, Cursor, VS Code, …) to Pura",
		Long: `Configure any MCP-speaking agent to talk to Pura.

Pura exposes all registered tools (sheet.list_rows, sheet.append_row, …) via
a JSON-RPC endpoint at ` + "`/mcp`" + `. This command group wires those tools
into nine common MCP clients so an agent can read and write Pura primitives
with the same UX as any other MCP service.

Quick start (Claude Desktop, macOS):

    pura auth login                           # if not already
    pura mcp install claude-desktop
    # restart Claude Desktop; Pura tools appear in the tool list

Or print the config JSON for a client without writing anything:

    pura mcp config cursor

Use ` + "`pura mcp ls`" + ` to see installed clients + key status.
Use ` + "`pura mcp test`" + ` to probe the Pura /mcp endpoint from this machine.
Use ` + "`pura mcp doctor`" + ` to scan for stale keys and broken configs.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMcpWizard(cmd)
		},
	}
	cmd.AddCommand(
		newMcpLsCmd(),
		newMcpConfigCmd(),
		newMcpInstallCmd(),
		newMcpUninstallCmd(),
		newMcpRotateCmd(),
		newMcpTestCmd(),
		newMcpDoctorCmd(),
		newMcpProxyCmd(),
	)
	return cmd
}

// clientOrGeneric + breadcrumbClientFlag stay here so every subcommand
// that emits output envelopes shares one set of cosmetic helpers.

func clientOrGeneric(id string) string {
	if id == "" {
		return "generic (works with any mcpServers-style client)"
	}
	if c := findClient(id); c != nil {
		return c.label
	}
	return id
}

func breadcrumbClientFlag(id string) string {
	if id == "" {
		return " <client>"
	}
	return " " + id
}
