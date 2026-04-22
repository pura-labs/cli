package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientCreate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/p" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		var req CreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Content != "# Hello" {
			t.Errorf("Content = %q, want # Hello", req.Content)
		}

		w.WriteHeader(201)
		json.NewEncoder(w).Encode(ApiResponse[CreateResponse]{
			OK: true,
			Data: CreateResponse{
				Slug:      "abc123",
				Token:     "sk_pur_test",
				URL:       "https://pura.so/abc123",
				Kind:      "doc",
				Substrate: "markdown",
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	resp, err := client.Create(CreateRequest{Content: "# Hello", Substrate: "markdown"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if resp.Slug != "abc123" {
		t.Errorf("Slug = %q, want abc123", resp.Slug)
	}
	if resp.Token != "sk_pur_test" {
		t.Errorf("Token = %q, want sk_pur_test", resp.Token)
	}
}

func TestClientGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/p/@_/abc123" {
			t.Errorf("path = %q, want /api/p/@_/abc123", r.URL.Path)
		}
		json.NewEncoder(w).Encode(ApiResponse[DocResponse]{
			OK: true,
			Data: DocResponse{
				Slug:      "abc123",
				Kind:      "doc",
				Substrate: "markdown",
				Title:     "Test",
				Content:   "# Hello",
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	doc, err := client.Get("abc123")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if doc.Slug != "abc123" {
		t.Errorf("Slug = %q", doc.Slug)
	}
	if doc.Content != "# Hello" {
		t.Errorf("Content = %q", doc.Content)
	}
}

func TestClientGetNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(ApiResponse[any]{
			OK:    false,
			Error: &ApiError{Code: "not_found", Message: "Document not found"},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	_, err := client.Get("missing")
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestClientDelete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/p/@_/abc123" {
			t.Errorf("path = %q, want /api/p/@_/abc123", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			t.Errorf("auth = %q, want Bearer test-token", auth)
		}
		w.WriteHeader(204)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	err := client.Delete("abc123")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestClientDeleteUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(ApiResponse[any]{
			OK:    false,
			Error: &ApiError{Code: "unauthorized", Message: "Invalid token"},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "wrong-token")
	err := client.Delete("abc123")
	if err == nil {
		t.Fatal("expected error for 401")
	}
}

func TestClientList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("token"); got != "my-token" {
			t.Errorf("token query = %q, want my-token", got)
		}
		json.NewEncoder(w).Encode(ApiResponse[[]DocListItem]{
			OK: true,
			Data: []DocListItem{
				{Slug: "a1", Kind: "doc", Substrate: "markdown", Title: "Doc 1"},
				{Slug: "b2", Kind: "sheet", Substrate: "csv", Title: "Doc 2"},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	items, err := client.List("my-token")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 2 {
		t.Errorf("len = %d, want 2", len(items))
	}
	if items[0].Slug != "a1" {
		t.Errorf("items[0].Slug = %q", items[0].Slug)
	}
}

// Edit tokens can contain characters that would mangle a URL if we naively
// concatenated them (url.Values.Encode() was added for exactly this reason).
func TestClientList_EscapesToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("token"); got != "a+b c&d=e" {
			t.Errorf("token query = %q, want 'a+b c&d=e' (decoded)", got)
		}
		json.NewEncoder(w).Encode(ApiResponse[[]DocListItem]{OK: true, Data: []DocListItem{}})
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	if _, err := client.List("a+b c&d=e"); err != nil {
		t.Fatalf("List() error = %v", err)
	}
}

// API keys must never ride the URL — the server also supports Bearer, and
// URLs are observable places (proxy logs, CF analytics, browser history).
func TestClientList_RefusesAPIKey(t *testing.T) {
	client := NewClient("http://example.invalid", "")
	_, err := client.List("sk_pura_abcdef")
	if err == nil {
		t.Fatal("expected refusal when token looks like an API key")
	}
}

func TestClientListForUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("user"); got != "me" {
			t.Fatalf("user query = %q, want me", got)
		}
		if got := r.URL.Query().Get("token"); got != "" {
			t.Fatalf("token query = %q, want empty", got)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer sk_pura_test_key" {
			t.Fatalf("auth = %q, want Bearer sk_pura_test_key", auth)
		}
		json.NewEncoder(w).Encode(ApiResponse[[]DocListItem]{
			OK: true,
			Data: []DocListItem{
				{Slug: "owned", Kind: "doc", Substrate: "markdown", Title: "Owned Doc"},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "sk_pura_test_key")
	items, err := client.ListForUser()
	if err != nil {
		t.Fatalf("ListForUser() error = %v", err)
	}
	if len(items) != 1 || items[0].Slug != "owned" {
		t.Fatalf("unexpected items = %+v", items)
	}
}

func TestClientGetRawServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	if _, err := client.GetRaw("abc123"); err == nil {
		t.Fatal("expected error for 500")
	}
}

func TestClientGetRawUsesNormalizedHandle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/@alice/abc123/raw" {
			t.Fatalf("path = %q, want /@alice/abc123/raw", r.URL.Path)
		}
		w.Write([]byte("# Hello"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	client.Handle = " @alice "
	body, err := client.GetRaw("abc123")
	if err != nil {
		t.Fatalf("GetRaw() error = %v", err)
	}
	if body != "# Hello" {
		t.Fatalf("body = %q, want # Hello", body)
	}
}

func TestClientGetUsesExplicitNamespacedSlug(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/p/@bob/abc123" {
			t.Fatalf("path = %q, want /api/p/@bob/abc123", r.URL.Path)
		}
		json.NewEncoder(w).Encode(ApiResponse[DocResponse]{
			OK: true,
			Data: DocResponse{
				Slug:      "abc123",
				Kind:      "doc",
				Substrate: "markdown",
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	client.Handle = "ignored"
	if _, err := client.Get("@bob/abc123"); err != nil {
		t.Fatalf("Get() error = %v", err)
	}
}

func TestClientGetContextInvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-json"))
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	if _, err := client.GetContext("abc123"); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
