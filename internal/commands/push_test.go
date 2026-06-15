package commands

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pura-labs/cli/internal/api"
)

// embedTestServer records image.upload calls and captures the /api/p body so
// embed-first tests can assert what got uploaded + how content was rewritten.
type embedTestServer struct {
	uploads       int
	createBody    api.CreateRequest
	uploadHostURL string
}

func newEmbedServer(t *testing.T) (*httptest.Server, *embedTestServer) {
	state := &embedTestServer{uploadHostURL: "https://i.pura.so/u/abc.png"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tool/image.upload":
			state.uploads++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": map[string]any{
					"image_ref": "@a/x", "url": state.uploadHostURL,
					"r2_key": "assets/u/abc.png", "slug": "x", "deduped": false,
				},
			})
		case "/api/p":
			_ = json.NewDecoder(r.Body).Decode(&state.createBody)
			_ = json.NewEncoder(w).Encode(api.ApiResponse[api.CreateResponse]{
				OK:   true,
				Data: api.CreateResponse{Slug: "doc1", Token: "t", URL: "https://pura.so/@a/doc1", Kind: "doc", Substrate: "markdown"},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	return srv, state
}

func TestPushCommand_EmbedsLocalImage(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()
	t.Setenv("HOME", t.TempDir())

	srv, state := newEmbedServer(t)
	defer srv.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), []byte("PNGDATA"), 0644); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "post.md")
	if err := os.WriteFile(doc, []byte("# T\n\n![hero](./pic.png)\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"push", doc, "--api-url", srv.URL, "--token", "tok"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("push: %v", err)
	}
	if state.uploads != 1 {
		t.Fatalf("image.upload calls = %d, want 1", state.uploads)
	}
	if !strings.Contains(state.createBody.Content, state.uploadHostURL) {
		t.Fatalf("content not rewritten to host URL: %q", state.createBody.Content)
	}
	if strings.Contains(state.createBody.Content, "./pic.png") {
		t.Fatalf("local path still present: %q", state.createBody.Content)
	}
}

func TestPushCommand_EmbedDedupsRepeatedImage(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()
	t.Setenv("HOME", t.TempDir())

	srv, state := newEmbedServer(t)
	defer srv.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), []byte("PNGDATA"), 0644); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "post.md")
	if err := os.WriteFile(doc, []byte("![a](./pic.png)\n\n![b](./pic.png)\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"push", doc, "--api-url", srv.URL, "--token", "tok"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("push: %v", err)
	}
	if state.uploads != 1 {
		t.Fatalf("image.upload calls = %d, want 1 (deduped)", state.uploads)
	}
	if strings.Count(state.createBody.Content, state.uploadHostURL) != 2 {
		t.Fatalf("both refs should be rewritten: %q", state.createBody.Content)
	}
}

