package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pura-labs/cli/internal/api"
	"github.com/pura-labs/cli/internal/auth"
)

// Integration-style tests for `pura auth …`.
//
// Each test spins a tiny httptest server emulating the Pura endpoints the
// CLI actually calls, then drives the cobra command end-to-end. We avoid
// mocking the api.Client internals — we want to catch real wiring bugs.
//
// A fresh HOME per test keeps the credentials file isolated; we never touch
// the user's real config.

// -------- helpers --------

// setupIsolatedHome wires HOME to a temp dir so auth.Store writes there.
// Returns the dir so tests can inspect credentials.json directly.
func setupIsolatedHome(t *testing.T) string {
	t.Helper()
	resetCommandGlobals()
	t.Cleanup(resetCommandGlobals)
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

// captureStderr redirects global stderr to a pipe for the duration of f.
// We use this because our command handlers write user-visible messages to
// stderr so stdout stays clean for JSON envelopes.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(r)
		done <- b
	}()
	f()
	_ = w.Close()
	os.Stderr = orig
	return string(<-done)
}

// captureStdout likewise for stdout — the JSON envelope lives here.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(r)
		done <- b
	}()
	f()
	_ = w.Close()
	os.Stdout = orig
	return string(<-done)
}

// mustUnmarshalEnvelope parses the JSON output of an OK envelope.
func mustUnmarshalEnvelope(t *testing.T, s string) map[string]any {
	t.Helper()
	// Strip any non-JSON prefix (shouldn't happen, but robust).
	i := strings.Index(s, "{")
	if i < 0 {
		t.Fatalf("no JSON in output: %q", s)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s[i:]), &m); err != nil {
		t.Fatalf("unmarshal: %v; raw: %s", err, s)
	}
	return m
}

// -------- fake server --------

// fakeServer lets a test configure specific endpoint responses without
// re-implementing the full API. Zero-value = 404 everywhere.
type fakeServer struct {
	srv             *httptest.Server
	meStatus        int
	mePayload       any
	deviceStartResp *api.DeviceStartResponse
	// pollSequence is consumed one poll at a time. nil entries return
	// "authorization_pending". Non-nil entries are returned verbatim.
	pollSequence []*fakePollReply
	pollIdx      atomic.Int32
	pollMu       sync.Mutex
	pollTimes    []time.Time
}

type fakePollReply struct {
	status int
	body   any
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	fs := &fakeServer{meStatus: 200}
	mux := http.NewServeMux()

	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if fs.mePayload == nil {
			http.Error(w, `{"ok":false,"error":{"code":"unauthorized","message":"no token"}}`, 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(fs.meStatus)
		_ = json.NewEncoder(w).Encode(fs.mePayload)
	})

	mux.HandleFunc("/api/auth/device", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if fs.deviceStartResp == nil {
			http.Error(w, `{"ok":false,"error":{"code":"server_error","message":"not configured"}}`, 500)
			return
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"data": fs.deviceStartResp,
		})
	})

	mux.HandleFunc("/api/auth/device/poll", func(w http.ResponseWriter, r *http.Request) {
		fs.pollMu.Lock()
		fs.pollTimes = append(fs.pollTimes, time.Now())
		fs.pollMu.Unlock()
		idx := int(fs.pollIdx.Add(1)) - 1
		if idx >= len(fs.pollSequence) {
			// Default: pending forever.
			http.Error(w, `{"ok":false,"error":{"code":"authorization_pending","message":"waiting"}}`, 401)
			return
		}
		reply := fs.pollSequence[idx]
		w.Header().Set("Content-Type", "application/json")
		if reply == nil {
			w.WriteHeader(401)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": map[string]string{"code": "authorization_pending", "message": "waiting"},
			})
			return
		}
		w.WriteHeader(reply.status)
		_ = json.NewEncoder(w).Encode(reply.body)
	})

	fs.srv = httptest.NewServer(mux)
	t.Cleanup(fs.srv.Close)
	return fs
}

func (fs *fakeServer) PollTimes() []time.Time {
	fs.pollMu.Lock()
	defer fs.pollMu.Unlock()
	out := make([]time.Time, len(fs.pollTimes))
	copy(out, fs.pollTimes)
	return out
}

// -------- tests: --token bypass --------

