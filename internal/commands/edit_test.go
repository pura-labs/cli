package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pura-labs/cli/internal/api"
)

func TestEditCommand_UpdateTitle(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != "/api/p/@_/abc123" {
			t.Errorf("expected /api/p/@_/abc123, got %s", r.URL.Path)
		}

		var req api.UpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Title != "New Title" {
			t.Errorf("title = %q, want %q", req.Title, "New Title")
		}

		resp := api.ApiResponse[api.DocResponse]{
			OK: true,
			Data: api.DocResponse{
				Slug:      "abc123",
				Kind:      "doc",
				Substrate: "markdown",
				Title:     "New Title",
				Theme:     "default",
				CreatedAt: "2025-01-15T10:00:00Z",
				UpdatedAt: "2025-01-15T11:00:00Z",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"edit", "abc123", "--title", "New Title", "--handle", "_", "--api-url", server.URL, "--token", "tok_test"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("edit command failed: %v", err)
	}
}

func TestEditCommand_NoToken(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	cmd := rootCmd
	cmd.SetArgs([]string{"edit", "abc123", "--title", "Test", "--handle", "_", "--api-url", "http://localhost:0", "--token", ""})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error when no token is set")
	}
}

func TestEditCommand_NothingToUpdate(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	cmd := rootCmd
	cmd.SetArgs([]string{"edit", "abc123", "--handle", "_", "--api-url", "http://localhost:0", "--token", "tok_test"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error when nothing to update")
	}
}

func TestEditCommand_UsesExplicitNamespacedSlug(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/p/@alice/abc123" {
			t.Errorf("expected /api/p/@alice/abc123, got %s", r.URL.Path)
		}
		resp := api.ApiResponse[api.DocResponse]{
			OK: true,
			Data: api.DocResponse{
				Slug:      "abc123",
				Kind:      "doc",
				Substrate: "markdown",
				Title:     "New Title",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"edit", "@alice/abc123", "--title", "New Title", "--api-url", server.URL, "--token", "tok_test"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("edit command failed: %v", err)
	}
}
