package output

import (
	"encoding/json"
	"strings"
	"testing"
)

// Envelope tests: we round-trip through JSON and assert on specific fields,
// not byte-exact strings. That keeps tests resilient to Go's json encoder
// re-ordering while still pinning the contract (which fields exist, with
// what shapes).

// ---------- v1 baseline (kept for regression) ----------

func TestNewOK(t *testing.T) {
	env := NewOK(map[string]string{"slug": "abc123"})

	if !env.OK {
		t.Error("NewOK envelope should have OK=true")
	}
	if env.Data == nil {
		t.Error("NewOK envelope should have non-nil Data")
	}
	if env.Error != nil {
		t.Error("NewOK envelope should have nil Error")
	}
}

func TestNewOK_JSON(t *testing.T) {
	env := NewOK("hello")
	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if parsed["ok"] != true {
		t.Errorf("ok = %v, want true", parsed["ok"])
	}
	if parsed["data"] != "hello" {
		t.Errorf("data = %v, want %q", parsed["data"], "hello")
	}
	if _, exists := parsed["error"]; exists {
		t.Error("error field should be omitted from JSON")
	}
}

func TestNewError(t *testing.T) {
	env := NewError("not_found", "Document not found", "Check the slug")

	if env.OK {
		t.Error("NewError envelope should have OK=false")
	}
	if env.Data != nil {
		t.Error("NewError envelope should have nil Data")
	}
	detail, ok := env.Error.(ErrorDetail)
	if !ok {
		t.Fatalf("Error should be ErrorDetail, got %T", env.Error)
	}
	if detail.Code != "not_found" || detail.Message != "Document not found" || detail.Hint != "Check the slug" {
		t.Errorf("bad detail: %+v", detail)
	}
}

func TestNewError_NoHint(t *testing.T) {
	env := NewError("server_error", "Internal error", "")
	detail := env.Error.(ErrorDetail)
	if detail.Hint != "" {
		t.Errorf("Hint should be empty, got %q", detail.Hint)
	}
	data, _ := json.Marshal(env)
	var parsed map[string]any
	_ = json.Unmarshal(data, &parsed)
	errMap := parsed["error"].(map[string]any)
	if _, exists := errMap["hint"]; exists {
		t.Error("hint field should be omitted from JSON when empty")
	}
}

// ---------- v2 additions: summary + breadcrumbs + options ----------

func TestNewOK_Minimal_OmitsV2Fields(t *testing.T) {
	b, err := json.Marshal(NewOK(map[string]string{"k": "v"}))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(b)
	mustContain(t, got, `"ok":true`)
	mustContain(t, got, `"data":{"k":"v"}`)
	mustNotContain(t, got, `"summary"`)
	mustNotContain(t, got, `"breadcrumbs"`)
}

func TestNewOK_WithSummary_FormatsArgs(t *testing.T) {
	e := NewOK(nil, WithSummary("Published %s (%d KB)", "abc", 12))
	if e.Summary != "Published abc (12 KB)" {
		t.Errorf("Summary = %q, want %q", e.Summary, "Published abc (12 KB)")
	}
	e2 := NewOK(nil, WithSummary("literal string"))
	if e2.Summary != "literal string" {
		t.Errorf("zero-arg summary should pass through literally, got %q", e2.Summary)
	}
}

func TestNewOK_WithBreadcrumbs_OrderPreserved(t *testing.T) {
	e := NewOK(nil,
		WithBreadcrumb("view", "pura open abc", "Open in browser"),
		WithBreadcrumb("edit", `pura chat abc "..."`, "AI-edit"),
		WithBreadcrumb("delete", "pura rm abc", ""), // empty description allowed
	)
	if len(e.Breadcrumbs) != 3 {
		t.Fatalf("want 3 breadcrumbs, got %d", len(e.Breadcrumbs))
	}
	if e.Breadcrumbs[0].Action != "view" || e.Breadcrumbs[2].Action != "delete" {
		t.Errorf("order not preserved: %+v", e.Breadcrumbs)
	}
	b, _ := json.Marshal(e)
	// Empty description must be omitted (omitempty), not serialized as "".
	mustNotContain(t, string(b), `"description":""`)
}

func TestNewOK_WithBreadcrumbsBulk(t *testing.T) {
	e := NewOK(nil, WithBreadcrumbs(
		Breadcrumb{Action: "a", Cmd: "pura a"},
		Breadcrumb{Action: "b", Cmd: "pura b"},
	))
	if len(e.Breadcrumbs) != 2 {
		t.Fatalf("want 2, got %d", len(e.Breadcrumbs))
	}
}

func TestNewError_CarriesBreadcrumbs(t *testing.T) {
	e := NewError("auth_required", "Token expired", "Run pura auth login",
		WithBreadcrumb("retry", "pura auth login", "Sign in"),
	)
	b, _ := json.Marshal(e)
	got := string(b)
	mustContain(t, got, `"ok":false`)
	mustContain(t, got, `"code":"auth_required"`)
	mustContain(t, got, `"message":"Token expired"`)
	mustContain(t, got, `"hint":"Run pura auth login"`)
	mustContain(t, got, `"breadcrumbs":[{"action":"retry"`)
	mustNotContain(t, got, `"data"`) // errors never carry data
}

func TestWithMeta_Attaches(t *testing.T) {
	type meta struct {
		Total int `json:"total"`
	}
	e := NewOK("payload", WithMeta(meta{Total: 7}))
	b, _ := json.Marshal(e)
	mustContain(t, string(b), `"meta":{"total":7}`)
}

// ---------- helpers ----------

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected JSON to contain %q\ngot: %s", needle, haystack)
	}
}

func mustNotContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("expected JSON NOT to contain %q\ngot: %s", needle, haystack)
	}
}
