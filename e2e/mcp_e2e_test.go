//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end coverage for `pura mcp` against a running wrangler dev.
// Each scenario uses a per-test temp dir for both the isolated HOME
// (so credentials + config files don't clobber real ones) and the
// cursor-scoped config path (via PURA_CURSOR_CONFIG). Requires
// PURA_E2E_URL (default http://localhost:8787) and PURA_E2E_TOKEN
// (the bypass-minted session token).

func TestE2EMcp_FullCycle_Cursor_UrlTransport(t *testing.T) {
	token := e2eToken(t)
	home := withIsolatedHome(t)

	// Sign in so `pura mcp` can hit /api/auth/keys.
	if _, err := puraCmd("auth", "login",
		"--token", token,
		"--profile", "e2e-mcp",
		"--api-url", apiURL(),
		"--json",
	); err != nil {
		t.Fatalf("auth login: %v", err)
	}

	// Isolate the cursor config path inside the temp home so we never
	// touch the user's real ~/.cursor/mcp.json.
	cursorCfg := filepath.Join(home, "cursor_mcp.json")
	t.Setenv("PURA_CURSOR_CONFIG", cursorCfg)

	// Cleanup — always attempt an uninstall at the end so dangling MCP
	// keys don't accumulate on the server between runs.
	t.Cleanup(func() {
		_, _ = puraCmd("mcp", "uninstall", "cursor",
			"--profile", "e2e-mcp",
			"--api-url", apiURL(),
			"--json",
		)
	})

	// --- install ---
	out, err := puraCmd("mcp", "install", "cursor",
		"--transport=url",
		"--yes",
		"--profile", "e2e-mcp",
		"--api-url", apiURL(),
		"--json",
	)
	if err != nil {
		t.Fatalf("mcp install: %v\n%s", err, out)
	}
	env := unmarshalE2E(t, out)
	data := env["data"].(map[string]any)
	if data["transport"] != "url" || data["client"] != "cursor" {
		t.Errorf("install payload unexpected: %+v", data)
	}
	keyID, _ := data["key_id"].(string)
	if keyID == "" {
		t.Fatalf("install payload missing key_id: %+v", data)
	}

	// Config file contains URL + Authorization + __puraKeyId.
	raw, err := os.ReadFile(cursorCfg)
	if err != nil {
		t.Fatalf("cursor config not written: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("cursor config not valid JSON: %v\n%s", err, raw)
	}
	pura := doc["mcpServers"].(map[string]any)["pura"].(map[string]any)
	if url, _ := pura["url"].(string); !strings.HasPrefix(url, apiURL()) {
		t.Errorf("cursor url = %v, expected %s/mcp", pura["url"], apiURL())
	}
	headers, _ := pura["headers"].(map[string]any)
	if auth, _ := headers["Authorization"].(string); !strings.HasPrefix(auth, "Bearer sk_pura_") {
		t.Errorf("Authorization header = %v", headers["Authorization"])
	}
	if pura["__puraKeyId"] != keyID {
		t.Errorf("__puraKeyId mismatch: %v vs %v", pura["__puraKeyId"], keyID)
	}

	// --- test ---
	out, err = puraCmd("mcp", "test",
		"--client=cursor",
		"--profile", "e2e-mcp",
		"--api-url", apiURL(),
		"--json",
	)
	if err != nil {
		t.Fatalf("mcp test: %v\n%s", err, out)
	}
	env = unmarshalE2E(t, out)
	tools, _ := env["data"].(map[string]any)["tools"].([]any)
	if len(tools) == 0 {
		t.Errorf("mcp test returned 0 tools: %s", out)
	}

	// --- rotate ---
	out, err = puraCmd("mcp", "rotate", "cursor",
		"--profile", "e2e-mcp",
		"--api-url", apiURL(),
		"--json",
	)
	if err != nil {
		t.Fatalf("mcp rotate: %v\n%s", err, out)
	}
	env = unmarshalE2E(t, out)
	data = env["data"].(map[string]any)
	newKeyID, _ := data["new_key_id"].(string)
	if newKeyID == "" || newKeyID == keyID {
		t.Errorf("rotate: new_key_id = %v, old = %v", newKeyID, keyID)
	}
	if data["old_key_id"] != keyID {
		t.Errorf("rotate: old_key_id = %v, want %v", data["old_key_id"], keyID)
	}

	// --- ls ---
	out, err = puraCmd("mcp", "ls",
		"--profile", "e2e-mcp",
		"--api-url", apiURL(),
		"--json",
	)
	if err != nil {
		t.Fatalf("mcp ls: %v\n%s", err, out)
	}
	env = unmarshalE2E(t, out)
	rows, _ := env["data"].(map[string]any)["rows"].([]any)
	foundActive := false
	for _, r := range rows {
		m := r.(map[string]any)
		if m["id"] == "cursor" && m["installed"] == true {
			if m["key_status"] != "active" {
				t.Errorf("cursor ls key_status = %v, want active", m["key_status"])
			}
			foundActive = true
		}
	}
	if !foundActive {
		t.Errorf("cursor not found as active in ls: %+v", rows)
	}

	// --- doctor (clean) ---
	out, err = puraCmd("mcp", "doctor",
		"--profile", "e2e-mcp",
		"--api-url", apiURL(),
		"--json",
	)
	if err != nil {
		t.Fatalf("mcp doctor: %v\n%s", err, out)
	}
	env = unmarshalE2E(t, out)
	findings, _ := env["data"].(map[string]any)["findings"].([]any)
	// Allow orphan_key findings for pre-existing keys; bail only on
	// findings specific to our install.
	for _, f := range findings {
		m := f.(map[string]any)
		if m["client"] == "cursor" {
			t.Errorf("doctor flagged our install: %+v", m)
		}
	}

	// --- uninstall (explicit here; cleanup is a safety net) ---
	out, err = puraCmd("mcp", "uninstall", "cursor",
		"--profile", "e2e-mcp",
		"--api-url", apiURL(),
		"--json",
	)
	if err != nil {
		t.Fatalf("mcp uninstall: %v\n%s", err, out)
	}
	env = unmarshalE2E(t, out)
	if env["data"].(map[string]any)["removed"] != true {
		t.Errorf("uninstall.removed != true: %+v", env["data"])
	}

	// File shouldn't contain "pura" anymore.
	raw, _ = os.ReadFile(cursorCfg)
	var post map[string]any
	_ = json.Unmarshal(raw, &post)
	servers, _ := post["mcpServers"].(map[string]any)
	if _, still := servers["pura"]; still {
		t.Errorf("pura entry still in file after uninstall: %s", raw)
	}
}

func unmarshalE2E(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(firstJSONObject(s)), &m); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, s)
	}
	return m
}
