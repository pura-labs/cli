// MCP client registry — per-client config path, format, and entry shape.
//
// Adding a new client is a single-entry diff to `mcpClients` below: give it
// an id, path resolvers, root-key, format, and a renderEntry callback that
// turns a generic serverBlock into whatever JSON/YAML/TOML shape the client
// expects. The install/uninstall/rotate/ls/doctor commands are registry-
// driven and need no changes per new client.
//
// All nine entries were cross-checked against each client's official config
// reference on 2026-04-22. Per-OS path helpers honor the same
// `PURA_<CLIENT>_CONFIG` environment overrides the old single-file version
// supported, so existing tests keep working.

package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ─── Types ──────────────────────────────────────────────────────────────

// configFormat is the serialization of the target file (drives mcp_format.go).
type configFormat string

const (
	formatJSON  configFormat = "json"
	formatJSONC configFormat = "jsonc" // JSON with comments (VS Code)
	formatYAML  configFormat = "yaml"
	formatTOML  configFormat = "toml"
)

// mcpTransport is the wire protocol the client uses to reach Pura.
type mcpTransport string

const (
	transportURL   mcpTransport = "url"   // client POSTs straight to $pura/mcp
	transportStdio mcpTransport = "stdio" // client spawns `pura mcp proxy`
)

// mcpScope — user (home dir) vs project (cwd) config locations.
type mcpScope string

const (
	scopeUser    mcpScope = "user"
	scopeProject mcpScope = "project"
)

