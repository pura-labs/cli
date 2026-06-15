package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImageLs_Success(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tool/image.list" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"total": 2,
				"images": []map[string]any{
					{"slug": "cat", "title": "Cat", "url": "https://i.pura.so/u/cat.png", "bytes": 2048, "width": 100, "height": 80, "usage_count": 1, "updated_at": "2026-06-15T00:00:00Z"},
					{"slug": "dog", "filename": "dog.png", "url": "https://i.pura.so/u/dog.png", "bytes": 1024, "usage_count": 0, "updated_at": "2026-06-14T00:00:00Z"},
				},
			},
		})
	}))
	defer srv.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"image", "ls", "--api-url", srv.URL, "--token", "tok", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("image ls: %v", err)
	}
	if sawAuth != "Bearer tok" {
		t.Fatalf("Authorization = %q, want Bearer tok", sawAuth)
	}
}

func TestImageLs_RequiresAuth(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()
	t.Setenv("HOME", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected network call to %s", r.URL.Path)
	}))
	defer srv.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"image", "ls", "--api-url", srv.URL})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected unauthorized error without a token")
	}
}

func TestImageRm_Success(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	var gotMethod, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.WriteHeader(204)
	}))
	defer srv.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"image", "rm", "photo", "--handle", "alice", "--yes", "--api-url", srv.URL, "--token", "tok"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("image rm: %v", err)
	}
	if gotMethod != "DELETE" || gotPath != "/api/p/@alice/photo" {
		t.Fatalf("got %s %s, want DELETE /api/p/@alice/photo", gotMethod, gotPath)
	}
	if gotQuery != "" {
		t.Fatalf("force query should be absent, got %q", gotQuery)
	}
}

func TestImageRm_RefcountBlocked(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	var sawForce bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "force") {
			sawForce = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": false,
			"error": map[string]any{
				"code":    "in_use",
				"message": "Image is embedded in 1 document(s).",
				"consumers": []map[string]any{
					{"handle": "alice", "slug": "my-post", "title": "My Post"},
				},
			},
		})
	}))
	defer srv.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"image", "rm", "photo", "--handle", "alice", "--yes", "--api-url", srv.URL, "--token", "tok"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected in_use error blocking the delete")
	}
	if sawForce {
		t.Fatal("must NOT send force when only --yes was passed")
	}
}

func TestImageRm_Force(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	var sawForce bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "force=1") {
			sawForce = true
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"image", "rm", "photo", "--handle", "alice", "--force", "--api-url", srv.URL, "--token", "tok"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("image rm --force: %v", err)
	}
	if !sawForce {
		t.Fatal("--force must send ?force=1")
	}
}

func TestImageGet_Success(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tool/image.read" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if got := body["image_ref"]; got != "@alice/photo" {
			t.Fatalf("image_ref = %v, want @alice/photo", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"url": "https://i.pura.so/u/photo.png", "r2_key": "assets/u/photo.png",
				"mime": "image/png", "bytes": 4096, "width": 200, "height": 150,
				"alt": "a photo", "tags": []string{"x"}, "filename": "photo.png", "title": "Photo",
			},
		})
	}))
	defer srv.Close()

	cmd := rootCmd
	cmd.SetArgs([]string{"image", "get", "photo", "--handle", "alice", "--api-url", srv.URL, "--token", "tok", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("image get: %v", err)
	}
}
