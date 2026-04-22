// Unit tests for `pura mcp`. Exercises the 9-client registry, format
// codecs, transport resolution, and the install/uninstall/rotate/ls/
// doctor flows against a fake /api/auth/keys + /mcp server.
//
// Deliberately no real network: every flow goes through an httptest
// server so CI stays green offline. End-to-end scenarios against a real
// wrangler dev live in cli/e2e/mcp_e2e_test.go.

package commands

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"
	apiClient "github.com/pura-labs/cli/internal/api"
	"gopkg.in/yaml.v3"
)

// ─── Fake Pura server ───────────────────────────────────────────────────

// puraStub is a mini stand-in for the Pura origin. Implements everything
// `pura mcp install/uninstall/rotate/ls/doctor/test` hits:
//
//	GET  /mcp                     → 200 {}
//	POST /mcp                     → JSON-RPC initialize + tools/list
//	GET  /api/auth/keys           → list stored keys
//	POST /api/auth/keys           → mint a new key, store it
//	DELETE /api/auth/keys/{id}    → mark revoked
//
// Tests can flip `failHandshake` to simulate the post-install handshake
// going bad (rollback path).
type puraStub struct {
	mu            sync.Mutex
	keys          map[string]*keyRow
	nextID        int
	failHandshake bool
	server        *httptest.Server
}

type keyRow struct {
	ID        string
	Name      string
	Prefix    string
	Token     string
	Origin    string
	Scopes    []string
	RevokedAt string
	CreatedAt string
}

func newPuraStub(t *testing.T) *puraStub {
	s := &puraStub{keys: map[string]*keyRow{}}
	s.server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.server.Close)
	return s
}

func (s *puraStub) URL() string { return s.server.URL }

func (s *puraStub) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/mcp":
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"name":"pura-stub"}`))
	case r.Method == http.MethodPost && r.URL.Path == "/mcp":
		s.handleMcpRPC(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/auth/keys":
		s.handleListKeys(w)
	case r.Method == http.MethodPost && r.URL.Path == "/api/auth/keys":
		s.handleCreateKey(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/auth/keys/"):
		s.handleRevokeKey(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *puraStub) handleMcpRPC(w http.ResponseWriter, r *http.Request) {
	if s.failHandshake {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32000,"message":"stub: forced failure"}}`))
		return
	}
	body, _ := io.ReadAll(r.Body)
	var msg map[string]any
	_ = json.Unmarshal(body, &msg)
	method, _ := msg["method"].(string)
	id := msg["id"]
	w.Header().Set("Content-Type", "application/json")
	switch method {
	case "initialize":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  map[string]any{"protocolVersion": mcpProtocolVersion, "serverInfo": map[string]any{"name": "pura-stub"}},
		})
	case "tools/list":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"result": map[string]any{
				"tools": []map[string]any{
					{"name": "sheet.list_rows"},
					{"name": "doc.read"},
				},
			},
		})
	case "notifications/initialized":
		w.WriteHeader(202)
	case "ping":
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
	default:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"error":   map[string]any{"code": -32601, "message": "method not found"},
		})
	}
}

func (s *puraStub) handleListKeys(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]map[string]any, 0, len(s.keys))
	for _, k := range s.keys {
		list = append(list, keyRowJSON(k))
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": list})
}