func TestPushCommand_ExternalURLLeftUntouched(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()
	t.Setenv("HOME", t.TempDir())

	var uploads int
	var createBody api.CreateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tool/image.upload":
			uploads++
			t.Error("external URL must not be uploaded by the CLI")
		case "/api/p":
			_ = json.NewDecoder(r.Body).Decode(&createBody)
			_ = json.NewEncoder(w).Encode(api.ApiResponse[api.CreateResponse]{
				OK: true, Data: api.CreateResponse{Slug: "d", Token: "t", URL: "https://pura.so/@a/d", Kind: "doc", Substrate: "markdown"},
			})
		}
	}))
	defer srv.Close()

	doc := filepath.Join(t.TempDir(), "post.md")
	if err := os.WriteFile(doc, []byte("![x](https://example.com/x.png)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := rootCmd
	cmd.SetArgs([]string{"push", doc, "--api-url", srv.URL, "--token", "tok"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("push: %v", err)
	}
	if uploads != 0 {
		t.Fatalf("uploads = %d, want 0", uploads)
	}
	if !strings.Contains(createBody.Content, "https://example.com/x.png") {
		t.Fatalf("external URL should survive: %q", createBody.Content)
	}
}

func TestPushCommand_CSVDoesNotEmbedMarkdownLookingImage(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()
	t.Setenv("HOME", t.TempDir())

	var uploads int
	var createBody api.CreateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tool/image.upload":
			uploads++
			t.Error("csv content must not be scanned for local markdown images")
		case "/api/p":
			_ = json.NewDecoder(r.Body).Decode(&createBody)
			_ = json.NewEncoder(w).Encode(api.ApiResponse[api.CreateResponse]{
				OK: true, Data: api.CreateResponse{Slug: "d", Token: "t", URL: "https://pura.so/@a/d", Kind: "sheet", Substrate: "csv"},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	doc := filepath.Join(t.TempDir(), "rows.csv")
	content := "name,note\nalice,![x](./missing.png)\n"
	if err := os.WriteFile(doc, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := rootCmd
	cmd.SetArgs([]string{"push", doc, "--api-url", srv.URL, "--token", "tok"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("push csv: %v", err)
	}
	if uploads != 0 {
		t.Fatalf("uploads = %d, want 0", uploads)
	}
	if createBody.Content != strings.TrimSpace(content) {
		t.Fatalf("content should be unchanged:\n got %q\nwant %q", createBody.Content, strings.TrimSpace(content))
	}
	if createBody.Substrate != "csv" {
		t.Fatalf("Substrate = %q, want csv", createBody.Substrate)
	}
}

func TestPushCommand_SheetKindDoesNotEmbedMarkdownLookingImage(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()
	t.Setenv("HOME", t.TempDir())

	var uploads int
	var createBody api.CreateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tool/image.upload":
			uploads++
			t.Error("sheet kind must not be scanned for local markdown images")
		case "/api/p":
			_ = json.NewDecoder(r.Body).Decode(&createBody)
			_ = json.NewEncoder(w).Encode(api.ApiResponse[api.CreateResponse]{
				OK: true, Data: api.CreateResponse{Slug: "s", Token: "t", URL: "https://pura.so/@a/s", Kind: "sheet", Substrate: "csv"},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	doc := filepath.Join(t.TempDir(), "rows.md")
	content := "![x](./missing.png)\n"
	if err := os.WriteFile(doc, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := rootCmd
	cmd.SetArgs([]string{"push", doc, "--kind", "sheet", "--api-url", srv.URL, "--token", "tok"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("push --kind sheet: %v", err)
	}
	if uploads != 0 {
		t.Fatalf("uploads = %d, want 0", uploads)
	}
	if createBody.Content != strings.TrimSpace(content) {
		t.Fatalf("content should be unchanged:\n got %q\nwant %q", createBody.Content, strings.TrimSpace(content))
	}
	if createBody.Kind != "sheet" {
		t.Fatalf("Kind = %q, want sheet", createBody.Kind)
	}
	if createBody.Substrate != "" {
		t.Fatalf("Substrate = %q, want empty when only --kind is provided", createBody.Substrate)
	}
}

func TestPushCommand_NoEmbedLeavesLocalPaths(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()
	t.Setenv("HOME", t.TempDir())

	srv, state := newEmbedServer(t)
	defer srv.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pic.png"), []byte("PNGDATA"), 0644); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "post.md")
	if err := os.WriteFile(doc, []byte("![a](./pic.png)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := rootCmd
	cmd.SetArgs([]string{"push", doc, "--api-url", srv.URL, "--token", "tok", "--no-embed"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("push: %v", err)
	}
	if state.uploads != 0 {
		t.Fatalf("--no-embed should skip uploads, got %d", state.uploads)
	}
	if !strings.Contains(state.createBody.Content, "./pic.png") {
		t.Fatalf("--no-embed should keep local path: %q", state.createBody.Content)
	}
}

func TestPushCommand_EmbedErrorsOnMissingRelativeImage(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()
	t.Setenv("HOME", t.TempDir())

	var createCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/p" {
			createCalled = true
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
	}))
	defer srv.Close()

	// ./missing.png is a relative ref that doesn't resolve — fail rather than
	// publish a dead link.
	doc := filepath.Join(t.TempDir(), "post.md")
	if err := os.WriteFile(doc, []byte("![x](./missing.png)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := rootCmd
	cmd.SetArgs([]string{"push", doc, "--api-url", srv.URL, "--token", "tok"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for a missing relative local image")
	}
	if createCalled {
		t.Fatal("doc must NOT be published when a local image is missing")
	}
}

func TestPushCommand_EmbedLeavesAbsoluteWebPath(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()
	t.Setenv("HOME", t.TempDir())

	srv, state := newEmbedServer(t)
	defer srv.Close()

	// /static/logo.png is absolute and doesn't resolve to a file — ambiguous
	// (likely a site-root web path), so it's left untouched, NOT an error.
	doc := filepath.Join(t.TempDir(), "post.md")
	if err := os.WriteFile(doc, []byte("![x](/static/logo.png)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := rootCmd
	cmd.SetArgs([]string{"push", doc, "--api-url", srv.URL, "--token", "tok"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("absolute web path should not fail the push: %v", err)
	}
	if state.uploads != 0 {
		t.Fatalf("uploads = %d, want 0", state.uploads)
	}
	if !strings.Contains(state.createBody.Content, "/static/logo.png") {
		t.Fatalf("absolute web path should survive: %q", state.createBody.Content)
	}
}

func TestPushCommand_EmbedErrorsOnOversizeLocalImage(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()
	t.Setenv("HOME", t.TempDir())

	var createCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/p" {
			createCalled = true
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	big := make([]byte, (10<<20)+1) // just over the 10 MiB client cap
	copy(big, []byte("\x89PNG\r\n\x1a\n"))
	if err := os.WriteFile(filepath.Join(dir, "huge.png"), big, 0644); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(dir, "post.md")
	if err := os.WriteFile(doc, []byte("![x](./huge.png)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := rootCmd
	cmd.SetArgs([]string{"push", doc, "--api-url", srv.URL, "--token", "tok"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for an oversize local image")
	}
	if createCalled {
		t.Fatal("doc must NOT be published when a local image is oversize")
	}
}

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
				"image_ref":  "@alice/photo",
				"url":        "https://pura.so/@alice/photo",
				"r2_key":     "assets/u/photo.jpg",
				"r2_deduped": false,
				"slug":       "photo",
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
				"file_ref":   "@alice/data",
				"url":        "https://pura.so/@alice/data",
				"r2_key":     "assets/u/data.csv",
				"r2_deduped": false,
				"slug":       "data",
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
	t.Setenv("HOME", t.TempDir()) // isolate from the dev machine's real credentials

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
