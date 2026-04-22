package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pura-labs/cli/internal/api"
)

func TestListCommand_WithDocs(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}

		resp := api.ApiResponse[[]api.DocListItem]{
			OK: true,
			Data: []api.DocListItem{
				{
					Slug:      "abc123",
					Kind:      "doc",
					Substrate: "markdown",
					Title:     "First Doc",
					CreatedAt: "2025-01-15T10:00:00Z",
				},
				{
					Slug:      "def456",
					Kind:      "sheet",
					Substrate: "csv",
					Title:     "Data Export",
					CreatedAt: "2025-01-16T10:00:00Z",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"ls", "--handle", "_", "--api-url", server.URL, "--token", "tok_test"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("list command failed: %v", err)
	}
}

func TestKindSubstrateLabel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		kind      string
		substrate string
		want      string
	}{
		{name: "both distinct", kind: "sheet", substrate: "csv", want: "sheet/csv"},
		{name: "kind only", kind: "doc", want: "doc"},
		{name: "substrate only", substrate: "json", want: "json"},
		{name: "same values", kind: "canvas", substrate: "canvas", want: "canvas"},
		{name: "neither", want: "-"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := kindSubstrateLabel(tc.kind, tc.substrate); got != tc.want {
				t.Fatalf("kindSubstrateLabel(%q, %q) = %q, want %q", tc.kind, tc.substrate, got, tc.want)
			}
		})
	}
}

func TestListCommand_WithAPIKeyUsesAuthenticatedListing(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("user"); got != "me" {
			t.Fatalf("user query = %q, want me", got)
		}
		if got := r.URL.Query().Get("token"); got != "" {
			t.Fatalf("token query = %q, want empty", got)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer sk_pura_cli_key_123" {
			t.Fatalf("auth = %q, want Bearer sk_pura_cli_key_123", auth)
		}

		resp := api.ApiResponse[[]api.DocListItem]{
			OK: true,
			Data: []api.DocListItem{
				{
					Slug:      "owned-doc",
					Kind:      "doc",
					Substrate: "markdown",
					Title:     "Owned Doc",
					CreatedAt: "2025-01-17T10:00:00Z",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"ls", "--handle", "_", "--api-url", server.URL, "--token", "sk_pura_cli_key_123"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("list command failed: %v", err)
	}
}

func TestListCommand_Empty(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := api.ApiResponse[[]api.DocListItem]{
			OK:   true,
			Data: []api.DocListItem{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"ls", "--handle", "_", "--api-url", server.URL, "--token", "tok_test"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("list command (empty) failed: %v", err)
	}
}

func TestListCommand_NoToken(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	cmd := rootCmd
	cmd.SetArgs([]string{"ls", "--handle", "_", "--api-url", "http://localhost:0", "--token", ""})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error when no token is set")
	}
}

func TestListCommand_ShortCreatedAtDoesNotPanic(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := api.ApiResponse[[]api.DocListItem]{
			OK: true,
			Data: []api.DocListItem{
				{
					Slug:      "abc123",
					Kind:      "doc",
					Substrate: "markdown",
					Title:     "First Doc",
					CreatedAt: "",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"ls", "--handle", "_", "--api-url", server.URL, "--token", "tok_test"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("list command failed: %v", err)
	}
}
