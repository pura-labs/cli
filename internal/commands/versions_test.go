package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// versionsFake mirrors /api/p/@:handle/:slug/versions[/<N>[/restore]].
type versionsFake struct {
	srv      *httptest.Server
	items    []map[string]any
	contents map[int]string
}

func newVersionsFake(t *testing.T, slug, handle string) *versionsFake {
	t.Helper()
	fs := &versionsFake{contents: map[int]string{}}
	mux := http.NewServeMux()

	base := fmt.Sprintf("/api/p/%%40%s/%s", handle, slug)
	_ = base // some Hono matchers escape the @, some don't; we register both.

	// GET /api/p/@h/s/versions
	list := func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": fs.items})
	}
	// GET /api/p/@h/s/versions/<N>  (or POST restore)
	one := func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) < 2 {
			w.WriteHeader(404)
			return
		}
		if r.Method == http.MethodPost {
			// restore path: .../versions/<N>/restore
			// parts: ["", "api", "p", "@handle", "slug", "versions", "N", "restore"]
			if len(parts) < 8 {
				w.WriteHeader(404)
				return
			}
			var n int
			fmt.Sscanf(parts[6], "%d", &n)
			content, ok := fs.contents[n]
			if !ok {
				w.WriteHeader(404)
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]string{"code": "not_found", "message": "nope"}})
				return
			}
			// New version = max+1
			newV := 0
			for _, it := range fs.items {
				if v, _ := it["version"].(float64); int(v) > newV {
					newV = int(v)
				}
			}
			newV++
			fs.contents[newV] = content
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"data": map[string]any{
					"id":      "v_restored",
					"version": newV,
					"content": content,
					"origin":  fmt.Sprintf("restore:v%d", n),
				},
			})
			return
		}
		// GET single: parts[6] is N
		if len(parts) < 7 {
			w.WriteHeader(404)
			return
		}
		var n int
		fmt.Sscanf(parts[6], "%d", &n)
		content, ok := fs.contents[n]
		if !ok {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]string{"code": "not_found", "message": "version not found"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"data": map[string]any{
				"id":      fmt.Sprintf("v_%d", n),
				"version": n,
				"content": content,
			},
		})
	}

	// Dispatch by URL shape.
	mux.HandleFunc(fmt.Sprintf("/api/p/@%s/%s/versions", handle, slug), list)
	mux.HandleFunc(fmt.Sprintf("/api/p/@%s/%s/versions/", handle, slug), one)

	fs.srv = httptest.NewServer(mux)
	t.Cleanup(fs.srv.Close)
	return fs
}

func (fs *versionsFake) addVersion(n int, content string) {
	fs.items = append([]map[string]any{
		{"id": fmt.Sprintf("v_%d", n), "version": float64(n), "created_by": "manual", "origin": "", "created_at": "2026-04-17T10:00:00Z"},
	}, fs.items...)
	fs.contents[n] = content
}

// --- tests ---

func TestVersionsLs_UnauthorizedShortCircuits(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = "http://127.0.0.1:1"
	_, err := runCmd(t, "versions", "ls", "abc")
	if err == nil || err.Error() != "no token" {
		t.Fatalf("want no token, got %v", err)
	}
}

func TestVersionsLs_RendersList(t *testing.T) {
	setupIsolatedHome(t)
	fs := newVersionsFake(t, "xy12", "_")
	fs.addVersion(1, "hello\n")
	fs.addVersion(2, "hello world\n")

	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_t"

	out, err := runCmd(t, "versions", "ls", "xy12", "--json")
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("want 2 versions, got %d", len(data))
	}
	if sum := env["summary"].(string); !strings.Contains(sum, "latest v2") {
		t.Errorf("summary didn't name the latest: %q", sum)
	}
}

func TestVersionsShow_ReturnsContent(t *testing.T) {
	setupIsolatedHome(t)
	fs := newVersionsFake(t, "xy12", "_")
	fs.addVersion(1, "version-one-body\n")

	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_t"

	out, err := runCmd(t, "versions", "show", "xy12", "1", "--json")
	if err != nil {
		t.Fatalf("show: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)
	if data["content"] != "version-one-body\n" {
		t.Errorf("content = %v", data["content"])
	}
}

func TestVersionsShow_BadVersionArg(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = "http://127.0.0.1:1"
	flagToken = "sk_pura_t"

	_, err := runCmd(t, "versions", "show", "xy12", "banana")
	if err == nil {
		t.Fatal("want error for non-numeric version")
	}
}

func TestVersionsShow_RejectsTrailingGarbageVersionArg(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = "http://127.0.0.1:1"
	flagToken = "sk_pura_t"

	_, err := runCmd(t, "versions", "show", "xy12", "1abc")
	if err == nil {
		t.Fatal("want error for malformed numeric version")
	}
}

func TestVersionsDiff_ComputesUnifiedDiff(t *testing.T) {
	setupIsolatedHome(t)
	fs := newVersionsFake(t, "xy12", "_")
	fs.addVersion(1, "line one\nline two\nline three\n")
	fs.addVersion(2, "line one\nline 2 changed\nline three\n")

	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_t"

	out, err := runCmd(t, "versions", "diff", "xy12", "1", "2", "--json", "--color", "never")
	if err != nil {
		t.Fatalf("diff: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)
	diff := data["diff"].(string)
	if !strings.Contains(diff, "--- v1") || !strings.Contains(diff, "+++ v2") {
		t.Errorf("missing unified headers: %q", diff)
	}
	if !strings.Contains(diff, "-line two") {
		t.Errorf("diff missing removal: %q", diff)
	}
	if !strings.Contains(diff, "+line 2 changed") {
		t.Errorf("diff missing addition: %q", diff)
	}
}

func TestVersionsDiff_BDefaultsToLatest(t *testing.T) {
	setupIsolatedHome(t)
	fs := newVersionsFake(t, "xy12", "_")
	fs.addVersion(1, "a\n")
	fs.addVersion(2, "b\n")
	fs.addVersion(3, "c\n")

	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_t"

	out, err := runCmd(t, "versions", "diff", "xy12", "1", "--json", "--color", "never")
	if err != nil {
		t.Fatalf("diff: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	if env["data"].(map[string]any)["b"].(float64) != 3 {
		t.Errorf("B should default to latest=3, got %v", env["data"].(map[string]any)["b"])
	}
}

func TestVersionsRestore_CreatesNewVersion(t *testing.T) {
	setupIsolatedHome(t)
	fs := newVersionsFake(t, "xy12", "_")
	fs.addVersion(1, "original\n")
	fs.addVersion(2, "edited\n")

	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_t"

	out, err := runCmd(t, "versions", "restore", "xy12", "1", "--yes", "--json")
	if err != nil {
		t.Fatalf("restore: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)
	// Newly-created version is v3 = max(1,2)+1.
	if data["version"].(float64) != 3 {
		t.Errorf("new version = %v, want 3", data["version"])
	}
	if data["content"] != "original\n" {
		t.Errorf("restored content lost: %v", data["content"])
	}
}

func TestVersionsRestore_NonTTYRequiresYes(t *testing.T) {
	setupIsolatedHome(t)
	fs := newVersionsFake(t, "xy12", "_")
	fs.addVersion(1, "original\n")
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_t"

	_, err := runCmd(t, "versions", "restore", "xy12", "1", "--json")
	if err == nil {
		t.Fatal("want confirmation error without --yes on non-tty")
	}
	if !strings.Contains(err.Error(), "confirmation required") {
		t.Errorf("err = %v", err)
	}
}
