package commands

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Shared stub — accepts both /api/p (for book create) and /api/tool/<name>.
type bookStub struct {
	lastPath string
	lastBody map[string]any
	respond  func(path string, body map[string]any) (any, int)
}

func newBookStub(t *testing.T) (*httptest.Server, *bookStub) {
	t.Helper()
	s := &bookStub{
		respond: func(_ string, _ map[string]any) (any, int) {
			return map[string]any{"ok": true, "result": map[string]any{}}, 200
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.lastPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		s.lastBody = parsed
		resp, status := s.respond(r.URL.Path, parsed)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

// ─── `pura book create` ─────────────────────────────────────────────────

func TestBook_Create_PostsToApiPWithKindBook(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newBookStub(t)
	flagAPIURL = srv.URL

	stub.respond = func(path string, _ map[string]any) (any, int) {
		if path != "/api/p" {
			return map[string]any{"ok": false, "error": map[string]any{"code": "route_error"}}, 404
		}
		return map[string]any{
			"ok": true,
			"data": map[string]any{
				"slug":      "my-notes",
				"token":     "sk_test",
				"url":       "http://localhost/@_/my-notes",
				"raw_url":   "http://localhost/@_/my-notes/raw",
				"ctx_url":   "http://localhost/@_/my-notes/ctx",
				"kind":      "book",
				"substrate": "refs",
				"title":     "My Notes",
			},
		}, 201
	}
	resetBookFlags()

	out, err := runCmd(t, "book", "create", "@_/my-notes",
		"--title", "My Notes",
		"--subtitle", "A test",
		"--author", "alice",
		"--json")
	if err != nil {
		t.Fatalf("book create: %v\n%s", err, out)
	}
	if stub.lastPath != "/api/p" {
		t.Errorf("path = %q, want /api/p", stub.lastPath)
	}
	if stub.lastBody["kind"] != "book" {
		t.Errorf("kind = %v, want book", stub.lastBody["kind"])
	}
	if stub.lastBody["slug"] != "my-notes" {
		t.Errorf("slug = %v, want my-notes", stub.lastBody["slug"])
	}
	// Empty content is required for book creation — server now accepts it.
	if stub.lastBody["content"] != "" {
		t.Errorf("content = %q, want \"\"", stub.lastBody["content"])
	}
	// Metadata carries subtitle + author under the dedicated keys.
	meta, _ := stub.lastBody["metadata"].(map[string]any)
	if meta["subtitle"] != "A test" {
		t.Errorf("metadata.subtitle = %v", meta["subtitle"])
	}
	if meta["author"] != "alice" {
		t.Errorf("metadata.author = %v", meta["author"])
	}
}

func TestBook_Create_RejectsMalformedRef(t *testing.T) {
	setupIsolatedHome(t)
	srv, _ := newBookStub(t)
	flagAPIURL = srv.URL
	resetBookFlags()

	_, err := runCmd(t, "book", "create", "@/slug", "--title", "x")
	if err == nil {
		t.Fatalf("expected error on malformed ref")
	}
}

// ─── `pura book read` ───────────────────────────────────────────────────

func TestBook_Read_CallsBookReadTool(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newBookStub(t)
	flagAPIURL = srv.URL

	stub.respond = func(path string, _ map[string]any) (any, int) {
		if !strings.HasPrefix(path, "/api/tool/book.read") {
			return map[string]any{"ok": false, "error": map[string]any{"code": "route_error"}}, 404
		}
		return map[string]any{
			"ok": true,
			"result": map[string]any{
				"title":         "Manual",
				"owner":         "_",
				"version":       float64(1),
				"chapter_count": float64(2),
				"metadata":      map[string]any{"subtitle": "Guide", "author": "alice"},
				"chapters": []any{
					map[string]any{"child_item_id": "a", "ref": "@_/intro", "kind": "doc", "title": "Intro", "position_score": float64(1)},
					map[string]any{"child_item_id": "b", "ref": "@_/install", "kind": "doc", "title": "Install", "position_score": float64(2)},
				},
			},
		}, 200
	}
	resetBookFlags()

	out, err := runCmd(t, "book", "read", "@_/manual", "--json")
	if err != nil {
		t.Fatalf("book read: %v\n%s", err, out)
	}
	if stub.lastPath != "/api/tool/book.read" {
		t.Errorf("path = %q, want /api/tool/book.read", stub.lastPath)
	}
	if stub.lastBody["book_ref"] != "@_/manual" {
		t.Errorf("book_ref = %v", stub.lastBody["book_ref"])
	}
}

// ─── `pura book add` ────────────────────────────────────────────────────

func TestBook_Add_ForwardsPositionAndAnchor(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newBookStub(t)
	flagAPIURL = srv.URL

	stub.respond = func(_ string, _ map[string]any) (any, int) {
		return map[string]any{
			"ok": true,
			"result": map[string]any{
				"leaf_id":        float64(1),
				"position_score": float64(1.5),
				"chapter_count":  float64(3),
			},
		}, 200
	}
	resetBookFlags()

	out, err := runCmd(t, "book", "add", "@_/manual", "@_/install",
		"--position", "after",
		"--anchor", "@_/intro",
		"--json")
	if err != nil {
		t.Fatalf("book add: %v\n%s", err, out)
	}
	if stub.lastPath != "/api/tool/book.add_chapter" {
		t.Errorf("path = %q", stub.lastPath)
	}
	if stub.lastBody["book_ref"] != "@_/manual" {
		t.Errorf("book_ref = %v", stub.lastBody["book_ref"])
	}
	if stub.lastBody["child_ref"] != "@_/install" {
		t.Errorf("child_ref = %v", stub.lastBody["child_ref"])
	}
	if stub.lastBody["position"] != "after" {
		t.Errorf("position = %v", stub.lastBody["position"])
	}
	if stub.lastBody["anchor_ref"] != "@_/intro" {
		t.Errorf("anchor_ref = %v", stub.lastBody["anchor_ref"])
	}
}

func TestBook_Add_DispatchErrorSurfaces(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newBookStub(t)
	flagAPIURL = srv.URL

	stub.respond = func(_ string, _ map[string]any) (any, int) {
		return map[string]any{
			"ok": false,
			"error": map[string]any{
				"code":       "validation_failed",
				"field":      "child_ref",
				"suggestion": "a book cannot contain itself",
			},
		}, 400
	}
	resetBookFlags()

	_, err := runCmd(t, "book", "add", "@_/manual", "@_/manual")
	if err == nil {
		t.Fatalf("expected validation_failed")
	}
	if !strings.Contains(err.Error(), "validation_failed") {
		t.Errorf("error = %v, want validation_failed", err)
	}
}

// ─── `pura book rm` ─────────────────────────────────────────────────────

func TestBook_Rm_CallsRemoveChapter(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newBookStub(t)
	flagAPIURL = srv.URL

	stub.respond = func(_ string, _ map[string]any) (any, int) {
		return map[string]any{
			"ok": true,
			"result": map[string]any{
				"removed":       true,
				"chapter_count": float64(1),
			},
		}, 200
	}
	resetBookFlags()

	_, err := runCmd(t, "book", "rm", "@_/manual", "@_/intro", "--json")
	if err != nil {
		t.Fatalf("book rm: %v", err)
	}
	if stub.lastPath != "/api/tool/book.remove_chapter" {
		t.Errorf("path = %q", stub.lastPath)
	}
}

// ─── `pura book reorder` ────────────────────────────────────────────────

func TestBook_Reorder_ForwardsRefsArray(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newBookStub(t)
	flagAPIURL = srv.URL

	stub.respond = func(_ string, _ map[string]any) (any, int) {
		return map[string]any{
			"ok": true,
			"result": map[string]any{
				"chapter_count": float64(3),
				"reordered":     float64(3),
			},
		}, 200
	}
	resetBookFlags()

	_, err := runCmd(t, "book", "reorder", "@_/manual",
		"@_/install", "@_/intro", "@_/usage", "--json")
	if err != nil {
		t.Fatalf("book reorder: %v", err)
	}
	refs, ok := stub.lastBody["ordered_child_refs"].([]any)
	if !ok {
		t.Fatalf("ordered_child_refs missing or wrong type: %v", stub.lastBody["ordered_child_refs"])
	}
	if len(refs) != 3 || refs[0] != "@_/install" || refs[2] != "@_/usage" {
		t.Errorf("refs = %v", refs)
	}
}

// ─── `pura book export` ─────────────────────────────────────────────────

func TestBook_Export_ReturnsContent(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newBookStub(t)
	flagAPIURL = srv.URL

	stub.respond = func(_ string, _ map[string]any) (any, int) {
		return map[string]any{
			"ok": true,
			"result": map[string]any{
				"format":        "markdown",
				"content":       "# Manual\n\n---\n\n## Intro\n\nbody",
				"bytes":         float64(33),
				"chapter_count": float64(1),
			},
		}, 200
	}
	resetBookFlags()

	out, err := runCmd(t, "book", "export", "@_/manual", "--format", "markdown", "--json")
	if err != nil {
		t.Fatalf("book export: %v", err)
	}
	if stub.lastBody["format"] != "markdown" {
		t.Errorf("format = %v", stub.lastBody["format"])
	}
	// When --json is set the envelope JSON carries the content.
	if !strings.Contains(string(out), "Manual") {
		t.Errorf("output missing book title; got %s", out)
	}
}

// ─── splitRef helper ────────────────────────────────────────────────────

func TestSplitRef(t *testing.T) {
	cases := []struct {
		in, slug, handle string
		err              bool
	}{
		{"@alice/manual", "manual", "alice", false},
		{"alice/manual", "manual", "alice", false},
		{"@_/notes", "notes", "_", false},
		{"bare-slug", "bare-slug", "", false},
		{"", "", "", true},
		{"@/slug", "", "", true},
		{"@alice/", "", "", true},
	}
	for _, c := range cases {
		slug, handle, err := splitRef(c.in)
		if c.err && err == nil {
			t.Errorf("splitRef(%q) expected error, got slug=%q handle=%q", c.in, slug, handle)
			continue
		}
		if !c.err && err != nil {
			t.Errorf("splitRef(%q) unexpected error: %v", c.in, err)
			continue
		}
		if !c.err && (slug != c.slug || handle != c.handle) {
			t.Errorf("splitRef(%q) = (%q, %q), want (%q, %q)", c.in, slug, handle, c.slug, c.handle)
		}
	}
}
