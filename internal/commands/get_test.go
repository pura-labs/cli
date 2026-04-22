package commands

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/pura-labs/cli/internal/api"
)

func TestGetCommand_Meta(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/p/@_/abc123" {
			t.Errorf("expected /api/p/@_/abc123, got %s", r.URL.Path)
		}

		resp := api.ApiResponse[api.DocResponse]{
			OK: true,
			Data: api.DocResponse{
				Slug:      "abc123",
				Kind:      "doc",
				Substrate: "markdown",
				Title:     "Test",
				Theme:     "default",
				CreatedAt: "2025-01-15T10:00:00Z",
				UpdatedAt: "2025-01-15T10:00:00Z",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"get", "abc123", "--handle", "_", "--api-url", server.URL})
	if err := cmd.Execute(); err != nil {
		t.Errorf("get command failed: %v", err)
	}
}

func TestGetCommand_Raw(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/@_/abc123/raw" {
			w.Write([]byte("# Hello World"))
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"get", "abc123", "-f", "raw", "--handle", "_", "--api-url", server.URL})
	if err := cmd.Execute(); err != nil {
		t.Errorf("get raw command failed: %v", err)
	}
}

func TestGetCommand_NotFound(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := api.ApiResponse[api.DocResponse]{
			OK: false,
			Error: &api.ApiError{
				Code:    "not_found",
				Message: "Document not found",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"get", "nonexistent", "--handle", "_", "--api-url", server.URL})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for not found document")
	}
}

func TestGetCommand_RawQuietOutputsRawBody(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/@_/abc123/raw" {
			w.Write([]byte("# Hello World"))
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() {
		os.Stdout = origStdout
	}()
	os.Stdout = w

	cmd := rootCmd
	cmd.SetArgs([]string{"get", "abc123", "-f", "raw", "--quiet", "--handle", "_", "--api-url", server.URL})
	runErr := cmd.Execute()
	_ = w.Close()
	if runErr != nil {
		t.Fatalf("get raw quiet failed: %v", runErr)
	}

	var out bytes.Buffer
	if _, err := out.ReadFrom(r); err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}
	if got := out.String(); got != "# Hello World" {
		t.Fatalf("quiet raw output = %q, want raw body", got)
	}
}

func TestGetCommand_CtxJSONEnvelope(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/@_/abc123/ctx" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"summary":"hello"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer func() {
		os.Stdout = origStdout
	}()
	os.Stdout = w

	cmd := rootCmd
	cmd.SetArgs([]string{"get", "abc123", "-f", "ctx", "--json", "--handle", "_", "--api-url", server.URL})
	runErr := cmd.Execute()
	_ = w.Close()
	if runErr != nil {
		t.Fatalf("get ctx json failed: %v", runErr)
	}

	var out bytes.Buffer
	if _, err := out.ReadFrom(r); err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}

	var parsed struct {
		OK   bool `json:"ok"`
		Data struct {
			Slug    string         `json:"slug"`
			Format  string         `json:"format"`
			Context map[string]any `json:"context"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.Bytes())
	}
	if !parsed.OK || parsed.Data.Format != "ctx" || parsed.Data.Context["summary"] != "hello" {
		t.Fatalf("unexpected envelope: %+v", parsed)
	}
}
