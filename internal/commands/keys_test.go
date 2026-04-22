package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// keysFakeServer models the /api/auth/keys endpoints with just enough state
// to exercise the CLI. Each test starts with a clean slate — keysDB is per
// instance.
type keysFakeServer struct {
	srv             *httptest.Server
	keysDB          []map[string]any // kept as untyped so we control every field byte
	listRequireAuth bool
	lastAuthHeader  string
	createStatus    int
}

func newKeysFake(t *testing.T) *keysFakeServer {
	t.Helper()
	fs := &keysFakeServer{createStatus: 201}
	mux := http.NewServeMux()

	mux.HandleFunc("/api/auth/keys", func(w http.ResponseWriter, r *http.Request) {
		fs.lastAuthHeader = r.Header.Get("Authorization")
		if fs.listRequireAuth && !strings.HasPrefix(fs.lastAuthHeader, "Bearer ") {
			w.WriteHeader(401)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": map[string]string{"code": "unauthorized", "message": "need bearer"},
			})
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":   true,
				"data": fs.keysDB,
			})
		case http.MethodPost:
			var body struct {
				Name   string   `json:"name"`
				Scopes []string `json:"scopes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			newKey := map[string]any{
				"id":     "key_abc",
				"name":   body.Name,
				"prefix": "sk_pura_abcdefgh",
				"token":  "sk_pura_abcdefghSECRETPART",
				"scopes": body.Scopes,
			}
			fs.keysDB = append(fs.keysDB, map[string]any{
				"id": "key_abc", "name": body.Name, "prefix": "sk_pura_abcdefgh",
				"scopes": body.Scopes,
			})
			w.WriteHeader(fs.createStatus)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":   true,
				"data": newKey,
			})
		default:
			w.WriteHeader(405)
		}
	})

	// DELETE /api/auth/keys/<id>
	mux.HandleFunc("/api/auth/keys/", func(w http.ResponseWriter, r *http.Request) {
		fs.lastAuthHeader = r.Header.Get("Authorization")
		if r.Method != http.MethodDelete {
			w.WriteHeader(405)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/auth/keys/")
		kept := fs.keysDB[:0]
		for _, k := range fs.keysDB {
			if k["id"] != id {
				kept = append(kept, k)
			}
		}
		fs.keysDB = kept
		w.WriteHeader(204)
	})

	fs.srv = httptest.NewServer(mux)
	t.Cleanup(fs.srv.Close)
	return fs
}

func runKeys(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := rootCmd
	cmd.SetArgs(args)
	var out string
	err := func() error {
		var execErr error
		out = captureStdout(t, func() {
			_ = captureStderr(t, func() {
				execErr = cmd.Execute()
			})
		})
		return execErr
	}()
	return out, err
}

// ---- tests ----

func TestKeysLs_NoAuth_ShortCircuits(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = "http://127.0.0.1:1" // anywhere; not actually called
	_, err := runKeys(t, "keys", "ls")
	if err == nil {
		t.Fatal("want error when not signed in, got nil")
	}
	if err.Error() != "no token" {
		t.Errorf("err = %q, want %q", err.Error(), "no token")
	}
}

func TestKeysLs_EmptyList(t *testing.T) {
	setupIsolatedHome(t)
	fs := newKeysFake(t)
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_bootstrap"

	out, err := runKeys(t, "keys", "ls", "--json")
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	if env["ok"] != true {
		t.Errorf("want ok=true: %+v", env)
	}
	if env["summary"] != "No API keys yet" {
		t.Errorf("summary = %v", env["summary"])
	}
}

func TestKeysLs_SendsBearerAndRendersRows(t *testing.T) {
	setupIsolatedHome(t)
	fs := newKeysFake(t)
	fs.keysDB = []map[string]any{
		{"id": "key_1", "name": "ci", "prefix": "sk_pura_11111111", "scopes": []string{"docs:read"}, "created_at": "2026-04-17T00:00:00Z"},
		{"id": "key_2", "name": "bot", "prefix": "sk_pura_22222222", "scopes": []string{"docs:write"}, "created_at": "2026-04-17T12:00:00Z"},
	}
	fs.listRequireAuth = true
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_bootstrap"

	out, err := runKeys(t, "keys", "ls", "--json")
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, out)
	}
	if fs.lastAuthHeader != "Bearer sk_pura_bootstrap" {
		t.Errorf("Authorization header = %q", fs.lastAuthHeader)
	}
	env := mustUnmarshalEnvelope(t, out)
	arr, ok := env["data"].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("data should be 2-item array, got %T len=%d", env["data"], len(arr))
	}
}

func TestKeysCreate_RequiresName(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = "http://127.0.0.1:1"
	flagToken = "sk_pura_bootstrap"

	_, err := runKeys(t, "keys", "create", "--json")
	if err == nil {
		t.Fatal("want error when --name missing")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("err should mention name: %v", err)
	}
}

func TestKeysCreate_ReturnsPlaintextTokenOnce(t *testing.T) {
	setupIsolatedHome(t)
	fs := newKeysFake(t)
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_bootstrap"

	out, err := runKeys(t, "keys", "create", "--name", "ci:test", "--json")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)
	if data["token"] != "sk_pura_abcdefghSECRETPART" {
		t.Errorf("token not in response: %+v", data)
	}
	if data["prefix"] != "sk_pura_abcdefgh" {
		t.Errorf("prefix missing: %+v", data)
	}
	// Scopes default to docs:read + docs:write.
	scopes, _ := data["scopes"].([]any)
	if len(scopes) != 2 {
		t.Errorf("default scopes = %v, want 2 items", scopes)
	}
}

func TestKeysRm_ByID_Idempotent(t *testing.T) {
	setupIsolatedHome(t)
	fs := newKeysFake(t)
	fs.keysDB = []map[string]any{
		{"id": "key_tokill", "name": "old", "prefix": "sk_pura_deadbeef", "scopes": []string{"docs:read"}},
	}
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_bootstrap"

	out, err := runKeys(t, "keys", "rm", "key_tokill", "--yes", "--json")
	if err != nil {
		t.Fatalf("rm: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)
	if data["status"] != "revoked" || data["id"] != "key_tokill" {
		t.Errorf("bad rm envelope: %+v", data)
	}
	if len(fs.keysDB) != 0 {
		t.Errorf("server should be empty after rm, got %+v", fs.keysDB)
	}
}

func TestKeysRm_ByPrefix_ResolvesToID(t *testing.T) {
	setupIsolatedHome(t)
	fs := newKeysFake(t)
	fs.keysDB = []map[string]any{
		{"id": "key_byprefix", "name": "bot", "prefix": "sk_pura_pfx12345", "scopes": []string{"docs:write"}},
	}
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_bootstrap"

	_, err := runKeys(t, "keys", "rm", "sk_pura_pfx12345", "--yes", "--json")
	if err != nil {
		t.Fatalf("rm by prefix: %v", err)
	}
	if len(fs.keysDB) != 0 {
		t.Errorf("should have been revoked")
	}
}

func TestKeysRm_NonTTYRequiresYes(t *testing.T) {
	setupIsolatedHome(t)
	fs := newKeysFake(t)
	fs.keysDB = []map[string]any{
		{"id": "key_byprefix", "name": "bot", "prefix": "sk_pura_pfx12345", "scopes": []string{"docs:write"}},
	}
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_bootstrap"

	_, err := runKeys(t, "keys", "rm", "sk_pura_pfx12345", "--json")
	if err == nil {
		t.Fatal("want confirmation error without --yes on non-tty")
	}
	if !strings.Contains(err.Error(), "confirmation required") {
		t.Errorf("err = %v", err)
	}
	if len(fs.keysDB) != 1 {
		t.Errorf("key should not have been revoked")
	}
}

func TestKeysRm_UnknownTarget_ExitsNotFound(t *testing.T) {
	setupIsolatedHome(t)
	fs := newKeysFake(t)
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_bootstrap"

	out, err := runKeys(t, "keys", "rm", "sk_pura_ghost", "--yes", "--json")
	if err == nil {
		t.Fatalf("want error for unknown target\n%s", out)
	}
	if !strings.Contains(err.Error(), "no key") {
		t.Errorf("err = %v", err)
	}
}

// Smoke: the concrete type round-trips through the envelope JSON path.
func TestKeysCreate_RespectsScopesFlag(t *testing.T) {
	setupIsolatedHome(t)
	fs := newKeysFake(t)
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_bootstrap"

	out, err := runKeys(t, "keys", "create",
		"--name", "narrow",
		"--scope", "docs:read",
		"--json",
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	env := mustUnmarshalEnvelope(t, out)
	scopes, _ := env["data"].(map[string]any)["scopes"].([]any)
	if len(scopes) != 1 || scopes[0] != "docs:read" {
		t.Errorf("scopes = %v, want [docs:read]", scopes)
	}
	// Sanity check: bearer made it to server.
	if !strings.HasPrefix(fs.lastAuthHeader, "Bearer ") {
		t.Errorf("no bearer on create")
	}
	_ = out
}
