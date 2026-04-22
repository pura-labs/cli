package commands

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pura-labs/cli/internal/api"
)

// ─── fake servers ───────────────────────────────────────────────────────

type toolCallRecord struct {
	Path    string
	Query   string
	Headers http.Header
	Body    string
}

type fakeDispatcher struct {
	mu        *atomic.Int32
	calls     chan toolCallRecord
	toolsList []any // list returned by /mcp tools/list
	// respond(call) → (body, status)
	respond func(rec toolCallRecord) (any, int)
}

func newFakeDispatcher(t *testing.T) (*httptest.Server, *fakeDispatcher) {
	t.Helper()
	fd := &fakeDispatcher{
		mu:    &atomic.Int32{},
		calls: make(chan toolCallRecord, 32),
		toolsList: []any{
			map[string]any{
				"name":        "sheet.list_rows",
				"description": "List rows\n\nReturns up to limit rows.",
				"inputSchema": map[string]any{"type": "object"},
			},
			map[string]any{
				"name":        "sheet.append_row",
				"description": "Append one row",
				"inputSchema": map[string]any{
					"type":     "object",
					"required": []any{"sheet_ref", "values"},
				},
			},
		},
		respond: func(_ toolCallRecord) (any, int) {
			return map[string]any{"ok": true, "result": map[string]any{"echoed": true}}, 200
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec := toolCallRecord{
			Path:    r.URL.Path,
			Query:   r.URL.RawQuery,
			Headers: r.Header.Clone(),
			Body:    string(body),
		}
		fd.calls <- rec
		fd.mu.Add(1)
		switch {
		case r.URL.Path == "/mcp" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result":  map[string]any{"tools": fd.toolsList},
			})
		case strings.HasPrefix(r.URL.Path, "/api/tool/"):
			resp, status := fd.respond(rec)
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, fd
}

// ─── `pura tool ls` ─────────────────────────────────────────────────────

func TestTool_Ls_FetchesFromServerAndCaches(t *testing.T) {
	home := setupIsolatedHome(t)
	srv, fd := newFakeDispatcher(t)
	cachePath := filepath.Join(home, "tool-cache-test.json")
	t.Setenv("PURA_TOOL_CACHE", cachePath)

	flagAPIURL = srv.URL

	// First call: fetches.
	out, err := runCmd(t, "tool", "ls", "--json")
	if err != nil {
		t.Fatalf("tool ls: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	tools, _ := env["data"].(map[string]any)["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	if src := env["data"].(map[string]any)["source"]; src != "network" {
		t.Errorf("source = %v, want network on first call", src)
	}

	// Cache file was written.
	if _, err := json.Marshal(nil); err != nil {
		t.Fatal(err)
	}

	// Second call: should hit cache, NOT the server.
	requestsBefore := fd.mu.Load()
	out2, err := runCmd(t, "tool", "ls", "--json")
	if err != nil {
		t.Fatalf("tool ls 2: %v", err)
	}
	env2 := mustUnmarshalEnvelope(t, out2)
	if src := env2["data"].(map[string]any)["source"]; src != "cache" {
		t.Errorf("source = %v, want cache on second call", src)
	}
	if fd.mu.Load() != requestsBefore {
		t.Errorf("second call hit the server (requests: before=%d after=%d)", requestsBefore, fd.mu.Load())
	}
}

func TestTool_Ls_RefreshForcesFetch(t *testing.T) {
	home := setupIsolatedHome(t)
	srv, fd := newFakeDispatcher(t)
	cachePath := filepath.Join(home, "tool-cache.json")
	t.Setenv("PURA_TOOL_CACHE", cachePath)
	flagAPIURL = srv.URL

	// Prime cache.
	if _, err := runCmd(t, "tool", "ls", "--json"); err != nil {
		t.Fatal(err)
	}
	before := fd.mu.Load()
	// --refresh should re-fetch.
	if _, err := runCmd(t, "tool", "ls", "--refresh", "--json"); err != nil {
		t.Fatal(err)
	}
	if fd.mu.Load() == before {
		t.Error("--refresh did not trigger a server call")
	}
}

// ─── `pura tool inspect` ────────────────────────────────────────────────

func TestTool_Inspect_ReturnsSchema(t *testing.T) {
	home := setupIsolatedHome(t)
	srv, _ := newFakeDispatcher(t)
	t.Setenv("PURA_TOOL_CACHE", filepath.Join(home, "c.json"))
	flagAPIURL = srv.URL

	out, err := runCmd(t, "tool", "inspect", "sheet.append_row", "--json")
	if err != nil {
		t.Fatalf("inspect: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	tool := env["data"].(map[string]any)
	if tool["name"] != "sheet.append_row" {
		t.Errorf("name = %v", tool["name"])
	}
	if tool["short_description"] == nil {
		t.Error("short_description missing")
	}
}

func TestTool_Inspect_UnknownIsError(t *testing.T) {
	home := setupIsolatedHome(t)
	srv, _ := newFakeDispatcher(t)
	t.Setenv("PURA_TOOL_CACHE", filepath.Join(home, "c.json"))
	flagAPIURL = srv.URL
	if _, err := runCmd(t, "tool", "inspect", "sheet.ghost"); err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

// ─── `pura tool call` ───────────────────────────────────────────────────

func TestTool_Call_PostsArgsAsJsonBody(t *testing.T) {
	home := setupIsolatedHome(t)
	srv, fd := newFakeDispatcher(t)
	t.Setenv("PURA_TOOL_CACHE", filepath.Join(home, "c.json"))
	flagAPIURL = srv.URL

	fd.respond = func(_ toolCallRecord) (any, int) {
		return map[string]any{
			"ok":     true,
			"result": map[string]any{"row_id": "r1", "version": 2},
		}, 200
	}

	out, err := runCmd(t, "tool", "call", "sheet.append_row",
		"--args", `{"sheet_ref":"@alice/leads","values":{"name":"Alice"}}`,
		"--json",
	)
	if err != nil {
		t.Fatalf("call: %v\n%s", err, out)
	}
	// Verify the HTTP body arrived intact and path was correct.
	var rec toolCallRecord
	select {
	case r := <-fd.calls:
		rec = r
	default:
		t.Fatal("server saw no request")
	}
	if rec.Path != "/api/tool/sheet.append_row" {
		t.Errorf("path = %s", rec.Path)
	}
	if rec.Query != "" {
		t.Errorf("query should be empty for non-dry-run: %q", rec.Query)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(rec.Body), &parsed); err != nil {
		t.Fatalf("request body not JSON: %q", rec.Body)
	}
	if parsed["sheet_ref"] != "@alice/leads" {
		t.Errorf("sheet_ref missing in forwarded body")
	}
}

func TestTool_Call_DryRunAppendsQuery(t *testing.T) {
	home := setupIsolatedHome(t)
	srv, fd := newFakeDispatcher(t)
	t.Setenv("PURA_TOOL_CACHE", filepath.Join(home, "c.json"))
	flagAPIURL = srv.URL

	if _, err := runCmd(t, "tool", "call", "sheet.append_row",
		"--dry-run", "--json",
	); err != nil {
		t.Fatalf("dry-run call: %v", err)
	}
	rec := <-fd.calls
	if !strings.Contains(rec.Query, "dry_run=1") {
		t.Errorf("missing dry_run query: %s", rec.Query)
	}
}

func TestTool_Call_ForwardsIdempotencyKey(t *testing.T) {
	home := setupIsolatedHome(t)
	srv, fd := newFakeDispatcher(t)
	t.Setenv("PURA_TOOL_CACHE", filepath.Join(home, "c.json"))
	flagAPIURL = srv.URL

	if _, err := runCmd(t, "tool", "call", "sheet.append_row",
		"--idempotency-key", "abc-123", "--json",
	); err != nil {
		t.Fatalf("call: %v", err)
	}
	rec := <-fd.calls
	if got := rec.Headers.Get("Idempotency-Key"); got != "abc-123" {
		t.Errorf("Idempotency-Key = %q", got)
	}
}

func TestTool_Call_MalformedArgsFailFast(t *testing.T) {
	home := setupIsolatedHome(t)
	srv, _ := newFakeDispatcher(t)
	t.Setenv("PURA_TOOL_CACHE", filepath.Join(home, "c.json"))
	flagAPIURL = srv.URL
	_, err := runCmd(t, "tool", "call", "sheet.append_row",
		"--args", `{not valid json`,
		"--json",
	)
	if err == nil {
		t.Fatal("expected error for malformed --args JSON")
	}
	if !strings.Contains(err.Error(), "--args") {
		t.Errorf("error message should mention --args: %v", err)
	}
}

func TestTool_Call_ValidationFailureReturnsApiError(t *testing.T) {
	home := setupIsolatedHome(t)
	srv, fd := newFakeDispatcher(t)
	t.Setenv("PURA_TOOL_CACHE", filepath.Join(home, "c.json"))
	flagAPIURL = srv.URL

	fd.respond = func(_ toolCallRecord) (any, int) {
		return map[string]any{
			"ok": false,
			"error": map[string]any{
				"code":       "validation_failed",
				"field":      "sheet_ref",
				"suggestion": "required",
			},
		}, 400
	}

	_, err := runCmd(t, "tool", "call", "sheet.append_row", "--json")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	e := api.AsError(err)
	if e == nil {
		t.Fatalf("error should be *api.Error; got %T: %v", err, err)
	}
	if e.Status != 400 {
		t.Errorf("Status = %d, want 400", e.Status)
	}
	if e.Code != "validation_failed" {
		t.Errorf("Code = %q", e.Code)
	}
}

func TestTool_Call_ProposalKindReportsProposalID(t *testing.T) {
	home := setupIsolatedHome(t)
	srv, fd := newFakeDispatcher(t)
	t.Setenv("PURA_TOOL_CACHE", filepath.Join(home, "c.json"))
	flagAPIURL = srv.URL

	fd.respond = func(_ toolCallRecord) (any, int) {
		return map[string]any{
			"ok":          true,
			"kind":        "proposal",
			"proposal_id": "prop_abc",
			"preview":     map[string]any{"summary": "…"},
		}, 202
	}

	out, err := runCmd(t, "tool", "call", "sheet.append_row", "--json")
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)
	if data["kind"] != "proposal" {
		t.Errorf("kind = %v", data["kind"])
	}
	if data["proposal_id"] != "prop_abc" {
		t.Errorf("proposal_id = %v", data["proposal_id"])
	}
}

// ─── offline cache fallback ─────────────────────────────────────────────

func TestTool_Ls_FallsBackToStaleCacheWhenServerDown(t *testing.T) {
	home := setupIsolatedHome(t)
	cachePath := filepath.Join(home, "tools.json")
	t.Setenv("PURA_TOOL_CACHE", cachePath)

	// Seed a cache with a *different* api_url than the one we'll point to.
	cat := toolCatalog{
		FetchedAt: "2026-04-19T00:00:00Z",
		APIURL:    "https://stale.example",
		Tools: []toolMeta{
			{Name: "sheet.list_rows", ShortDescription: "stale"},
		},
	}
	if err := writeToolCache(cachePath, cat); err != nil {
		t.Fatal(err)
	}

	// Point at an unreachable port; server is down.
	flagAPIURL = "http://127.0.0.1:1"

	out, err := runCmd(t, "tool", "ls", "--json")
	if err != nil {
		t.Fatalf("ls with stale cache: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	src, _ := env["data"].(map[string]any)["source"].(string)
	if !strings.HasPrefix(src, "cache") {
		t.Errorf("source = %q, want a cache-based source", src)
	}
}
