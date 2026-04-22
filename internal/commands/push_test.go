package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/pura-labs/cli/internal/api"
)

func TestPushCommand_Success(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	var gotReq api.CreateRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/p" {
			t.Errorf("expected /api/p, got %s", r.URL.Path)
		}

		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if gotReq.Content == "" {
			t.Error("content should not be empty")
		}

		resp := api.ApiResponse[api.CreateResponse]{
			OK: true,
			Data: api.CreateResponse{
				Slug:      "abc123",
				Token:     "tok_test",
				URL:       "https://pura.so/abc123",
				Kind:      "doc",
				Substrate: "markdown",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create a temp file to push
	tmpFile := t.TempDir() + "/test.md"
	if err := os.WriteFile(tmpFile, []byte("# Hello World"), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"push", tmpFile, "--api-url", server.URL, "--token", "tok_existing"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("push command failed: %v", err)
	}
	if gotReq.Substrate != "markdown" {
		t.Fatalf("CreateRequest.Substrate = %q, want markdown", gotReq.Substrate)
	}
	if gotReq.Kind != "" {
		t.Fatalf("CreateRequest.Kind = %q, want empty", gotReq.Kind)
	}
}

func TestPushCommand_KindFlagPreservesExplicitKindWithoutAutoSubstrate(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	var gotReq api.CreateRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		resp := api.ApiResponse[api.CreateResponse]{
			OK: true,
			Data: api.CreateResponse{
				Slug:      "grid123",
				Token:     "tok_test",
				URL:       "https://pura.so/grid123",
				Kind:      "sheet",
				Substrate: "csv",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	tmpFile := filepath.Join(t.TempDir(), "rows.csv")
	if err := os.WriteFile(tmpFile, []byte("a,b\n1,2\n"), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"push", tmpFile, "--kind", "sheet", "--api-url", server.URL, "--token", "tok_existing"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("push command failed: %v", err)
	}
	if gotReq.Kind != "sheet" {
		t.Fatalf("CreateRequest.Kind = %q, want sheet", gotReq.Kind)
	}
	if gotReq.Substrate != "" {
		t.Fatalf("CreateRequest.Substrate = %q, want empty when only --kind is provided", gotReq.Substrate)
	}
}

func TestPushCommand_EmptyContent(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	tmpFile := t.TempDir() + "/empty.md"
	if err := os.WriteFile(tmpFile, []byte(""), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"push", tmpFile, "--api-url", "http://localhost:0"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for empty content")
	}
}

func TestPushCommand_FileNotFound(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	cmd := rootCmd
	cmd.SetArgs([]string{"push", "/nonexistent/file.md", "--api-url", "http://localhost:0"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestPushCommand_AutoSavesHandleFromPublishedURL(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := api.ApiResponse[api.CreateResponse]{
			OK: true,
			Data: api.CreateResponse{
				Slug:      "abc123",
				Token:     "tok_test",
				URL:       "https://pura.so/@alice/abc123",
				Kind:      "doc",
				Substrate: "markdown",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	tmpFile := filepath.Join(t.TempDir(), "test.md")
	if err := os.WriteFile(tmpFile, []byte("# Hello World"), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"push", tmpFile, "--api-url", server.URL})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("push command failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".config", "pura", "config.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if got := cfg["handle"]; got != "alice" {
		t.Fatalf("saved handle = %v, want alice", got)
	}
}
