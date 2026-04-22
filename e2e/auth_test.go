//go:build e2e

package e2e

// E2E coverage for `pura auth …` against a running wrangler dev server.
//
//   go test -tags=e2e ./e2e/... -v
//
// Requires:
//   PURA_E2E_URL    — base URL of the Pura server (default http://localhost:8787)
//   PURA_E2E_TOKEN  — a pre-issued sk_pura_ token belonging to a real user
//
// Skipped silently when either is missing or the server isn't up.
//
// We focus on the --token bypass path — that's the programmatically-testable
// half of auth. Full device-flow e2e needs a scripted browser approve and is
// tracked separately (PLAN §13 future work).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func e2eToken(t *testing.T) string {
	t.Helper()
	if !isServerUp() {
		t.Skip("API server not running at " + apiURL())
	}
	tok := os.Getenv("PURA_E2E_TOKEN")
	if tok == "" {
		t.Skip("PURA_E2E_TOKEN not set — device-flow e2e requires a pre-issued token")
	}
	if !strings.HasPrefix(tok, "sk_pura_") {
		t.Skipf("PURA_E2E_TOKEN does not look like an API key (got prefix %q)", tok[:8])
	}
	return tok
}

// withIsolatedHome redirects HOME to a per-test temp dir so the e2e run
// never clobbers the developer's real ~/.config/pura/credentials.json.
func withIsolatedHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestE2EAuth_TokenBypass(t *testing.T) {
	token := e2eToken(t)
	home := withIsolatedHome(t)

	// Sign in via the bypass path.
	out, err := puraCmd("auth", "login",
		"--token", token,
		"--profile", "e2e-bypass",
		"--api-url", apiURL(),
		"--json",
	)
	if err != nil {
		t.Fatalf("pura auth login --token failed: %v\nOutput: %s", err, out)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &env); err != nil {
		t.Fatalf("non-JSON login output: %v\n%s", err, out)
	}
	if env["ok"] != true {
		t.Fatalf("login envelope not ok=true: %v", env)
	}

	// Credentials file should be created with the token persisted.
	raw, err := os.ReadFile(filepath.Join(home, ".config", "pura", "credentials.json"))
	if err != nil {
		t.Fatalf("credentials not written: %v", err)
	}
	if !strings.Contains(string(raw), token) {
		t.Errorf("creds file missing token:\n%s", raw)
	}
	if !strings.Contains(string(raw), `"e2e-bypass"`) {
		t.Errorf("creds missing e2e-bypass profile:\n%s", raw)
	}

	// status --verify must round-trip the token via /api/auth/me.
	out, err = puraCmd("auth", "status",
		"--verify",
		"--profile", "e2e-bypass",
		"--api-url", apiURL(),
		"--json",
	)
	if err != nil {
		t.Fatalf("status --verify failed: %v\nOutput: %s", err, out)
	}
	if err := json.Unmarshal([]byte(firstJSONObject(out)), &env); err != nil {
		t.Fatalf("non-JSON status output: %v\n%s", err, out)
	}
	data, _ := env["data"].(map[string]any)
	if data == nil || data["verified"] != true {
		t.Errorf("status --verify did not set verified=true: %v", env)
	}
	// /me response surfaces via=api_key when the caller uses a Bearer.
	if via, ok := data["via"].(string); !ok || via != "api_key" {
		t.Errorf("status.via = %v, want api_key", data["via"])
	}

	// logout wipes the profile; a follow-up status should exit non-zero.
	_, _ = puraCmd("auth", "logout", "--profile", "e2e-bypass")

	out, err = puraCmd("auth", "status", "--profile", "e2e-bypass", "--json")
	if err == nil {
		t.Errorf("auth status after logout should fail, got success:\n%s", out)
	}
	if !strings.Contains(out, "not_signed_in") {
		t.Errorf("expected not_signed_in code after logout, got:\n%s", out)
	}
}

// firstJSONObject extracts the first balanced {...} from a mixed output
// stream. puraCmd merges stdout+stderr, and some envelope output is
// pretty-printed, so we have to hop past any leading human text.
func firstJSONObject(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return s
	}
	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return s[start:]
}
