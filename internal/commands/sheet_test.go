package commands

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pura-labs/cli/internal/api"
)

// A minimal stub /api/tool/<name> that records the last JSON body + query
// for assertion. The response shape is per-tool and supplied by the test.
type sheetStub struct {
	lastName string
	lastBody map[string]any
	respond  func(name string, body map[string]any) (any, int)
}

func newSheetStub(t *testing.T) (*httptest.Server, *sheetStub) {
	t.Helper()
	s := &sheetStub{
		respond: func(_ string, _ map[string]any) (any, int) {
			return map[string]any{"ok": true, "result": map[string]any{}}, 200
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/tool/") {
			w.WriteHeader(404)
			return
		}
		s.lastName = strings.TrimPrefix(r.URL.Path, "/api/tool/")
		body, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(body, &parsed)
		s.lastBody = parsed
		resp, status := s.respond(s.lastName, parsed)
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

// ─── `pura sheet ls` ────────────────────────────────────────────────────

func TestSheet_Ls_ForwardsRefAndPagingFlags(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newSheetStub(t)
	flagAPIURL = srv.URL

	stub.respond = func(_ string, _ map[string]any) (any, int) {
		return map[string]any{
			"ok": true,
			"result": map[string]any{
				"rows": []any{
					map[string]any{"_id": "01890000-0000-7000-8000-000000000001", "name": "Alice"},
				},
				"total":       float64(1),
				"next_cursor": nil,
				"version":     float64(1),
			},
		}, 200
	}

	out, err := runCmd(t, "sheet", "ls", "@alice/leads", "--limit", "50", "--cursor", "cursor-xyz", "--json")
	if err != nil {
		t.Fatalf("sheet ls: %v\n%s", err, out)
	}
	if stub.lastName != "sheet.list_rows" {
		t.Errorf("called %q, want sheet.list_rows", stub.lastName)
	}
	if stub.lastBody["sheet_ref"] != "@alice/leads" {
		t.Errorf("sheet_ref = %v", stub.lastBody["sheet_ref"])
	}
	if stub.lastBody["limit"].(float64) != 50 {
		t.Errorf("limit forwarded as %v", stub.lastBody["limit"])
	}
	if stub.lastBody["cursor"] != "cursor-xyz" {
		t.Errorf("cursor not forwarded: %v", stub.lastBody["cursor"])
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)
	rows, _ := data["rows"].([]any)
	if len(rows) != 1 {
		t.Errorf("envelope rows length = %d", len(rows))
	}
}

func TestSheet_Ls_SkipsOptionalFlagsWhenZero(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newSheetStub(t)
	flagAPIURL = srv.URL
	stub.respond = func(_ string, _ map[string]any) (any, int) {
		return map[string]any{
			"ok": true,
			"result": map[string]any{
				"rows": []any{}, "total": float64(0), "next_cursor": nil,
			},
		}, 200
	}
	if _, err := runCmd(t, "sheet", "ls", "@_/x", "--json"); err != nil {
		t.Fatalf("ls: %v", err)
	}
	if _, has := stub.lastBody["limit"]; has {
		t.Error("limit should be omitted when 0 (server-default)")
	}
	if _, has := stub.lastBody["cursor"]; has {
		t.Error("cursor should be omitted when empty")
	}
}

// ─── `pura sheet schema` ────────────────────────────────────────────────

func TestSheet_Schema_CallsGetSchema(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newSheetStub(t)
	flagAPIURL = srv.URL
	stub.respond = func(_ string, _ map[string]any) (any, int) {
		return map[string]any{
			"ok": true,
			"result": map[string]any{
				"schema": []any{
					map[string]any{"name": "name", "type": "string", "required": true},
					map[string]any{"name": "age", "type": "number"},
				},
				"schema_version": float64(3),
				"doc_version":    float64(42),
			},
		}, 200
	}
	out, err := runCmd(t, "sheet", "schema", "@alice/leads", "--json")
	if err != nil {
		t.Fatalf("schema: %v\n%s", err, out)
	}
	if stub.lastName != "sheet.get_schema" {
		t.Errorf("called %q", stub.lastName)
	}
	env := mustUnmarshalEnvelope(t, out)
	schema, _ := env["data"].(map[string]any)["schema"].([]any)
	if len(schema) != 2 {
		t.Errorf("schema len = %d", len(schema))
	}
}

// ─── `pura sheet export` ────────────────────────────────────────────────

func TestSheet_Export_Csv_DefaultsToCsvAndWritesEnvelopeInJsonMode(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newSheetStub(t)
	flagAPIURL = srv.URL
	stub.respond = func(_ string, _ map[string]any) (any, int) {
		return map[string]any{
			"ok": true,
			"result": map[string]any{
				"mime":      "text/csv; charset=utf-8",
				"body":      "_id,name\n1,Alice\n",
				"encoding":  "utf8",
				"filename":  "x.csv",
				"row_count": float64(1),
				"byte_size": float64(17),
				"format":    "csv",
			},
		}, 200
	}
	out, err := runCmd(t, "sheet", "export", "@_/x", "--json")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if stub.lastBody["format"] != "csv" {
		t.Errorf("format default should be csv, got %v", stub.lastBody["format"])
	}
	env := mustUnmarshalEnvelope(t, out)
	if env["data"].(map[string]any)["format"] != "csv" {
		t.Errorf("envelope format not csv")
	}
}

func TestSheet_Export_Xlsx_WithoutOutRefuses(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newSheetStub(t)
	flagAPIURL = srv.URL
	stub.respond = func(_ string, _ map[string]any) (any, int) {
		return map[string]any{
			"ok": true,
			"result": map[string]any{
				"mime":      "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
				"body":      base64.StdEncoding.EncodeToString([]byte("PK\x03\x04fake")),
				"encoding":  "base64",
				"filename":  "x.xlsx",
				"row_count": float64(0),
				"byte_size": float64(8),
				"format":    "xlsx",
			},
		}, 200
	}
	_, err := runCmd(t, "sheet", "export", "@_/x", "--format", "xlsx")
	if err == nil {
		t.Fatal("expected error when xlsx export has no --out")
	}
	if !strings.Contains(err.Error(), "--out") {
		t.Errorf("error should mention --out: %v", err)
	}
}

func TestSheet_Export_Xlsx_WithOutWritesBinary(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newSheetStub(t)
	flagAPIURL = srv.URL
	xlsxBytes := []byte("PK\x03\x04\x14\x00fake-xlsx-body")
	stub.respond = func(_ string, _ map[string]any) (any, int) {
		return map[string]any{
			"ok": true,
			"result": map[string]any{
				"mime":      "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
				"body":      base64.StdEncoding.EncodeToString(xlsxBytes),
				"encoding":  "base64",
				"filename":  "x.xlsx",
				"row_count": float64(0),
				"byte_size": float64(len(xlsxBytes)),
				"format":    "xlsx",
			},
		}, 200
	}
	outPath := filepath.Join(t.TempDir(), "out.xlsx")
	if _, err := runCmd(t, "sheet", "export", "@_/x", "--format", "xlsx", "--out", outPath, "--json"); err != nil {
		t.Fatalf("export --out: %v", err)
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read %s: %v", outPath, err)
	}
	if !strings.HasPrefix(string(got), "PK\x03\x04") {
		t.Errorf("output bytes lack PK magic: %q", got[:4])
	}
	if len(got) != len(xlsxBytes) {
		t.Errorf("size mismatch: got %d want %d", len(got), len(xlsxBytes))
	}
}

// ─── `pura sheet clone` ─────────────────────────────────────────────────

func TestSheet_Clone_ForwardsTargetFlags(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newSheetStub(t)
	flagAPIURL = srv.URL
	stub.respond = func(_ string, _ map[string]any) (any, int) {
		return map[string]any{
			"ok": true,
			"result": map[string]any{
				"doc_id":  "doc_abc",
				"handle":  "_",
				"slug":    "x-copy",
				"version": float64(1),
			},
		}, 200
	}

	if _, err := runCmd(t, "sheet", "clone", "@_/x", "--to", "x-copy", "--to-handle", "_", "--json"); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if stub.lastName != "sheet.clone" {
		t.Errorf("called %q", stub.lastName)
	}
	if stub.lastBody["target_slug"] != "x-copy" {
		t.Errorf("target_slug = %v", stub.lastBody["target_slug"])
	}
	if stub.lastBody["target_handle"] != "_" {
		t.Errorf("target_handle = %v", stub.lastBody["target_handle"])
	}
}

func TestSheet_Clone_OmitsFlagsWhenEmpty(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newSheetStub(t)
	flagAPIURL = srv.URL
	stub.respond = func(_ string, _ map[string]any) (any, int) {
		return map[string]any{
			"ok": true,
			"result": map[string]any{
				"doc_id": "d", "handle": "_", "slug": "auto-slug", "version": float64(1),
			},
		}, 200
	}
	if _, err := runCmd(t, "sheet", "clone", "@_/x", "--json"); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if _, has := stub.lastBody["target_slug"]; has {
		t.Error("target_slug should be absent when --to not given")
	}
	if _, has := stub.lastBody["target_handle"]; has {
		t.Error("target_handle should be absent when --to-handle not given")
	}
}

// ─── error flow ─────────────────────────────────────────────────────────

func TestSheet_PropagatesApiErrorOnFailure(t *testing.T) {
	setupIsolatedHome(t)
	srv, stub := newSheetStub(t)
	flagAPIURL = srv.URL
	stub.respond = func(_ string, _ map[string]any) (any, int) {
		return map[string]any{
			"ok": false,
			"error": map[string]any{
				"code":       "not_found",
				"suggestion": "sheet does not exist",
			},
		}, 404
	}
	_, err := runCmd(t, "sheet", "ls", "@_/ghost", "--json")
	if err == nil {
		t.Fatal("expected error on 404 from stub")
	}
	e := api.AsError(err)
	if e == nil || e.Status != 404 || e.Code != "not_found" {
		t.Errorf("expected *api.Error{404, not_found}; got %v (%T)", err, err)
	}
}

func TestSheet_ErrorsWhenApiUrlUnset(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = ""
	flagToken = ""
	if _, err := runCmd(t, "sheet", "ls", "@_/x"); err == nil {
		t.Fatal("expected error when api_url unset")
	}
}