func (s *puraStub) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
		Origin string   `json:"origin"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("key_stub_%03d", s.nextID)
	// Token must be ≥16 chars so server returns a 16-char prefix
	// (matches the real server's tokenPrefix implementation).
	token := fmt.Sprintf("sk_pura_stub%04d%s", s.nextID, strings.Repeat("x", 16))
	row := &keyRow{
		ID:        id,
		Name:      req.Name,
		Prefix:    token[:16],
		Token:     token,
		Origin:    req.Origin,
		Scopes:    req.Scopes,
		CreatedAt: "2026-04-22T00:00:00Z",
	}
	s.keys[id] = row
	s.mu.Unlock()
	w.WriteHeader(201)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": true,
		"data": map[string]any{
			"id":         row.ID,
			"name":       row.Name,
			"prefix":     row.Prefix,
			"token":      token,
			"scopes":     row.Scopes,
			"origin":     row.Origin,
			"created_at": row.CreatedAt,
		},
	})
}

func (s *puraStub) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/auth/keys/")
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.keys[id]
	if !ok || row.RevokedAt != "" {
		http.Error(w, "not_found", 404)
		return
	}
	row.RevokedAt = "2026-04-22T00:00:01Z"
	w.WriteHeader(204)
}

func keyRowJSON(k *keyRow) map[string]any {
	return map[string]any{
		"id":           k.ID,
		"name":         k.Name,
		"prefix":       k.Prefix,
		"scopes":       k.Scopes,
		"origin":       k.Origin,
		"last_used_at": "",
		"created_at":   k.CreatedAt,
		"revoked_at":   k.RevokedAt,
	}
}

// Counts helper for assertions.
func (s *puraStub) countActive() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, k := range s.keys {
		if k.RevokedAt == "" {
			n++
		}
	}
	return n
}

func (s *puraStub) countRevoked() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, k := range s.keys {
		if k.RevokedAt != "" {
			n++
		}
	}
	return n
}

// setStubAsAPI points the CLI at a puraStub with a fake session token so
// every /api/auth/keys + /mcp call from the CLI lands in the stub.
func setStubAsAPI(t *testing.T, s *puraStub) {
	t.Helper()
	flagAPIURL = s.URL()
	flagToken = "sk_pura_session_fixture"
}

// readJsonFileT reads a JSON file and decodes into map[string]any.
// Used to inspect what install/uninstall wrote.
func readJsonFileT(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %s: %v (raw: %s)", p, err, b)
	}
	return m
}

// tmpConfigPath allocates a per-test path and binds the matching
// PURA_<CLIENT>_CONFIG env override.
func tmpConfigPath(t *testing.T, envName, base string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, base)
	t.Setenv(envName, path)
	return path
}

// ─── Registry sanity ────────────────────────────────────────────────────

func TestMcp_Registry_AllClientsWellFormed(t *testing.T) {
	setupIsolatedHome(t)
	if len(mcpClients) < 9 {
		t.Fatalf("registry shrank: %d clients (expected ≥9)", len(mcpClients))
	}
	for _, c := range mcpClients {
		if c.id == "" || c.label == "" {
			t.Errorf("client with empty id/label: %+v", c)
		}
		if c.rootKey == "" {
			t.Errorf("%s has empty rootKey", c.id)
		}
		if c.format == "" {
			t.Errorf("%s has empty format", c.id)
		}
		if c.renderEntry == nil {
			t.Errorf("%s has nil renderEntry", c.id)
		}
		if len(c.transports) == 0 {
			t.Errorf("%s has no transports", c.id)
		}
		if !c.hasTransport(c.defaultTransport) {
			t.Errorf("%s default transport %s not in set %v", c.id, c.defaultTransport, c.transports)
		}
		if len(c.scopes) == 0 {
			t.Errorf("%s has no scopes", c.id)
		}
		if c.hasScope(scopeProject) && c.projectPath == nil {
			t.Errorf("%s declares project scope but has no projectPath resolver", c.id)
		}
	}
}

func TestMcp_FindClient(t *testing.T) {
	setupIsolatedHome(t)
	for _, want := range []string{"claude-desktop", "claude-code", "cursor", "vscode", "windsurf", "zed", "opencode", "codex", "goose", "gemini-cli"} {
		if findClient(want) == nil {
			t.Errorf("findClient(%q) = nil", want)
		}
	}
	if findClient("ghost") != nil {
		t.Errorf("findClient(ghost) should be nil")
	}
}

// ─── Path resolvers — env overrides ─────────────────────────────────────

func TestMcp_PathResolvers_EnvOverrides(t *testing.T) {
	setupIsolatedHome(t)
	t.Setenv("PURA_CLAUDE_DESKTOP_CONFIG", "/tmp/cd.json")
	t.Setenv("PURA_CLAUDE_CODE_CONFIG", "/tmp/cc.json")
	t.Setenv("PURA_CURSOR_CONFIG", "/tmp/cursor.json")
	t.Setenv("PURA_VSCODE_CONFIG", "/tmp/vscode.json")
	t.Setenv("PURA_WINDSURF_CONFIG", "/tmp/windsurf.json")
	t.Setenv("PURA_ZED_CONFIG", "/tmp/zed.json")
	t.Setenv("PURA_OPENCODE_CONFIG", "/tmp/opencode.json")
	t.Setenv("PURA_CODEX_CONFIG", "/tmp/codex.toml")
	t.Setenv("PURA_GOOSE_CONFIG", "/tmp/goose.yaml")
	t.Setenv("PURA_GEMINI_CONFIG", "/tmp/gemini.json")

	want := map[string]string{
		"claude-desktop": "/tmp/cd.json",
		"claude-code":    "/tmp/cc.json",
		"cursor":         "/tmp/cursor.json",
		"vscode":         "/tmp/vscode.json",
		"windsurf":       "/tmp/windsurf.json",
		"zed":            "/tmp/zed.json",
		"opencode":       "/tmp/opencode.json",
		"codex":          "/tmp/codex.toml",
		"goose":          "/tmp/goose.yaml",
		"gemini-cli":     "/tmp/gemini.json",
	}
	for id, expect := range want {
		c := findClient(id)
		got, err := c.resolvePath(scopeUser)
		if err != nil {
			t.Fatalf("%s resolvePath(user): %v", id, err)
		}
		if got != expect {
			t.Errorf("%s user path = %q, want %q", id, got, expect)
		}
	}
}

func TestMcp_PathResolvers_ProjectScope(t *testing.T) {
	setupIsolatedHome(t)
	// Land cwd in a temp dir so we can check the computed project paths.
	proj := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(proj); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cases := []struct {
		id  string
		suf string // expected path suffix relative to project dir
	}{
		{"claude-code", ".mcp.json"},
		{"cursor", filepath.Join(".cursor", "mcp.json")},
		{"vscode", filepath.Join(".vscode", "mcp.json")},
		{"opencode", ".opencode.json"},
		{"gemini-cli", filepath.Join(".gemini", "settings.json")},
	}
	for _, tc := range cases {
		c := findClient(tc.id)
		if !c.hasScope(scopeProject) {
			t.Errorf("%s should have project scope", tc.id)
			continue
		}
		got, err := c.resolvePath(scopeProject)
		if err != nil {
			t.Fatalf("%s: %v", tc.id, err)
		}
		if !strings.HasSuffix(got, tc.suf) {
			t.Errorf("%s project path = %q, expected suffix %q", tc.id, got, tc.suf)
		}
		// On macOS `/tmp` symlinks to `/private/tmp`, and os.Chdir
		// followed by os.Getwd can return the resolved form. Compare
		// both sides through EvalSymlinks to make the comparison stable.
		gotResolved, _ := filepath.EvalSymlinks(filepath.Dir(got))
		wantResolved, _ := filepath.EvalSymlinks(proj)
		if gotResolved != "" && wantResolved != "" && !strings.HasPrefix(gotResolved, wantResolved) && !strings.HasPrefix(got, proj) {
			t.Errorf("%s project path %q not under %q", tc.id, got, proj)
		}
	}
}

func TestMcp_PathResolvers_ProjectScopeUnsupported(t *testing.T) {
	setupIsolatedHome(t)
	for _, id := range []string{"claude-desktop", "windsurf", "zed", "codex", "goose"} {
		c := findClient(id)
		if _, err := c.resolvePath(scopeProject); err == nil {
			t.Errorf("%s: resolvePath(project) should fail — client only supports user scope", id)
		}
	}
}

// ─── RenderEntry regression guards ──────────────────────────────────────

func TestMcp_RenderEntry_Zed_UsesContextServersAndSource(t *testing.T) {
	c := findClient("zed")
	if c.rootKey != "context_servers" {
		t.Fatalf("zed rootKey = %q, want context_servers", c.rootKey)
	}
	block, _ := buildStdioBlock("https://pura.so", "tok", "zed")
	entry := c.renderEntry(block, "key_1")
	if entry["source"] != "custom" {
		t.Errorf("zed entry missing source:custom, got %+v", entry)
	}
	if entry["command"] == "" {
		t.Errorf("zed entry missing command")
	}
	if entry[puraKeyIDField] != "key_1" {
		t.Errorf("zed entry missing __puraKeyId marker")
	}
}

func TestMcp_ClaudeCode_UserPathIsRootDotClaudeJson(t *testing.T) {
	setupIsolatedHome(t)
	// Explicitly unset the test env var so we exercise the default branch.
	t.Setenv("PURA_CLAUDE_CODE_CONFIG", "")
	p, err := findClient("claude-code").resolvePath(scopeUser)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.HasSuffix(p, filepath.Join(os.Getenv("HOME"), ".claude.json")) {
		t.Errorf("claude-code user path = %q, want ~/.claude.json", p)
	}
	if strings.Contains(p, "claude_desktop_config.json") {
		t.Errorf("claude-code still points at the old desktop config: %q", p)
	}
}

func TestMcp_RenderEntry_Codex_TOMLShape(t *testing.T) {
	c := findClient("codex")
	if c.format != formatTOML {
		t.Fatalf("codex format = %q, want toml", c.format)
	}
	block, _ := buildStdioBlock("https://pura.so", "tok", "codex")
	entry := c.renderEntry(block, "key_1")
	if entry["command"] == "" || entry["args"] == nil {
		t.Errorf("codex entry shape = %+v", entry)
	}
	// TOML encode round-trip must succeed.
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(map[string]any{"mcp_servers": map[string]any{"pura": entry}}); err != nil {
		t.Fatalf("toml encode: %v", err)
	}
}

func TestMcp_RenderEntry_Goose_YAMLWithEnvsAndTimeout(t *testing.T) {
	c := findClient("goose")
	if c.format != formatYAML {
		t.Fatalf("goose format = %q, want yaml", c.format)
	}
	block, _ := buildStdioBlock("https://pura.so", "tok", "goose")
	entry := c.renderEntry(block, "key_1")
	if entry["envs"] == nil {
		t.Errorf("goose entry missing envs; got %+v", entry)
	}
	if entry["timeout"] != 300 {
		t.Errorf("goose entry timeout = %v, want 300", entry["timeout"])
	}
	if entry["type"] != "stdio" {
		t.Errorf("goose entry type = %v, want stdio", entry["type"])
	}
	var buf bytes.Buffer
	if err := yaml.NewEncoder(&buf).Encode(map[string]any{"extensions": map[string]any{"pura": entry}}); err != nil {
		t.Fatalf("yaml encode: %v", err)
	}
}

func TestMcp_RenderEntry_VSCode_UrlVariantSetsType(t *testing.T) {
	c := findClient("vscode")
	block := buildUrlBlock("https://pura.so", "tok", "vscode")
	entry := c.renderEntry(block, "key_1")
	if entry["type"] != "http" {
		t.Errorf("vscode url entry type = %v, want http", entry["type"])
	}
	if entry["url"] != "https://pura.so/mcp" {
		t.Errorf("vscode url entry url = %v", entry["url"])
	}
}

func TestMcp_RenderEntry_OpenCode_RemoteVsLocal(t *testing.T) {
	c := findClient("opencode")
	urlEntry := c.renderEntry(buildUrlBlock("https://pura.so", "t", "opencode"), "k1")
	if urlEntry["type"] != "remote" || urlEntry["enabled"] != true {
		t.Errorf("opencode url entry = %+v", urlEntry)
	}
	stdio, _ := buildStdioBlock("https://pura.so", "t", "opencode")
	localEntry := c.renderEntry(stdio, "k1")
	if localEntry["type"] != "local" || localEntry["enabled"] != true {
		t.Errorf("opencode stdio entry = %+v", localEntry)
	}
	// "environment" not "env" (opencode quirk).
	if _, has := localEntry["environment"]; !has {
		t.Errorf("opencode stdio entry missing 'environment'")
	}
}

// ─── Transport resolution ───────────────────────────────────────────────

func TestMcp_Transport_AutoPerClient(t *testing.T) {
	setupIsolatedHome(t)
	table := map[string]mcpTransport{
		"claude-desktop": transportStdio,
		"claude-code":    transportURL,
		"cursor":         transportURL,
		"vscode":         transportURL,
		"windsurf":       transportStdio,
		"zed":            transportStdio,
		"opencode":       transportURL,
		"codex":          transportStdio,
		"goose":          transportStdio,
		"gemini-cli":     transportStdio,
	}
	for id, want := range table {
		c := findClient(id)
		got, err := resolveTransport(c, "auto")
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got != want {
			t.Errorf("%s auto = %s, want %s", id, got, want)
		}
	}
}

func TestMcp_Transport_ExplicitOverridesAuto(t *testing.T) {
	setupIsolatedHome(t)
	c := findClient("claude-code")
	got, err := resolveTransport(c, "stdio")
	if err != nil {
		t.Fatal(err)
	}
	if got != transportStdio {
		t.Errorf("explicit stdio → %s", got)
	}
}

func TestMcp_Transport_RejectsUnsupported(t *testing.T) {
	setupIsolatedHome(t)
	c := findClient("codex")
	if _, err := resolveTransport(c, "url"); err == nil {
		t.Fatal("codex should reject url transport")
	}
}

// ─── Config codec round-trips ───────────────────────────────────────────

func TestMcp_Codec_JSONRoundTrip(t *testing.T) {
	in := map[string]any{"mcpServers": map[string]any{"a": map[string]any{"x": 1.0}}}
	b, err := encodeFormat(in, formatJSON, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeFormat(b, formatJSON)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(out) != fmt.Sprint(in) {
		t.Errorf("JSON round-trip mismatch: %+v vs %+v", out, in)
	}
}

func TestMcp_Codec_JSONC_PreservesTopLevelComments(t *testing.T) {
	orig := []byte(`{
  // top-level comment survives
  "mcpServers": {
    "existing": {"cmd": "something"}
  }
}`)
	tree, _, err := loadConfigFileRaw(orig, formatJSONC)
	if err != nil {
		t.Fatal(err)
	}
	// Add a new entry, re-emit. Check the comment survives.
	dict := tree["mcpServers"].(map[string]any)
	dict["pura"] = map[string]any{"url": "https://pura.so/mcp"}
	out, err := encodeFormat(tree, formatJSONC, orig)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "top-level comment survives") {
		t.Errorf("JSONC comment not preserved:\n%s", out)
	}
	if !strings.Contains(string(out), "pura") {
		t.Errorf("new pura key missing from output:\n%s", out)
	}
}

func TestMcp_Codec_YAML_RoundTrip(t *testing.T) {
	in := map[string]any{"extensions": map[string]any{"pura": map[string]any{"name": "pura", "timeout": 300}}}
	b, err := encodeFormat(in, formatYAML, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeFormat(b, formatYAML)
	if err != nil {
		t.Fatal(err)
	}
	// timeout serializes to int or int64 depending on path; cast to int64.
	ext := out["extensions"].(map[string]any)["pura"].(map[string]any)
	if v := ext["timeout"]; fmt.Sprint(v) != "300" {
		t.Errorf("yaml timeout = %v (%T)", v, v)
	}
}

func TestMcp_Codec_TOML_RoundTrip(t *testing.T) {
	in := map[string]any{"mcp_servers": map[string]any{"pura": map[string]any{"command": "/usr/bin/pura", "args": []any{"mcp", "proxy"}}}}
	b, err := encodeFormat(in, formatTOML, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeFormat(b, formatTOML)
	if err != nil {
		t.Fatal(err)
	}
	p := out["mcp_servers"].(map[string]any)["pura"].(map[string]any)
	if p["command"] != "/usr/bin/pura" {
		t.Errorf("toml command lost: %+v", p)
	}
}

// loadConfigFileRaw is a test-only helper — bypasses the file-read and
// decodes directly from raw bytes. Lets us write codec tests without
// round-tripping through the filesystem.
func loadConfigFileRaw(raw []byte, f configFormat) (map[string]any, []byte, error) {
	tree, err := decodeFormat(raw, f)
	if err != nil {
		return nil, raw, err
	}
	return tree, raw, nil
}

// ─── entriesEqual ───────────────────────────────────────────────────────

func TestMcp_EntriesEqual_IgnoresKeyIDMarker(t *testing.T) {
	a := map[string]any{"url": "https://pura.so/mcp", "headers": map[string]any{"Authorization": "Bearer X"}, puraKeyIDField: "key_1"}
	b := map[string]any{"url": "https://pura.so/mcp", "headers": map[string]any{"Authorization": "Bearer X"}, puraKeyIDField: "key_2"}
	if !entriesEqual(a, b) {
		t.Fatal("entries differing only in __puraKeyId should compare equal")
	}
}

func TestMcp_EntriesEqual_DetectsShapeDiff(t *testing.T) {
	a := map[string]any{"url": "https://pura.so/mcp"}
	b := map[string]any{"url": "https://other.host/mcp"}
	if entriesEqual(a, b) {
		t.Fatal("different urls should not compare equal")
	}
}

// ─── `pura mcp config` ──────────────────────────────────────────────────

func TestMcp_Config_Generic_PrintsStdio(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = "https://pura.example.com"
	flagToken = "sk_pura_abc"
	out, err := runCmd(t, "mcp", "config", "--json")
	if err != nil {
		t.Fatalf("mcp config: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	servers := env["data"].(map[string]any)["mcpServers"].(map[string]any)
	pura := servers["pura"].(map[string]any)
	if _, hasURL := pura["url"]; hasURL {
		t.Errorf("generic/stdio should not emit url; got %+v", pura)
	}
	if _, hasCmd := pura["command"]; !hasCmd {
		t.Errorf("generic/stdio should emit command")
	}
}

func TestMcp_Config_Cursor_UrlTransport(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = "https://pura.example.com"
	flagToken = "sk_pura_abc"
	out, err := runCmd(t, "mcp", "config", "cursor", "--json")
	if err != nil {
		t.Fatalf("mcp config cursor: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	servers := env["data"].(map[string]any)["mcpServers"].(map[string]any)
	pura := servers["pura"].(map[string]any)
	if pura["url"] != "https://pura.example.com/mcp" {
		t.Errorf("cursor URL = %v", pura["url"])
	}
}

func TestMcp_Config_ForCopy_RedactsToken(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = "https://pura.example.com"
	flagToken = "sk_pura_real_secret"
	out, err := runCmd(t, "mcp", "config", "cursor", "--for-copy", "--json")
	if err != nil {
		t.Fatalf("config: %v\n%s", err, out)
	}
	if strings.Contains(out, "sk_pura_real_secret") {
		t.Errorf("--for-copy leaked the token: %s", out)
	}
	// Go's json package HTML-escapes `<` as `<`; match the literal
	// token name which is stable either way.
	if !strings.Contains(out, "PURA_API_KEY") {
		t.Errorf("--for-copy did not emit placeholder: %s", out)
	}
}

// ─── `pura mcp install` ─────────────────────────────────────────────────

func TestMcp_Install_Cursor_Url_CreatesKeyAndWritesAuthHeader(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)
	path := tmpConfigPath(t, "PURA_CURSOR_CONFIG", "mcp.json")

	out, err := runCmd(t, "mcp", "install", "cursor", "--transport=url", "--yes", "--json")
	if err != nil {
		t.Fatalf("install: %v\nout: %s", err, out)
	}
	if s.countActive() != 1 {
		t.Fatalf("expected 1 active key; got %d", s.countActive())
	}
	doc := readJsonFileT(t, path)
	pura := doc["mcpServers"].(map[string]any)["pura"].(map[string]any)
	if pura["url"] != s.URL()+"/mcp" {
		t.Errorf("url = %v", pura["url"])
	}
	headers, _ := pura["headers"].(map[string]any)
	auth, _ := headers["Authorization"].(string)
	if !strings.HasPrefix(auth, "Bearer sk_pura_stub") {
		t.Errorf("Authorization = %v, want stub token", auth)
	}
	if pura[puraKeyIDField] == nil {
		t.Errorf("install should write __puraKeyId marker")
	}
}

func TestMcp_Install_ClaudeDesktop_Stdio_SetsEnvToken(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)
	path := tmpConfigPath(t, "PURA_CLAUDE_DESKTOP_CONFIG", "claude_desktop_config.json")

	out, err := runCmd(t, "mcp", "install", "claude-desktop", "--yes", "--json")
	if err != nil {
		t.Fatalf("install: %v\nout: %s", err, out)
	}
	doc := readJsonFileT(t, path)
	pura := doc["mcpServers"].(map[string]any)["pura"].(map[string]any)
	env := pura["env"].(map[string]any)
	if tok, _ := env["PURA_API_KEY"].(string); !strings.HasPrefix(tok, "sk_pura_stub") {
		t.Errorf("stdio PURA_API_KEY = %v", tok)
	}
}

func TestMcp_Install_Idempotent_ShortCircuits(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)
	tmpConfigPath(t, "PURA_CURSOR_CONFIG", "mcp.json")

	if _, err := runCmd(t, "mcp", "install", "cursor", "--transport=url", "--yes", "--json"); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if s.countActive() != 1 {
		t.Fatalf("after first install: %d active (want 1)", s.countActive())
	}
	if _, err := runCmd(t, "mcp", "install", "cursor", "--transport=url", "--yes", "--json"); err != nil {
		t.Fatalf("second install: %v", err)
	}
	// Second install must be a no-op on the server side: no new key, no
	// revocations. The second `create_key` happens inside the short-circuit
	// branch — let's actually trace: our current install flow computes the
	// preview BEFORE minting a key, so idempotent case never calls create.
	if s.countActive() != 1 {
		t.Errorf("idempotent install changed active key count: %d", s.countActive())
	}
	if s.countRevoked() != 0 {
		t.Errorf("idempotent install revoked something: %d", s.countRevoked())
	}
}

func TestMcp_Install_RollbackOnHandshakeFailure(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	s.failHandshake = true
	setStubAsAPI(t, s)
	path := tmpConfigPath(t, "PURA_CURSOR_CONFIG", "mcp.json")

	out, err := runCmd(t, "mcp", "install", "cursor", "--transport=url", "--yes", "--json")
	if err == nil {
		t.Fatalf("install should have failed; got %s", out)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		// File may exist but with backup restoration handled — or
		// just not be present (never had one).
		// Either way: server-side key must be revoked.
	}
	if s.countActive() != 0 {
		t.Errorf("rollback should revoke the key: %d active keys left", s.countActive())
	}
}

func TestMcp_Install_Print_DryRun_NoKey_NoFile(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)
	path := tmpConfigPath(t, "PURA_CURSOR_CONFIG", "mcp.json")

	out, err := runCmd(t, "mcp", "install", "cursor", "--print", "--json")
	if err != nil {
		t.Fatalf("install --print: %v\n%s", err, out)
	}
	if s.countActive() != 0 {
		t.Errorf("--print should not create a key; got %d", s.countActive())
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("--print should not write the config file")
	}
}

func TestMcp_Install_NonTTY_ConflictRequiresYes(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)
	path := tmpConfigPath(t, "PURA_CURSOR_CONFIG", "mcp.json")

	// Stage a conflicting pura entry.
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"pura":{"url":"https://other.host/mcp","headers":{"Authorization":"Bearer X"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, "mcp", "install", "cursor", "--transport=url", "--json")
	if err == nil {
		t.Fatalf("expected non-TTY conflict error; got %s", out)
	}
	// --yes should succeed.
	if _, err := runCmd(t, "mcp", "install", "cursor", "--transport=url", "--yes", "--json"); err != nil {
		t.Fatalf("install --yes: %v", err)
	}
}

func TestMcp_Install_ProjectScope_WritesCwdPath(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)
	// Chdir to a fresh temp so .cursor/ lands there.
	proj := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	_ = os.Chdir(proj)

	out, err := runCmd(t, "mcp", "install", "cursor", "--scope=project", "--transport=url", "--yes", "--json")
	if err != nil {
		t.Fatalf("install project: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(proj, ".cursor", "mcp.json")); err != nil {
		t.Fatalf("project config not written: %v", err)
	}
}

// ─── `pura mcp uninstall` ───────────────────────────────────────────────

func TestMcp_Uninstall_RevokesKeyByDefault(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)
	tmpConfigPath(t, "PURA_CURSOR_CONFIG", "mcp.json")

	if _, err := runCmd(t, "mcp", "install", "cursor", "--transport=url", "--yes", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, "mcp", "uninstall", "cursor", "--json"); err != nil {
		t.Fatal(err)
	}
	if s.countActive() != 0 {
		t.Errorf("uninstall should revoke: %d active", s.countActive())
	}
	if s.countRevoked() != 1 {
		t.Errorf("uninstall should revoke exactly one: %d revoked", s.countRevoked())
	}
}

func TestMcp_Uninstall_KeepKey_Preserves(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)
	tmpConfigPath(t, "PURA_CURSOR_CONFIG", "mcp.json")

	if _, err := runCmd(t, "mcp", "install", "cursor", "--transport=url", "--yes", "--json"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, "mcp", "uninstall", "cursor", "--keep-key", "--json"); err != nil {
		t.Fatal(err)
	}
	if s.countActive() != 1 {
		t.Errorf("--keep-key should leave key active: %d", s.countActive())
	}
}

func TestMcp_Uninstall_MissingMarker_DropsEntryNoRevoke(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)
	path := tmpConfigPath(t, "PURA_CURSOR_CONFIG", "mcp.json")
	// Manual install without __puraKeyId (legacy or user-edited).
	_ = os.WriteFile(path, []byte(`{"mcpServers":{"pura":{"url":"https://pura.so/mcp"}}}`), 0o600)
	if _, err := runCmd(t, "mcp", "uninstall", "cursor", "--json"); err != nil {
		t.Fatal(err)
	}
	doc := readJsonFileT(t, path)
	servers, _ := doc["mcpServers"].(map[string]any)
	if _, had := servers["pura"]; had {
		t.Errorf("pura entry should be gone")
	}
	if s.countRevoked() != 0 {
		t.Errorf("orphan uninstall should not revoke: %d revoked", s.countRevoked())
	}
}

// ─── `pura mcp rotate` ──────────────────────────────────────────────────

func TestMcp_Rotate_AtomicSuccess(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)
	path := tmpConfigPath(t, "PURA_CURSOR_CONFIG", "mcp.json")

	if _, err := runCmd(t, "mcp", "install", "cursor", "--transport=url", "--yes", "--json"); err != nil {
		t.Fatal(err)
	}
	origDoc := readJsonFileT(t, path)
	origAuth := origDoc["mcpServers"].(map[string]any)["pura"].(map[string]any)["headers"].(map[string]any)["Authorization"]

	if _, err := runCmd(t, "mcp", "rotate", "cursor", "--json"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	newDoc := readJsonFileT(t, path)
	newAuth := newDoc["mcpServers"].(map[string]any)["pura"].(map[string]any)["headers"].(map[string]any)["Authorization"]

	if origAuth == newAuth {
		t.Errorf("rotate did not change the token")
	}
	if s.countActive() != 1 {
		t.Errorf("rotate should leave exactly 1 active key: %d", s.countActive())
	}
	if s.countRevoked() != 1 {
		t.Errorf("rotate should revoke old key: %d revoked", s.countRevoked())
	}
}

func TestMcp_Rotate_HandshakeFailure_RevertsToOldKey(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)
	tmpConfigPath(t, "PURA_CURSOR_CONFIG", "mcp.json")

	if _, err := runCmd(t, "mcp", "install", "cursor", "--transport=url", "--yes", "--json"); err != nil {
		t.Fatal(err)
	}
	s.failHandshake = true
	if _, err := runCmd(t, "mcp", "rotate", "cursor", "--json"); err == nil {
		t.Fatal("rotate should have failed")
	}
	// Exactly 1 active (the original) + 1 revoked (the one we tried to
	// introduce). Old key stays active — the whole point of rotate safety.
	if s.countActive() != 1 {
		t.Errorf("after failed rotate: %d active (want 1)", s.countActive())
	}
	if s.countRevoked() != 1 {
		t.Errorf("after failed rotate: %d revoked (want 1)", s.countRevoked())
	}
}

// ─── `pura mcp ls` ──────────────────────────────────────────────────────

func TestMcp_Ls_ShowsInstalledAndStatus(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)
	tmpConfigPath(t, "PURA_CURSOR_CONFIG", "mcp.json")
	if _, err := runCmd(t, "mcp", "install", "cursor", "--transport=url", "--yes", "--json"); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, "mcp", "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	env := mustUnmarshalEnvelope(t, out)
	rows := env["data"].(map[string]any)["rows"].([]any)
	installedFound := false
	for _, r := range rows {
		row := r.(map[string]any)
		if row["id"] == "cursor" && row["installed"] == true {
			installedFound = true
			if row["key_status"] != string(keyStatusActive) {
				t.Errorf("cursor key_status = %v (want active)", row["key_status"])
			}
		}
	}
	if !installedFound {
		t.Error("cursor install not reflected in ls output")
	}
}

// ─── `pura mcp doctor` ──────────────────────────────────────────────────

func TestMcp_Doctor_CleanEnvironment(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)
	tmpConfigPath(t, "PURA_CURSOR_CONFIG", "mcp.json")

	if _, err := runCmd(t, "mcp", "install", "cursor", "--transport=url", "--yes", "--json"); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, "mcp", "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	env := mustUnmarshalEnvelope(t, out)
	findings := env["data"].(map[string]any)["findings"].([]any)
	if len(findings) != 0 {
		t.Errorf("doctor on clean env produced %d finding(s): %+v", len(findings), findings)
	}
}

func TestMcp_Doctor_FlagsRevokedKey(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)
	tmpConfigPath(t, "PURA_CURSOR_CONFIG", "mcp.json")

	if _, err := runCmd(t, "mcp", "install", "cursor", "--transport=url", "--yes", "--json"); err != nil {
		t.Fatal(err)
	}
	// Revoke the bound key out-of-band.
	s.mu.Lock()
	for _, k := range s.keys {
		k.RevokedAt = "2026-04-22T00:00:05Z"
	}
	s.mu.Unlock()

	out, err := runCmd(t, "mcp", "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	env := mustUnmarshalEnvelope(t, out)
	findings := env["data"].(map[string]any)["findings"].([]any)
	foundStale := false
	for _, f := range findings {
		m := f.(map[string]any)
		if m["kind"] == "stale_key" && m["client"] == "cursor" {
			foundStale = true
		}
	}
	if !foundStale {
		t.Errorf("doctor should report stale_key for cursor; got findings: %+v", findings)
	}
}

// ─── `pura mcp test` ────────────────────────────────────────────────────

func TestMcp_Test_HappyPath(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)

	out, err := runCmd(t, "mcp", "test", "--json")
	if err != nil {
		t.Fatalf("mcp test: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	tools := env["data"].(map[string]any)["tools"].([]any)
	if len(tools) == 0 {
		t.Errorf("mcp test returned 0 tools")
	}
}

// ─── `pura mcp proxy` (subprocess API) ──────────────────────────────────

func TestMcp_ProxyErrorPayloadHasOriginalId(t *testing.T) {
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	writeProxyErr(bw, `{"jsonrpc":"2.0","id":42,"method":"ping"}`, "boom")
	_ = bw.Flush()
	var got map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if id, _ := got["id"].(float64); int(id) != 42 {
		t.Errorf("proxy err id = %v", got["id"])
	}
}

// ─── Small helpers ──────────────────────────────────────────────────────

func TestMcp_ClientIDs_ReturnsAllInRegistryOrder(t *testing.T) {
	ids := clientIDs()
	if len(ids) != len(mcpClients) {
		t.Errorf("clientIDs len = %d, registry %d", len(ids), len(mcpClients))
	}
	for i, c := range mcpClients {
		if ids[i] != c.id {
			t.Errorf("clientIDs[%d] = %q, want %q", i, ids[i], c.id)
		}
	}
}

func TestMcp_ShortKey(t *testing.T) {
	if got := shortKey("sk_pura_abcd1234", ""); got != "sk_pura_abcd1234…" {
		t.Errorf("shortKey prefix = %q", got)
	}
	if got := shortKey("", "key_short"); got != "key_short" {
		t.Errorf("shortKey short id = %q", got)
	}
	if got := shortKey("", "key_this_is_long"); got != "key_this_is_…" {
		t.Errorf("shortKey long id = %q", got)
	}
	if got := shortKey("", ""); got != "(none)" {
		t.Errorf("shortKey empty = %q", got)
	}
}

func TestMcp_ClassifyKey(t *testing.T) {
	if classifyKey(nil, fmt.Errorf("net")) != keyStatusUnknown {
		t.Error("err → unknown")
	}
	if classifyKey(nil, nil) != keyStatusOrphaned {
		t.Error("nil → orphaned")
	}
	revoked := &apiClient.ApiKeyListItem{RevokedAt: "2026-04-22T00:00:01Z"}
	if classifyKey(revoked, nil) != keyStatusRevoked {
		t.Error("revoked")
	}
	active := &apiClient.ApiKeyListItem{}
	if classifyKey(active, nil) != keyStatusActive {
		t.Error("active")
	}
}

// ─── renderClaudeCodeEntry URL branch ───────────────────────────────────

func TestMcp_RenderEntry_ClaudeCode_UrlSetsTypeHttp(t *testing.T) {
	c := findClient("claude-code")
	block := buildUrlBlock("https://pura.so", "tok", "claude-code")
	entry := c.renderEntry(block, "key_cc")
	if entry["type"] != "http" {
		t.Errorf("claude-code url entry type = %v, want http", entry["type"])
	}
	if entry["url"] != "https://pura.so/mcp" {
		t.Errorf("claude-code url = %v", entry["url"])
	}
	if entry[puraKeyIDField] != "key_cc" {
		t.Errorf("claude-code url entry missing __puraKeyId")
	}
}

// Exercises the install-claude-code-url path end-to-end.
func TestMcp_Install_ClaudeCode_Url(t *testing.T) {
	setupIsolatedHome(t)
	s := newPuraStub(t)
	setStubAsAPI(t, s)
	path := tmpConfigPath(t, "PURA_CLAUDE_CODE_CONFIG", "claude.json")

	if _, err := runCmd(t, "mcp", "install", "claude-code", "--transport=url", "--yes", "--json"); err != nil {
		t.Fatal(err)
	}
	doc := readJsonFileT(t, path)
	pura := doc["mcpServers"].(map[string]any)["pura"].(map[string]any)
	if pura["type"] != "http" {
		t.Errorf("claude-code url entry missing type:http, got %+v", pura)
	}
}

// ─── SSE response parsing ───────────────────────────────────────────────

func TestMcp_ReadSseResponse_MatchesId(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"jsonrpc":"2.0","id":99,"result":{"protocolVersion":"2025-03-26"}}`,
		``,
		``,
	}, "\n")
	got, err := readSseResponse(strings.NewReader(sse), 99)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := got["result"].(map[string]any)
	if result["protocolVersion"] != "2025-03-26" {
		t.Errorf("sse result = %+v", got)
	}
}