// serverBlock is the transport-neutral payload. renderEntry callbacks
// transform it into whatever shape each client's config file expects.
// Exactly one of {URL+Headers} or {Command+Args+Env} is populated at a time.
type serverBlock struct {
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// isURL reports whether block carries URL transport data.
func (b serverBlock) isURL() bool { return b.URL != "" }

// mcpClient describes one agent host we know how to integrate with.
type mcpClient struct {
	id               string
	label            string
	transports       []mcpTransport // set of supported transports
	defaultTransport mcpTransport   // picked when `--transport=auto`
	scopes           []mcpScope     // subset of {user, project}
	userPath         func() (string, error)
	// projectPath may be nil → client does not support per-project configs.
	projectPath func() (string, error)
	rootKey     string       // JSON path (may be dotted) where server dict lives
	format      configFormat // drives the codec in mcp_format.go
	// renderEntry wraps a serverBlock into the exact JSON/YAML/TOML shape
	// this client expects. Handlers live next to the registry to keep
	// per-client quirks co-located.
	renderEntry func(block serverBlock, keyID string) map[string]any
	note        string
}

func (c *mcpClient) hasTransport(t mcpTransport) bool {
	for _, tt := range c.transports {
		if tt == t {
			return true
		}
	}
	return false
}

func (c *mcpClient) hasScope(s mcpScope) bool {
	for _, ss := range c.scopes {
		if ss == s {
			return true
		}
	}
	return false
}

// resolvePath returns the user- or project-scoped config file path.
// Errors when the requested scope isn't supported by the client or the
// runtime can't resolve $HOME / cwd.
func (c *mcpClient) resolvePath(scope mcpScope) (string, error) {
	switch scope {
	case scopeUser:
		if c.userPath == nil {
			return "", fmt.Errorf("%s has no user-scope config path", c.id)
		}
		return c.userPath()
	case scopeProject:
		if c.projectPath == nil {
			return "", fmt.Errorf("%s does not support project-scope configs; use --scope=user", c.id)
		}
		return c.projectPath()
	}
	return "", fmt.Errorf("unknown scope: %s", scope)
}

// ─── Registry ───────────────────────────────────────────────────────────

var mcpClients = []mcpClient{
	clientClaudeDesktop(),
	clientClaudeCode(),
	clientCursor(),
	clientVSCode(),
	clientWindsurf(),
	clientZed(),
	clientOpenCode(),
	clientCodex(),
	clientGoose(),
	clientGeminiCLI(),
}

// clientIDs returns the list of ids in registry order. Used by help text
// and the interactive wizard.
func clientIDs() []string {
	out := make([]string, 0, len(mcpClients))
	for _, c := range mcpClients {
		out = append(out, c.id)
	}
	return out
}

func findClient(id string) *mcpClient {
	for i := range mcpClients {
		if mcpClients[i].id == id {
			return &mcpClients[i]
		}
	}
	return nil
}

// ─── Individual client definitions ──────────────────────────────────────

func clientClaudeDesktop() mcpClient {
	return mcpClient{
		id:               "claude-desktop",
		label:            "Claude Desktop",
		transports:       []mcpTransport{transportStdio},
		defaultTransport: transportStdio,
		scopes:           []mcpScope{scopeUser},
		userPath:         claudeDesktopUserPath,
		rootKey:          "mcpServers",
		format:           formatJSON,
		renderEntry:      renderStandardEntry,
		note:             "macOS / Windows / Linux desktop app. Restart after install.",
	}
}

func clientClaudeCode() mcpClient {
	return mcpClient{
		id:               "claude-code",
		label:            "Claude Code (CLI)",
		transports:       []mcpTransport{transportURL, transportStdio},
		defaultTransport: transportURL,
		scopes:           []mcpScope{scopeUser, scopeProject},
		userPath:         claudeCodeUserPath,
		projectPath:      claudeCodeProjectPath,
		rootKey:          "mcpServers",
		format:           formatJSON,
		renderEntry:      renderClaudeCodeEntry,
		note:             "Anthropic's coding CLI. User: ~/.claude.json · project: ./.mcp.json.",
	}
}

func clientCursor() mcpClient {
	return mcpClient{
		id:               "cursor",
		label:            "Cursor",
		transports:       []mcpTransport{transportURL, transportStdio},
		defaultTransport: transportURL,
		scopes:           []mcpScope{scopeUser, scopeProject},
		userPath:         cursorUserPath,
		projectPath:      cursorProjectPath,
		rootKey:          "mcpServers",
		format:           formatJSON,
		renderEntry:      renderStandardEntry,
		note:             "User: ~/.cursor/mcp.json · project: ./.cursor/mcp.json.",
	}
}

func clientVSCode() mcpClient {
	return mcpClient{
		id:               "vscode",
		label:            "VS Code (native MCP)",
		transports:       []mcpTransport{transportURL, transportStdio},
		defaultTransport: transportURL,
		scopes:           []mcpScope{scopeUser, scopeProject},
		userPath:         vscodeUserPath,
		projectPath:      vscodeProjectPath,
		rootKey:          "servers",
		format:           formatJSONC,
		renderEntry:      renderVSCodeEntry,
		note:             "Native MCP support. Comments in existing mcp.json are preserved.",
	}
}

func clientWindsurf() mcpClient {
	return mcpClient{
		id:               "windsurf",
		label:            "Windsurf",
		transports:       []mcpTransport{transportStdio},
		defaultTransport: transportStdio,
		scopes:           []mcpScope{scopeUser},
		userPath:         windsurfUserPath,
		rootKey:          "mcpServers",
		format:           formatJSON,
		renderEntry:      renderStandardEntry,
		note:             "Codeium-maintained IDE. ~/.codeium/windsurf/mcp_config.json.",
	}
}

func clientZed() mcpClient {
	return mcpClient{
		id:               "zed",
		label:            "Zed",
		transports:       []mcpTransport{transportStdio},
		defaultTransport: transportStdio,
		scopes:           []mcpScope{scopeUser},
		userPath:         zedUserPath,
		rootKey:          "context_servers", // NOT mcpServers — Zed-specific
		format:           formatJSON,
		renderEntry:      renderZedEntry,
		note:             "Zed reads context_servers, not mcpServers. Wrapper adds source:custom.",
	}
}

func clientOpenCode() mcpClient {
	return mcpClient{
		id:               "opencode",
		label:            "OpenCode",
		transports:       []mcpTransport{transportURL, transportStdio},
		defaultTransport: transportURL,
		scopes:           []mcpScope{scopeUser, scopeProject},
		userPath:         openCodeUserPath,
		projectPath:      openCodeProjectPath,
		rootKey:          "mcp",
		format:           formatJSON,
		renderEntry:      renderOpenCodeEntry,
		note:             "OpenCode uses {type:remote|local,...} instead of mcpServers.",
	}
}

func clientCodex() mcpClient {
	return mcpClient{
		id:               "codex",
		label:            "Codex (OpenAI CLI)",
		transports:       []mcpTransport{transportStdio},
		defaultTransport: transportStdio,
		scopes:           []mcpScope{scopeUser},
		userPath:         codexUserPath,
		rootKey:          "mcp_servers", // TOML table name
		format:           formatTOML,
		renderEntry:      renderStandardEntry,
		note:             "Config is TOML. Comments + key order preserved best-effort.",
	}
}

func clientGoose() mcpClient {
	return mcpClient{
		id:               "goose",
		label:            "Goose",
		transports:       []mcpTransport{transportStdio},
		defaultTransport: transportStdio,
		scopes:           []mcpScope{scopeUser},
		userPath:         gooseUserPath,
		rootKey:          "extensions",
		format:           formatYAML,
		renderEntry:      renderGooseEntry,
		note:             "Goose wraps entries with name/cmd/enabled/envs/timeout.",
	}
}

func clientGeminiCLI() mcpClient {
	return mcpClient{
		id:               "gemini-cli",
		label:            "Gemini CLI",
		transports:       []mcpTransport{transportStdio},
		defaultTransport: transportStdio,
		scopes:           []mcpScope{scopeUser, scopeProject},
		userPath:         geminiUserPath,
		projectPath:      geminiProjectPath,
		rootKey:          "mcpServers",
		format:           formatJSON,
		renderEntry:      renderStandardEntry,
		note:             "Google's Gemini CLI. User: ~/.gemini/settings.json · project: ./.gemini/settings.json.",
	}
}

// ─── Path resolvers ─────────────────────────────────────────────────────

// All resolvers honor the matching PURA_<ID>_CONFIG env var so tests can
// sandbox each client without touching the real home directory.

func envOr(name string, fallback func() (string, error)) func() (string, error) {
	return func() (string, error) {
		if v := os.Getenv(name); v != "" {
			return v, nil
		}
		return fallback()
	}
}

func home() (string, error) {
	return os.UserHomeDir()
}

func cwd() (string, error) {
	return os.Getwd()
}

func claudeDesktopUserPath() (string, error) {
	return envOr("PURA_CLAUDE_DESKTOP_CONFIG", func() (string, error) {
		h, err := home()
		if err != nil {
			return "", err
		}
		switch runtime.GOOS {
		case "darwin":
			return filepath.Join(h, "Library", "Application Support", "Claude", "claude_desktop_config.json"), nil
		case "windows":
			appdata := os.Getenv("APPDATA")
			if appdata == "" {
				appdata = filepath.Join(h, "AppData", "Roaming")
			}
			return filepath.Join(appdata, "Claude", "claude_desktop_config.json"), nil
		case "linux":
			xdg := os.Getenv("XDG_CONFIG_HOME")
			if xdg == "" {
				xdg = filepath.Join(h, ".config")
			}
			return filepath.Join(xdg, "Claude", "claude_desktop_config.json"), nil
		}
		return "", fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	})()
}

func claudeCodeUserPath() (string, error) {
	return envOr("PURA_CLAUDE_CODE_CONFIG", func() (string, error) {
		h, err := home()
		if err != nil {
			return "", err
		}
		// Claude Code's authoritative MCP config is ~/.claude.json (the
		// root JSON file in $HOME), NOT ~/.claude/... — the previous
		// implementation had this wrong, causing silent install failures.
		return filepath.Join(h, ".claude.json"), nil
	})()
}

func claudeCodeProjectPath() (string, error) {
	return envOr("PURA_CLAUDE_CODE_PROJECT_CONFIG", func() (string, error) {
		d, err := cwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(d, ".mcp.json"), nil
	})()
}

func cursorUserPath() (string, error) {
	return envOr("PURA_CURSOR_CONFIG", func() (string, error) {
		h, err := home()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, ".cursor", "mcp.json"), nil
	})()
}

