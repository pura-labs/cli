package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newClaimFake(t *testing.T, claimed int, wantCode string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/claim", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(401)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]string{"code": "unauthorized", "message": "no bearer"}})
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if wantCode != "" && body["edit_token"] != wantCode {
			w.WriteHeader(200)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]int{"claimed": 0}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]int{"claimed": claimed}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestClaim_RequiresAuth(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = "http://127.0.0.1:1"
	_, err := runCmd(t, "claim", "sk_pur_abc")
	if err == nil || err.Error() != "no token" {
		t.Fatalf("want no token error, got %v", err)
	}
}

func TestClaim_SuccessWithMatches(t *testing.T) {
	setupIsolatedHome(t)
	srv := newClaimFake(t, 3, "sk_pur_token123")
	flagAPIURL = srv.URL
	flagToken = "sk_pura_bootstrap"

	out, err := runCmd(t, "claim", "sk_pur_token123", "--json")
	if err != nil {
		t.Fatalf("claim: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)
	if data["claimed"].(float64) != 3 {
		t.Errorf("claimed = %v, want 3", data["claimed"])
	}
	if sum := env["summary"].(string); !strings.Contains(sum, "3 document") {
		t.Errorf("summary = %q", sum)
	}
}

func TestClaim_NoMatch_StillSucceeds(t *testing.T) {
	setupIsolatedHome(t)
	srv := newClaimFake(t, 0, "sk_pur_exists")
	flagAPIURL = srv.URL
	flagToken = "sk_pura_bootstrap"

	out, err := runCmd(t, "claim", "sk_pur_missing", "--json")
	if err != nil {
		t.Fatalf("claim: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	if env["data"].(map[string]any)["claimed"].(float64) != 0 {
		t.Error("claimed should be 0")
	}
	if sum := env["summary"].(string); !strings.Contains(sum, "No documents matched") {
		t.Errorf("summary should explain zero: %q", sum)
	}
}

func TestClaim_ServerRejects409(t *testing.T) {
	setupIsolatedHome(t)
	// Emulate the "no handle set" pathway where server returns 409.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": map[string]string{"code": "validation", "message": "Set a public handle before claiming documents"},
		})
	}))
	defer srv.Close()

	flagAPIURL = srv.URL
	flagToken = "sk_pura_bootstrap"

	_, err := runCmd(t, "claim", "sk_pur_anything", "--json")
	if err == nil {
		t.Fatal("want error on 409")
	}
	if !strings.Contains(err.Error(), "public handle") {
		t.Errorf("err should surface the hint: %v", err)
	}
}