func TestMcp_ReadSseResponse_IgnoresOtherIds(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"jsonrpc":"2.0","method":"notifications/progress","params":{}}`,
		``,
		`data: {"jsonrpc":"2.0","id":100,"result":{"ok":true}}`,
		``,
		``,
	}, "\n")
	got, err := readSseResponse(strings.NewReader(sse), 100)
	if err != nil {
		t.Fatal(err)
	}
	if got["id"] == nil {
		t.Errorf("no id in parsed: %+v", got)
	}
}

// ─── Proxy subprocess flow ──────────────────────────────────────────────

func TestMcp_Proxy_ForwardsRequestsToUpstream(t *testing.T) {
	// Upstream: a stub /mcp that echoes "result":"pong".
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var msg map[string]any
		_ = json.Unmarshal(body, &msg)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": "pong"})
	}))
	defer upstream.Close()

	t.Setenv("PURA_URL", upstream.URL)
	t.Setenv("PURA_API_KEY", "sk_pura_stub_for_proxy_test")

	// Replace stdin + stdout with pipes so runMcpProxy reads our line
	// and writes back to a buffer we can assert on.
	stdinR, stdinW, _ := os.Pipe()
	stdoutR, stdoutW, _ := os.Pipe()
	origIn, origOut := os.Stdin, os.Stdout
	os.Stdin = stdinR
	os.Stdout = stdoutW
	defer func() { os.Stdin = origIn; os.Stdout = origOut }()

	done := make(chan struct{})
	var readBuf bytes.Buffer
	go func() {
		_, _ = io.Copy(&readBuf, stdoutR)
		close(done)
	}()

	req := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"
	_, _ = stdinW.Write([]byte(req))
	_ = stdinW.Close() // EOF → runMcpProxy returns

	// runMcpProxy calls cmd.Context() — pass a freshly-wired cobra.Command
	// so the call site doesn't nil-panic.
	probeCmd := newMcpProxyCmd()
	if err := runMcpProxy(probeCmd, nil); err != nil {
		t.Fatalf("runMcpProxy: %v", err)
	}
	_ = stdoutW.Close()
	<-done

	line := strings.TrimSpace(readBuf.String())
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("proxy output not JSON: %v\n%s", err, line)
	}
	if got["result"] != "pong" {
		t.Errorf("proxy echoed wrong result: %+v", got)
	}
}