func cursorProjectPath() (string, error) {
	return envOr("PURA_CURSOR_PROJECT_CONFIG", func() (string, error) {
		d, err := cwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(d, ".cursor", "mcp.json"), nil
	})()
}

func vscodeUserPath() (string, error) {
	return envOr("PURA_VSCODE_CONFIG", func() (string, error) {
		h, err := home()
		if err != nil {
			return "", err
		}
		switch runtime.GOOS {
		case "darwin":
			return filepath.Join(h, "Library", "Application Support", "Code", "User", "mcp.json"), nil
		case "windows":
			appdata := os.Getenv("APPDATA")
			if appdata == "" {
				appdata = filepath.Join(h, "AppData", "Roaming")
			}
			return filepath.Join(appdata, "Code", "User", "mcp.json"), nil
		case "linux":
			xdg := os.Getenv("XDG_CONFIG_HOME")
			if xdg == "" {
				xdg = filepath.Join(h, ".config")
			}
			return filepath.Join(xdg, "Code", "User", "mcp.json"), nil
		}
		return "", fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	})()
}

func vscodeProjectPath() (string, error) {
	return envOr("PURA_VSCODE_PROJECT_CONFIG", func() (string, error) {
		d, err := cwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(d, ".vscode", "mcp.json"), nil
	})()
}

