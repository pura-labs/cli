// Transport resolvers for `pura mcp`.
//
// A transport is the wire protocol that bridges an MCP client and Pura's
// `/mcp` endpoint. Two options:
//
//   - URL    — client POSTs JSON-RPC directly to $pura/mcp with an
//              Authorization header. Zero binary dependency on `pura` — an
//              OS package-manager upgrade never breaks the MCP install.
//
//   - stdio  — client spawns `pura mcp proxy` as a subprocess and pipes
//              JSON-RPC through stdin/stdout. Required for clients that
//              don't yet implement URL transport (Claude Desktop, Windsurf,
//              Zed, Codex, Goose as of 2026-04).
//
// `--transport=auto` resolves per-client from the registry default. This
// file is the single source of truth for that policy.

package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// puraAgentString is stamped into every outbound request for attestation.
// Format: "pura-cli/<version> (install:<client> session:<pid>)".
func puraAgentString(clientID string) string {
	return fmt.Sprintf("pura-cli/%s (install:%s session:%d)", versionStr, clientID, os.Getpid())
}

// resolvePuraBinary returns the absolute path to the currently-running
// pura binary. Used by the stdio transport so clients spawn the exact
// same binary the user ran `pura mcp install` from.
func resolvePuraBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve pura binary path: %w", err)
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return "", fmt.Errorf("resolve absolute pura path: %w", err)
	}
	return abs, nil
}

// buildUrlBlock returns the URL-transport payload. token may be "" for
// `pura mcp config --for-copy` (placeholder-only prints); install always
// passes a fresh token.
func buildUrlBlock(apiURL, token, clientID string) serverBlock {
	headers := map[string]string{
		"X-Pura-Agent": puraAgentString(clientID),
	}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	return serverBlock{
		URL:     strings.TrimRight(apiURL, "/") + "/mcp",
		Headers: headers,
	}
}

// buildStdioBlock returns the stdio-transport payload. The client is
// expected to spawn us with `mcp proxy` as the single argument; the env
// carries the runtime parameters so they can rotate without re-spawning
// the subprocess.
func buildStdioBlock(apiURL, token, clientID string) (serverBlock, error) {
	exe, err := resolvePuraBinary()
	if err != nil {
		return serverBlock{}, err
	}
	env := map[string]string{
		"PURA_URL":   strings.TrimRight(apiURL, "/"),
		"PURA_AGENT": puraAgentString(clientID),
	}
	if token != "" {
		env["PURA_API_KEY"] = token
	}
	return serverBlock{
		Command: exe,
		Args:    []string{"mcp", "proxy"},
		Env:     env,
	}, nil
}

// resolveTransport picks a concrete transport for the client. "" and
// "auto" resolve to the client's default; explicit values are validated
// against the client's supported set.
func resolveTransport(c *mcpClient, requested string) (mcpTransport, error) {
	if c == nil {
		return "", errors.New("resolveTransport: nil client")
	}
	req := strings.ToLower(strings.TrimSpace(requested))
	if req == "" || req == "auto" {
		return c.defaultTransport, nil
	}
	t := mcpTransport(req)
	if !c.hasTransport(t) {
		return "", fmt.Errorf(
			"%s does not support transport %q; valid: %v",
			c.id, req, c.transports,
		)
	}
	return t, nil
}

// buildBlockForTransport is the unified entry the install / config / test
// commands call. Picks the right builder for the transport, returns a
// ready-to-render serverBlock.
func buildBlockForTransport(apiURL, token, clientID string, t mcpTransport) (serverBlock, error) {
	switch t {
	case transportURL:
		return buildUrlBlock(apiURL, token, clientID), nil
	case transportStdio:
		return buildStdioBlock(apiURL, token, clientID)
	}
	return serverBlock{}, fmt.Errorf("unknown transport: %s", t)
}

// resolveScope returns a concrete scope for the client. "" → first
// scope in client.scopes (which is always user for all nine clients).
func resolveScope(c *mcpClient, requested string) (mcpScope, error) {
	if c == nil {
		return "", errors.New("resolveScope: nil client")
	}
	req := strings.ToLower(strings.TrimSpace(requested))
	if req == "" {
		return c.scopes[0], nil
	}
	s := mcpScope(req)
	if !c.hasScope(s) {
		return "", fmt.Errorf(
			"%s does not support scope %q; valid: %v",
			c.id, req, c.scopes,
		)
	}
	return s, nil
}
