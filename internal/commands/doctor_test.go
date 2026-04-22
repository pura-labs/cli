package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Doctor returns an error when ANY check is hard-fail, so tests branch on
// err presence to distinguish "all ok / warn" from "something broken".

func TestDoctor_AllOK(t *testing.T) {
	setupIsolatedHome(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"ok":true,"service":"pura"}`))
		case "/api/auth/me":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"data": map[string]any{
					"id": "u_1", "email": "a@b.com", "handle": "alice", "via": "api_key",
				},
			})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	flagAPIURL = server.URL
	flagToken = "sk_pura_alive"

	out, err := runCmd(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("want no error on all-ok doctor, got %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	if env["data"].(map[string]any)["ok"] != true {
		t.Errorf("envelope.ok = false for all-ok checks")
	}
}

func TestDoctor_NetworkFailsHard(t *testing.T) {
	setupIsolatedHome(t)
	// 500 on /health → network check hard-fails.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer server.Close()

	flagAPIURL = server.URL

	out, err := runCmd(t, "doctor", "--json")
	if err == nil {
		t.Fatalf("want error when network check fails\n%s", out)
	}
	env := mustUnmarshalEnvelope(t, out)
	if env["data"].(map[string]any)["ok"] != false {
		t.Errorf("envelope.ok should be false")
	}
	// The network check row must be status=fail; auth should be marked
	// skipped because we don't probe /me when network is down.
	checks, _ := env["data"].(map[string]any)["checks"].([]any)
	var sawNetworkFail, sawAuthSkipped bool
	for _, c := range checks {
		cm := c.(map[string]any)
		if cm["name"] == "network" && cm["status"] == "fail" {
			sawNetworkFail = true
		}
		if cm["name"] == "auth" && cm["status"] == "warn" {
			if d, _ := cm["detail"].(string); strings.Contains(d, "skipped") {
				sawAuthSkipped = true
			}
		}
	}
	if !sawNetworkFail {
		t.Errorf("expected a network=fail row; got %v", checks)
	}
	if !sawAuthSkipped {
		t.Errorf("expected auth to be skipped when network fails")
	}
}

func TestDoctor_NoTokenWarnsButPasses(t *testing.T) {
	setupIsolatedHome(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"ok":true,"service":"pura"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	flagAPIURL = server.URL
	// No token configured.

	out, err := runCmd(t, "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor should pass (only warn) when token is unset, got err=%v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	if env["data"].(map[string]any)["ok"] != true {
		t.Errorf("envelope.ok should be true when only warnings present")
	}
}

// -------- drift check --------

func withDriftServer(t *testing.T, status int, body string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	orig := driftCheckURL
	driftCheckURL = srv.URL
	t.Cleanup(func() { driftCheckURL = orig })
}

func withLocalVersion(t *testing.T, v string) {
	t.Helper()
	orig := versionStr
	versionStr = v
	t.Cleanup(func() { versionStr = orig })
}

func TestDriftCheck_DevBuildWarns(t *testing.T) {
	withLocalVersion(t, "dev")
	r := runDriftCheck()
	if r.Status != checkWarn {
		t.Errorf("dev build should warn, got %s", r.Status)
	}
}

func TestDriftCheck_UpToDate(t *testing.T) {
	withLocalVersion(t, "v1.2.3")
	withDriftServer(t, 200, `{"tag_name":"v1.2.3","name":"Release 1.2.3"}`)
	r := runDriftCheck()
	if r.Status != checkOK {
		t.Errorf("matching tag should be ok, got %s (%s)", r.Status, r.Detail)
	}
}

func TestDriftCheck_OutOfDate(t *testing.T) {
	withLocalVersion(t, "v1.0.0")
	withDriftServer(t, 200, `{"tag_name":"v1.2.3"}`)
	r := runDriftCheck()
	if r.Status != checkWarn {
		t.Errorf("out-of-date should warn, got %s", r.Status)
	}
	if !strings.Contains(r.Detail, "v1.0.0") || !strings.Contains(r.Detail, "v1.2.3") {
		t.Errorf("detail should name both versions: %q", r.Detail)
	}
}

func TestDriftCheck_AheadOfRelease(t *testing.T) {
	withLocalVersion(t, "v1.5.0")
	withDriftServer(t, 200, `{"tag_name":"v1.2.3"}`)
	r := runDriftCheck()
	if r.Status != checkOK {
		t.Errorf("ahead-of-release should be ok (not fail), got %s", r.Status)
	}
}

func TestDriftCheck_NetworkFailure_Warns(t *testing.T) {
	withLocalVersion(t, "v1.0.0")
	orig := driftCheckURL
	// Port 1 = reserved/unused; GET should fail fast.
	driftCheckURL = "http://127.0.0.1:1/nope"
	t.Cleanup(func() { driftCheckURL = orig })

	r := runDriftCheck()
	if r.Status != checkWarn {
		t.Errorf("network failure should warn (not fail), got %s", r.Status)
	}
}

func TestDriftCheck_NonJSONResponse_Warns(t *testing.T) {
	withLocalVersion(t, "v1.0.0")
	withDriftServer(t, 200, "<html>rate limited</html>")
	r := runDriftCheck()
	if r.Status != checkWarn {
		t.Errorf("malformed response should warn, got %s (%s)", r.Status, r.Detail)
	}
}

func TestDoctor_TokenRejectedIsHardFail(t *testing.T) {
	setupIsolatedHome(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"ok":true,"service":"pura"}`))
		case "/api/auth/me":
			w.WriteHeader(401)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": map[string]string{"code": "unauthorized", "message": "revoked"},
			})
		}
	}))
	defer server.Close()

	// We need a stored record (with api_url) so the auth check actually runs.
	flagAPIURL = server.URL
	flagToken = "sk_pura_stale"

	// The auth check loads from the store, so seed it directly.
	_ = storeSeed(t, "default", server.URL, "sk_pura_stale")

	_, err := runCmd(t, "doctor", "--json")
	if err == nil {
		t.Fatal("want error when /me rejects token")
	}
}

// storeSeed is a tiny shim for tests that want a stored credential with
// the matching api_url pinned in.
func storeSeed(t *testing.T, profile, apiURL, token string) error {
	t.Helper()
	// We piggy-back on auth.NewStore() because HOME is already redirected
	// by setupIsolatedHome.
	return newTestStoreSeed(profile, apiURL, token)
}