func windsurfUserPath() (string, error) {
	return envOr("PURA_WINDSURF_CONFIG", func() (string, error) {
		h, err := home()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, ".codeium", "windsurf", "mcp_config.json"), nil
	})()
}

func zedUserPath() (string, error) {
	return envOr("PURA_ZED_CONFIG", func() (string, error) {
		h, err := home()
		if err != nil {
			return "", err
		}
		switch runtime.GOOS {
		case "darwin", "linux":
			xdg := os.Getenv("XDG_CONFIG_HOME")
			if xdg == "" {
				xdg = filepath.Join(h, ".config")
			}
			return filepath.Join(xdg, "zed", "settings.json"), nil
		case "windows":
			appdata := os.Getenv("APPDATA")
			if appdata == "" {
				appdata = filepath.Join(h, "AppData", "Roaming")
			}
			return filepath.Join(appdata, "Zed", "settings.json"), nil
		}
		return "", fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	})()
}

func openCodeUserPath() (string, error) {
	return envOr("PURA_OPENCODE_CONFIG", func() (string, error) {
		h, err := home()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, ".config", "opencode", "opencode.json"), nil
	})()
}

func openCodeProjectPath() (string, error) {
	return envOr("PURA_OPENCODE_PROJECT_CONFIG", func() (string, error) {
		d, err := cwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(d, ".opencode.json"), nil
	})()
}

func codexUserPath() (string, error) {
	return envOr("PURA_CODEX_CONFIG", func() (string, error) {
		if ch := os.Getenv("CODEX_HOME"); ch != "" {
			return filepath.Join(ch, "config.toml"), nil
		}
		h, err := home()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, ".codex", "config.toml"), nil
	})()
}

func gooseUserPath() (string, error) {
	return envOr("PURA_GOOSE_CONFIG", func() (string, error) {
		h, err := home()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, ".config", "goose", "config.yaml"), nil
	})()
}

func geminiUserPath() (string, error) {
	return envOr("PURA_GEMINI_CONFIG", func() (string, error) {
		h, err := home()
		if err != nil {
			return "", err
		}
		return filepath.Join(h, ".gemini", "settings.json"), nil
	})()
}

func geminiProjectPath() (string, error) {
	return envOr("PURA_GEMINI_PROJECT_CONFIG", func() (string, error) {
		d, err := cwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(d, ".gemini", "settings.json"), nil
	})()
}

// ─── renderEntry callbacks ──────────────────────────────────────────────

// puraKeyIDField is the sibling key we write alongside each server entry
// so `pura mcp {ls,rotate,uninstall,doctor}` can find the server-side key
// without grepping for a bearer token.
const puraKeyIDField = "__puraKeyId"

// renderStandardEntry — the common shape shared by most clients:
// url transport → {url, headers}
// stdio         → {command, args, env}
// Additive: "__puraKeyId" remembers the server-side key id.
func renderStandardEntry(b serverBlock, keyID string) map[string]any {
	entry := map[string]any{}
	if b.isURL() {
		entry["url"] = b.URL
		if len(b.Headers) > 0 {
			entry["headers"] = toAnyMap(b.Headers)
		}
	} else {
		entry["command"] = b.Command
		entry["args"] = toAnySlice(b.Args)
		if len(b.Env) > 0 {
			entry["env"] = toAnyMap(b.Env)
		}
	}
	if keyID != "" {
		entry[puraKeyIDField] = keyID
	}
	return entry
}