func TestAuthLogin_TokenBypass_Succeeds(t *testing.T) {
	dir := setupIsolatedHome(t)
	fs := newFakeServer(t)
	fs.mePayload = map[string]any{
		"ok": true,
		"data": map[string]any{
			"id":     "u_1",
			"email":  "alice@example.com",
			"handle": "alice",
			"via":    "api_key",
		},
	}

	flagAPIURL = fs.srv.URL
	flagProfile = "work"

	cmd := rootCmd
	cmd.SetArgs([]string{"auth", "login", "--token", "sk_pura_testtoken12345678", "--profile", "work"})

	stdout := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("auth login --token failed: %v", err)
		}
	})

	// Envelope on stdout should be ok=true with profile=work.
	env := mustUnmarshalEnvelope(t, stdout)
	if env["ok"] != true {
		t.Errorf("want ok=true, got %v", env["ok"])
	}
	data := env["data"].(map[string]any)
	if data["profile"] != "work" || data["handle"] != "alice" {
		t.Errorf("bad data: %+v", data)
	}

	// Credentials file should exist with the token stored.
	raw, err := os.ReadFile(filepath.Join(dir, ".config", "pura", "credentials.json"))
	if err != nil {
		t.Fatalf("read credentials: %v", err)
	}
	if !strings.Contains(string(raw), "sk_pura_testtoken12345678") {
		t.Errorf("credentials missing token:\n%s", raw)
	}
	if !strings.Contains(string(raw), `"work"`) {
		t.Errorf("credentials missing work profile:\n%s", raw)
	}
}

func TestAuthLogin_TokenBypass_VerifyFailure(t *testing.T) {
	setupIsolatedHome(t)
	fs := newFakeServer(t)
	fs.meStatus = 401
	fs.mePayload = nil // triggers 401 path in the stub

	flagAPIURL = fs.srv.URL

	cmd := rootCmd
	cmd.SetArgs([]string{"auth", "login", "--token", "sk_pura_badtoken"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("want error when /me rejects token, got nil")
	}
	// No credentials file should exist (we only save after verify succeeds).
	if _, statErr := os.Stat(filepath.Join(os.Getenv("HOME"), ".config", "pura", "credentials.json")); statErr == nil {
		t.Error("credentials file should NOT be created on verify failure")
	}
}

// -------- tests: status --------

func TestAuthStatus_NotSignedIn_Errors(t *testing.T) {
	setupIsolatedHome(t)
	cmd := rootCmd
	cmd.SetArgs([]string{"auth", "status"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("want error when not signed in, got nil")
	}
}

func TestAuthStatus_ShowsStoredRecord(t *testing.T) {
	setupIsolatedHome(t)
	// Seed a record directly to bypass the device flow.
	if err := auth.NewStore().Save("default", auth.Record{
		Token:     "sk_pura_stored",
		Handle:    "stored-user",
		APIUrl:    "https://example.test",
		KeyPrefix: "sk_pura_store",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"auth", "status"})

	stdout := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("status: %v", err)
		}
	})

	env := mustUnmarshalEnvelope(t, stdout)
	data := env["data"].(map[string]any)
	if data["handle"] != "stored-user" {
		t.Errorf("handle = %v, want stored-user", data["handle"])
	}
	if data["key_prefix"] != "sk_pura_store" {
		t.Errorf("key_prefix = %v", data["key_prefix"])
	}
	if data["verified"] != false {
		t.Errorf("verified should be false without --verify")
	}
}

func TestAuthStatus_VerifyHitsServer(t *testing.T) {
	setupIsolatedHome(t)
	fs := newFakeServer(t)
	fs.mePayload = map[string]any{
		"ok": true,
		"data": map[string]any{
			"id":     "u_X",
			"email":  "verified@example.com",
			"handle": "verified",
			"via":    "api_key",
		},
	}
	if err := auth.NewStore().Save("default", auth.Record{
		Token:  "sk_pura_stored",
		APIUrl: fs.srv.URL,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"auth", "status", "--verify"})

	stdout := captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("status --verify: %v", err)
		}
	})

	env := mustUnmarshalEnvelope(t, stdout)
	data := env["data"].(map[string]any)
	if data["verified"] != true {
		t.Errorf("verified = %v, want true", data["verified"])
	}
	if data["handle"] != "verified" {
		t.Errorf("handle after verify = %v, want verified", data["handle"])
	}
	if data["via"] != "api_key" {
		t.Errorf("via = %v, want api_key", data["via"])
	}
}

