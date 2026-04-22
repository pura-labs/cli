package api

import (
	"encoding/json"
	"testing"
)

func TestCreateRequest_Marshal(t *testing.T) {
	req := CreateRequest{
		Content:   "# Hello World",
		Substrate: "markdown",
		Title:     "Test Doc",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if parsed["content"] != "# Hello World" {
		t.Errorf("content = %v, want %q", parsed["content"], "# Hello World")
	}
	if parsed["substrate"] != "markdown" {
		t.Errorf("substrate = %v, want %q", parsed["substrate"], "markdown")
	}
	if parsed["title"] != "Test Doc" {
		t.Errorf("title = %v, want %q", parsed["title"], "Test Doc")
	}
}

func TestCreateRequest_OmitEmpty(t *testing.T) {
	req := CreateRequest{
		Content: "some data",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// These fields should be omitted when empty
	for _, key := range []string{"kind", "substrate", "title", "theme", "metadata"} {
		if _, exists := parsed[key]; exists {
			t.Errorf("field %q should be omitted when empty", key)
		}
	}
}

func TestDocResponse_Unmarshal(t *testing.T) {
	raw := `{
		"slug": "k8x2m1",
		"kind": "doc",
		"substrate": "markdown",
		"title": "My Doc",
		"content": "# Hello",
		"theme": "default",
		"created_at": "2025-01-15T10:30:00Z",
		"updated_at": "2025-01-15T10:30:00Z"
	}`

	var doc DocResponse
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if doc.Slug != "k8x2m1" {
		t.Errorf("Slug = %q, want %q", doc.Slug, "k8x2m1")
	}
	if doc.Kind != "doc" {
		t.Errorf("Kind = %q, want %q", doc.Kind, "doc")
	}
	if doc.Substrate != "markdown" {
		t.Errorf("Substrate = %q, want %q", doc.Substrate, "markdown")
	}
	if doc.Title != "My Doc" {
		t.Errorf("Title = %q, want %q", doc.Title, "My Doc")
	}
	if doc.Content != "# Hello" {
		t.Errorf("Content = %q, want %q", doc.Content, "# Hello")
	}
	if doc.CreatedAt != "2025-01-15T10:30:00Z" {
		t.Errorf("CreatedAt = %q, want %q", doc.CreatedAt, "2025-01-15T10:30:00Z")
	}
}

func TestDocResponse_RoundTrip(t *testing.T) {
	original := DocResponse{
		Slug:      "test123",
		Kind:      "sheet",
		Substrate: "csv",
		Title:     "Data Export",
		Content:   "a,b,c\n1,2,3",
		Theme:     "minimal",
		CreatedAt: "2025-06-01T00:00:00Z",
		UpdatedAt: "2025-06-02T12:00:00Z",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded DocResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded != original {
		t.Errorf("round-trip mismatch:\n  got:  %+v\n  want: %+v", decoded, original)
	}
}

func TestApiResponse_Unmarshal(t *testing.T) {
	raw := `{
		"ok": true,
		"data": {"slug": "abc", "kind": "doc", "substrate": "markdown", "title": "", "content": "# Hi", "theme": "default", "created_at": "", "updated_at": ""},
		"meta": {"total": 1, "timing_ms": 12.5}
	}`

	var resp ApiResponse[DocResponse]
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if !resp.OK {
		t.Error("OK should be true")
	}
	if resp.Data.Slug != "abc" {
		t.Errorf("Data.Slug = %q, want %q", resp.Data.Slug, "abc")
	}
	if resp.Meta == nil {
		t.Fatal("Meta should not be nil")
	}
	if resp.Meta.TimingMs != 12.5 {
		t.Errorf("Meta.TimingMs = %v, want 12.5", resp.Meta.TimingMs)
	}
}
