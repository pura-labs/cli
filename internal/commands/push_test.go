package commands

import (
	"encoding/base64"
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

func TestPushCommand_ImageAssetUsesUploadTool(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	original := []byte{0xff, 0xd8, 0xff, 0xd9}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/p" {
			t.Fatal("image asset push should not call /api/p")
		}
		if r.URL.Path != "/api/tool/image.upload" {
			t.Fatalf("expected /api/tool/image.upload, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok_existing" {
			t.Fatalf("Authorization = %q, want Bearer tok_existing", got)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := body["mime"]; got != "image/jpeg" {
			t.Fatalf("mime = %v, want image/jpeg", got)
		}
		if got := body["filename"]; got != "photo.jpg" {
			t.Fatalf("filename = %v, want photo.jpg", got)
		}
		encoded, _ := body["content_base64"].(string)
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("decode base64: %v", err)
		}
		if string(decoded) != string(original) {
			t.Fatalf("decoded body = %v, want %v", decoded, original)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"image_ref":   "@alice/photo",
				"url":         "https://pura.so/@alice/photo",
				"r2_key":      "assets/u/photo.jpg",
				"r2_deduped":  false,
				"slug":        "photo",
			},
		})
	}))
	defer server.Close()

	tmpFile := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(tmpFile, original, 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"push", tmpFile, "--api-url", server.URL, "--token", "tok_existing"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("push command failed: %v", err)
	}
}

func TestPushCommand_ExplicitFileKindUsesUploadTool(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	original := []byte("a,b\n1,2\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/p" {
			t.Fatal("file asset push should not call /api/p")
		}
		if r.URL.Path != "/api/tool/file.upload" {
			t.Fatalf("expected /api/tool/file.upload, got %s", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := body["mime"]; got != "text/csv" {
			t.Fatalf("mime = %v, want text/csv", got)
		}
		encoded, _ := body["content_base64"].(string)
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("decode base64: %v", err)
		}
		if string(decoded) != string(original) {
			t.Fatalf("decoded body = %q, want %q", string(decoded), string(original))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"file_ref":    "@alice/data",
				"url":         "https://pura.so/@alice/data",
				"r2_key":      "assets/u/data.csv",
				"r2_deduped":  false,
				"slug":        "data",
			},
		})
	}))
	defer server.Close()

	tmpFile := filepath.Join(t.TempDir(), "data.csv")
	if err := os.WriteFile(tmpFile, original, 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"push", tmpFile, "--kind", "file", "--api-url", server.URL, "--token", "tok_existing"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("push command failed: %v", err)
	}
}

func TestPushCommand_AssetUploadRequiresAuth(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected network call to %s", r.URL.Path)
	}))
	defer server.Close()

	tmpFile := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(tmpFile, []byte{0xff, 0xd8, 0xff, 0xd9}, 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"push", tmpFile, "--api-url", server.URL})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected auth error for asset upload without token")
	}
}
