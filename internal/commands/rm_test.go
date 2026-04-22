package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRmCommand_Yes(t *testing.T) {
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/api/p/@_/abc123" {
			t.Errorf("expected /api/p/@_/abc123, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("expected Authorization header")
		}
		deleted = true
		w.WriteHeader(204)
	}))
	defer server.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"rm", "abc123", "--yes", "--handle", "_", "--api-url", server.URL, "--token", "tok_test"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("rm command failed: %v", err)
	}

	if !deleted {
		t.Error("DELETE request was not made")
	}
}

// --force is kept as a hidden alias for --yes to avoid breaking scripts
// that were written against the earlier CLI shape.
func TestRmCommand_ForceAlias(t *testing.T) {
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deleted = true
		w.WriteHeader(204)
	}))
	defer server.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"rm", "abc123", "--force", "--handle", "_", "--api-url", server.URL, "--token", "tok_test"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm --force (alias): %v", err)
	}
	if !deleted {
		t.Error("DELETE request was not made")
	}
}

func TestRmCommand_NoToken(t *testing.T) {
	cmd := rootCmd
	cmd.SetArgs([]string{"rm", "abc123", "--yes", "--handle", "_", "--api-url", "http://localhost:0", "--token", ""})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error when no token is set")
	}
}

func TestRmCommand_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		json.NewEncoder(w).Encode(map[string]any{
			"ok": false,
			"error": map[string]string{
				"code":    "forbidden",
				"message": "Not authorized to delete this document",
			},
		})
	}))
	defer server.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"rm", "abc123", "--yes", "--handle", "_", "--api-url", server.URL, "--token", "tok_wrong"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for forbidden delete")
	}
}

func TestRmCommand_NonTTYRequiresYes(t *testing.T) {
	setupIsolatedHome(t)
	cmd := rootCmd
	cmd.SetArgs([]string{"rm", "abc123", "--api-url", "http://127.0.0.1:1", "--token", "tok_test"})
	rmCmd, _, findErr := cmd.Find([]string{"rm", "abc123"})
	if findErr != nil {
		t.Fatalf("find rm cmd: %v", findErr)
	}
	if err := rmCmd.Flags().Set("yes", "false"); err != nil {
		t.Fatalf("reset yes flag: %v", err)
	}
	var err error
	_ = captureStdout(t, func() {
		_ = captureStderr(t, func() {
			err = cmd.Execute()
		})
	})
	if err == nil {
		t.Fatal("want confirmation error without --yes on non-tty")
	}
	if !strings.Contains(err.Error(), "confirmation required") {
		t.Errorf("err = %v", err)
	}
}