// renderClaudeCodeEntry — Claude Code uses {type:"http",...} for URL
// transport (matches `claude mcp add --transport http`). Stdio is the
// default — no "type" field emitted.
func renderClaudeCodeEntry(b serverBlock, keyID string) map[string]any {
	if b.isURL() {
		entry := map[string]any{
			"type": "http",
			"url":  b.URL,
		}
		if len(b.Headers) > 0 {
			entry["headers"] = toAnyMap(b.Headers)
		}
		if keyID != "" {
			entry[puraKeyIDField] = keyID
		}
		return entry
	}
	return renderStandardEntry(b, keyID)
}

// renderVSCodeEntry — VS Code's native MCP keys on `servers`, requires a
// `type` discriminator on every entry (http | stdio).
func renderVSCodeEntry(b serverBlock, keyID string) map[string]any {
	if b.isURL() {
		entry := map[string]any{
			"type": "http",
			"url":  b.URL,
		}
		if len(b.Headers) > 0 {
			entry["headers"] = toAnyMap(b.Headers)
		}
		if keyID != "" {
			entry[puraKeyIDField] = keyID
		}
		return entry
	}
	entry := map[string]any{
		"type":    "stdio",
		"command": b.Command,
		"args":    toAnySlice(b.Args),
	}
	if len(b.Env) > 0 {
		entry["env"] = toAnyMap(b.Env)
	}
	if keyID != "" {
		entry[puraKeyIDField] = keyID
	}
	return entry
}

// renderZedEntry — Zed wraps with source:"custom" and keeps command/args/env.
// Stdio-only (URL transport for context_servers is not stable yet).
func renderZedEntry(b serverBlock, keyID string) map[string]any {
	entry := map[string]any{
		"source":  "custom",
		"command": b.Command,
		"args":    toAnySlice(b.Args),
	}
	if len(b.Env) > 0 {
		entry["env"] = toAnyMap(b.Env)
	}
	if keyID != "" {
		entry[puraKeyIDField] = keyID
	}
	return entry
}

// renderOpenCodeEntry — OpenCode uses two different shapes depending on
// local vs remote transport.
func renderOpenCodeEntry(b serverBlock, keyID string) map[string]any {
	if b.isURL() {
		entry := map[string]any{
			"type":    "remote",
			"url":     b.URL,
			"enabled": true,
		}
		if len(b.Headers) > 0 {
			entry["headers"] = toAnyMap(b.Headers)
		}
		if keyID != "" {
			entry[puraKeyIDField] = keyID
		}
		return entry
	}
	entry := map[string]any{
		"type":    "local",
		"command": b.Command,
		"args":    toAnySlice(b.Args),
		"enabled": true,
	}
	if len(b.Env) > 0 {
		// OpenCode uses "environment" not "env" for local MCP servers.
		entry["environment"] = toAnyMap(b.Env)
	}
	if keyID != "" {
		entry[puraKeyIDField] = keyID
	}
	return entry
}

// renderGooseEntry — Goose uses name/cmd/args/envs and requires type+timeout.
// Stdio-only per Goose's current MCP support.
func renderGooseEntry(b serverBlock, keyID string) map[string]any {
	entry := map[string]any{
		"name":    "pura",
		"cmd":     b.Command,
		"args":    toAnySlice(b.Args),
		"enabled": true,
		"envs":    toAnyMap(b.Env),
		"type":    "stdio",
		"timeout": 300,
	}
	if keyID != "" {
		entry[puraKeyIDField] = keyID
	}
	return entry
}

// ─── Helpers ────────────────────────────────────────────────────────────

func toAnyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func toAnySlice(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// extractPuraKeyID pulls the server-side key id out of a previously-
// written entry. Accepts both string and already-any-typed values. Returns
// "" when the marker isn't present (entry predates this feature or the
// user edited the file manually).
func extractPuraKeyID(entry map[string]any) string {
	if v, ok := entry[puraKeyIDField]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