// -------- tests: logout --------

func TestAuthLogout_WipesAndIsIdempotent(t *testing.T) {
	dir := setupIsolatedHome(t)
	if err := auth.NewStore().Save("default", auth.Record{Token: "t"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"auth", "logout"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("first logout: %v", err)
	}
	// Second logout must also succeed (store.Delete is idempotent).
	cmd.SetArgs([]string{"auth", "logout"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("second logout: %v", err)
	}

	// Profile should be gone from creds file (the file itself may remain empty).
	raw, _ := os.ReadFile(filepath.Join(dir, ".config", "pura", "credentials.json"))
	if bytes.Contains(raw, []byte(`"token":"t"`)) {
		t.Errorf("token should be wiped after logout, still in file:\n%s", raw)
	}
}

// -------- tests: device-flow happy path --------

func TestAuthLogin_DeviceFlow_HappyPath(t *testing.T) {
	dir := setupIsolatedHome(t)
	fs := newFakeServer(t)
	fs.deviceStartResp = &api.DeviceStartResponse{
		DeviceCode:              "dc_fake",
		UserCode:                "ABCD-1234",
		VerificationURL:         fs.srv.URL + "/login/device",
		VerificationURLComplete: fs.srv.URL + "/login/device?code=ABCD-1234",
		ExpiresIn:               60,
		Interval:                1,
	}
	// First poll returns pending; second returns approved with the token.
	fs.pollSequence = []*fakePollReply{
		nil, // pending
		{
			status: 200,
			body: map[string]any{
				"ok": true,
				"data": map[string]any{
					"token":        "sk_pura_devicetok",
					"token_prefix": "sk_pura_device",
					"key_id":       "key_abc",
					"scopes":       []string{"docs:read", "docs:write"},
					"user": map[string]any{
						"id":     "u_device",
						"handle": "device-user",
						"email":  "device@example.com",
					},
				},
			},
		},
	}

	flagAPIURL = fs.srv.URL

	cmd := rootCmd
	// --no-browser so we don't actually try to spawn one; --timeout short so
	// a wedged test fails fast rather than hanging CI.
	cmd.SetArgs([]string{"auth", "login", "--no-browser", "--timeout", "5"})

	// Device-flow login is slightly slow (1s poll interval); ~2 polls = 2s.
	start := time.Now()
	stdout := captureStderr(t, func() {
		// Note: we capture stderr to check for the code print; stdout holds
		// the envelope which we assert on separately via a re-run below.
		_ = captureStdout(t, func() {
			if err := cmd.Execute(); err != nil {
				t.Fatalf("device-flow login: %v", err)
			}
		})
	})
	if time.Since(start) > 10*time.Second {
		t.Errorf("device-flow took too long: %v", time.Since(start))
	}

	if !strings.Contains(stdout, "ABCD-1234") {
		t.Errorf("stderr should include the user_code; got: %s", stdout)
	}

	// Credentials file should contain the device-flow token.
	raw, err := os.ReadFile(filepath.Join(dir, ".config", "pura", "credentials.json"))
	if err != nil {
		t.Fatalf("read creds: %v", err)
	}
	if !strings.Contains(string(raw), "sk_pura_devicetok") {
		t.Errorf("creds missing device token:\n%s", raw)
	}
}

// -------- auth refresh --------

// refreshSSE is a tiny dedicated server because refresh touches both
// /api/auth/keys (GET list + POST create + DELETE) and /api/auth/me.
// We pass back state via closures so tests can assert on call counts.
type refreshServer struct {
	srv         *httptest.Server
	keys        []map[string]any
	mintedToken string
	deletedID   string
	failCreate  bool
	failRevoke  bool
}

func newRefreshServer(t *testing.T) *refreshServer {
	t.Helper()
	rs := &refreshServer{mintedToken: "sk_pura_newSECRETPART"}
	mux := http.NewServeMux()

	mux.HandleFunc("/api/auth/keys", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": rs.keys})
		case http.MethodPost:
			if rs.failCreate {
				w.WriteHeader(500)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok": false, "error": map[string]string{"code": "server_error", "message": "boom"},
				})
				return
			}
			var body struct {
				Name   string   `json:"name"`
				Scopes []string `json:"scopes"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"data": map[string]any{
					"id":     "key_new",
					"name":   body.Name,
					"prefix": "sk_pura_newxxxx",
					"token":  rs.mintedToken,
					"scopes": body.Scopes,
				},
			})
		}
	})

	mux.HandleFunc("/api/auth/keys/", func(w http.ResponseWriter, r *http.Request) {
		if rs.failRevoke {
			w.WriteHeader(500)
			return
		}
		rs.deletedID = strings.TrimPrefix(r.URL.Path, "/api/auth/keys/")
		w.WriteHeader(204)
	})

	rs.srv = httptest.NewServer(mux)
	t.Cleanup(rs.srv.Close)
	return rs
}

func TestAuthRefresh_NoCredentials(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = "http://127.0.0.1:1"
	_, err := runCmd(t, "auth", "refresh", "--json")
	if err == nil {
		t.Fatal("want error when not signed in")
	}
}

func TestAuthRefresh_MissingKeyID(t *testing.T) {
	setupIsolatedHome(t)
	// Seed a token-only record (no key_id) — simulates a user who set
	// --token manually pre-device-flow.
	_ = auth.NewStore().Save("default", auth.Record{Token: "sk_pura_oldSECRET"})
	flagAPIURL = "http://127.0.0.1:1"
	_, err := runCmd(t, "auth", "refresh", "--json")
	if err == nil || err.Error() != "no key_id" {
		t.Fatalf("want no key_id error, got %v", err)
	}
}

func TestAuthRefresh_HappyPath_MintsAndRevokes(t *testing.T) {
	home := setupIsolatedHome(t)
	rs := newRefreshServer(t)
	rs.keys = []map[string]any{
		{"id": "key_old", "name": "cli:laptop", "prefix": "sk_pura_oldxx", "scopes": []string{"docs:read", "docs:write"}},
	}
	if err := auth.NewStore().Save("default", auth.Record{
		Token:  "sk_pura_oldSECRETPART",
		APIUrl: rs.srv.URL,
		KeyID:  "key_old",
	}); err != nil {
		t.Fatal(err)
	}
	flagAPIURL = rs.srv.URL

	out, err := runCmd(t, "auth", "refresh", "--json")
	if err != nil {
		t.Fatalf("refresh: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)
	if data["new_key"] != "key_new" || data["old_key"] != "key_old" {
		t.Errorf("envelope mismatch: %+v", data)
	}

	// New token must be persisted in credentials.json.
	raw, err := os.ReadFile(filepath.Join(home, ".config", "pura", "credentials.json"))
	if err != nil {
		t.Fatalf("read creds: %v", err)
	}
	if !strings.Contains(string(raw), rs.mintedToken) {
		t.Errorf("credentials missing new token: %s", raw)
	}
	if strings.Contains(string(raw), "sk_pura_oldSECRETPART") {
		t.Errorf("old token should be gone: %s", raw)
	}

	// The revoke call hit the server with the old key id.
	if rs.deletedID != "key_old" {
		t.Errorf("deletedID = %q, want key_old", rs.deletedID)
	}
}

func TestAuthRefresh_CreateFails_OldTokenPreserved(t *testing.T) {
	home := setupIsolatedHome(t)
	rs := newRefreshServer(t)
	rs.keys = []map[string]any{
		{"id": "key_old", "name": "cli:laptop", "prefix": "sk_pura_oldxx", "scopes": []string{"docs:read"}},
	}
	rs.failCreate = true
	_ = auth.NewStore().Save("default", auth.Record{
		Token:  "sk_pura_oldSECRETPART",
		APIUrl: rs.srv.URL,
		KeyID:  "key_old",
	})
	flagAPIURL = rs.srv.URL

	_, err := runCmd(t, "auth", "refresh", "--json")
	if err == nil {
		t.Fatal("want error when create fails")
	}
	// Old token must still be in place.
	raw, _ := os.ReadFile(filepath.Join(home, ".config", "pura", "credentials.json"))
	if !strings.Contains(string(raw), "sk_pura_oldSECRETPART") {
		t.Errorf("old token should be preserved after failed create: %s", raw)
	}
}

func TestAuthRefresh_RevokeFails_NewTokenKept(t *testing.T) {
	home := setupIsolatedHome(t)
	rs := newRefreshServer(t)
	rs.keys = []map[string]any{
		{"id": "key_old", "name": "cli:laptop", "prefix": "sk_pura_oldxx", "scopes": []string{"docs:read"}},
	}
	rs.failRevoke = true
	_ = auth.NewStore().Save("default", auth.Record{
		Token:  "sk_pura_oldSECRETPART",
		APIUrl: rs.srv.URL,
		KeyID:  "key_old",
	})
	flagAPIURL = rs.srv.URL

	out, err := runCmd(t, "auth", "refresh", "--json")
	// No error — we keep the new token even when revoke fails (with stderr warning).
	if err != nil {
		t.Fatalf("refresh should still succeed with revoke failure, got err=%v\n%s", err, out)
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".config", "pura", "credentials.json"))
	if !strings.Contains(string(raw), rs.mintedToken) {
		t.Errorf("new token should be persisted: %s", raw)
	}
}

func TestDeriveRotatedName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "cli (rotated)"},
		{"cli:laptop", "cli:laptop (rotated)"},
		{"cli:laptop (rotated)", "cli:laptop (rotated)"},     // collapse
		{"cli:laptop (rotated 3)", "cli:laptop (rotated)"},   // collapse counter
		{"already (rotated) (rotated)", "already (rotated)"}, // chain collapse
	}
	for _, tc := range cases {
		if got := deriveRotatedName(tc.in); got != tc.want {
			t.Errorf("deriveRotatedName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAuthLogin_DeviceFlow_AccessDenied(t *testing.T) {
	setupIsolatedHome(t)
	fs := newFakeServer(t)
	fs.deviceStartResp = &api.DeviceStartResponse{
		DeviceCode: "dc_fake",
		UserCode:   "WXYZ-5678",
		Interval:   1,
	}
	fs.pollSequence = []*fakePollReply{
		{
			status: 403,
			body: map[string]any{
				"ok":    false,
				"error": map[string]string{"code": "access_denied", "message": "user denied"},
			},
		},
	}
	flagAPIURL = fs.srv.URL

	cmd := rootCmd
	cmd.SetArgs([]string{"auth", "login", "--no-browser", "--timeout", "5"})
	_ = captureStderr(t, func() {
		_ = captureStdout(t, func() {
			err := cmd.Execute()
			if err == nil {
				t.Fatal("want error on access_denied")
			}
			if !strings.Contains(err.Error(), "denied") {
				t.Errorf("err should mention denial, got: %v", err)
			}
		})
	})
}

func TestAuthLogin_DeviceFlow_SlowDownHonorsRetryAfter(t *testing.T) {
	setupIsolatedHome(t)
	fs := newFakeServer(t)
	fs.deviceStartResp = &api.DeviceStartResponse{
		DeviceCode:              "dc_fake",
		UserCode:                "ABCD-1234",
		VerificationURL:         fs.srv.URL + "/login/device",
		VerificationURLComplete: fs.srv.URL + "/login/device?code=ABCD-1234",
		ExpiresIn:               60,
		Interval:                1,
	}
	fs.pollSequence = []*fakePollReply{
		nil,
		{
			status: 429,
			body: map[string]any{
				"ok": false,
				"error": map[string]any{
					"code":        "slow_down",
					"message":     "ease up",
					"retry_after": 3,
				},
			},
		},
		{
			status: 200,
			body: map[string]any{
				"ok": true,
				"data": map[string]any{
					"token":        "sk_pura_devicetok",
					"token_prefix": "sk_pura_device",
					"key_id":       "key_abc",
					"scopes":       []string{"docs:read"},
					"user": map[string]any{
						"id":     "u_device",
						"handle": "device-user",
						"email":  "device@example.com",
					},
				},
			},
		},
	}
	flagAPIURL = fs.srv.URL

	cmd := rootCmd
	cmd.SetArgs([]string{"auth", "login", "--no-browser", "--timeout", "10"})
	_ = captureStderr(t, func() {
		_ = captureStdout(t, func() {
			if err := cmd.Execute(); err != nil {
				t.Fatalf("device-flow login with slow_down: %v", err)
			}
		})
	})

	times := fs.PollTimes()
	if len(times) < 3 {
		t.Fatalf("want at least 3 polls, got %d", len(times))
	}
	secondGap := times[1].Sub(times[0])
	thirdGap := times[2].Sub(times[1])
	if secondGap < 900*time.Millisecond {
		t.Fatalf("first backoff gap too short: %v", secondGap)
	}
	if thirdGap < 2500*time.Millisecond {
		t.Fatalf("slow_down retry_after was not honored; gap=%v", thirdGap)
	}
}
