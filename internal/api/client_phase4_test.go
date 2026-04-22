package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Phase 4 client helpers — AcceptProposal / RejectProposal / ListMessages /
// Bootstrap. These tests seed a minimal httptest.Server that answers with
// the Phase 4 envelope shapes.

func phase4Client(srv *httptest.Server) *Client {
	return &Client{
		BaseURL:    srv.URL,
		Token:      "sk_test",
		HTTPClient: &http.Client{},
		Ctx:        context.Background(),
	}
}

func TestAcceptProposal_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/p/@_/xy/chat/m1/accept" || r.Method != "POST" {
			t.Errorf("bad req: %s %s", r.Method, r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer sk_test" {
			t.Errorf("bad auth: %q", auth)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"data": map[string]any{
				"message_id":     "m1",
				"before_version": 3,
				"after_version":  4,
				"version_id":     "v_abc",
			},
		})
	}))
	defer srv.Close()

	c := phase4Client(srv)
	res, err := c.AcceptProposal("xy", "m1")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if res.AfterVersion != 4 {
		t.Errorf("after_version = %d, want 4", res.AfterVersion)
	}
}

func TestAcceptProposal_StaleReturns409(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": map[string]any{"code": "stale", "message": "doc changed"},
		})
	}))
	defer srv.Close()

	c := phase4Client(srv)
	_, err := c.AcceptProposal("xy", "m1")
	if err == nil {
		t.Fatal("want error")
	}
	var apiErr *Error
	if !strings.Contains(err.Error(), "doc changed") {
		t.Errorf("err = %v", err)
	}
	if e, ok := err.(*Error); ok {
		apiErr = e
		if apiErr.Code != "stale" {
			t.Errorf("code = %q, want stale", apiErr.Code)
		}
	} else {
		t.Errorf("expected *Error, got %T", err)
	}
}

func TestRejectProposal_Reject(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"data": map[string]any{"message_id": "m1", "reason": "reject"},
		})
	}))
	defer srv.Close()

	c := phase4Client(srv)
	res, err := c.RejectProposal("xy", "m1", "reject")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if res.Reason != "reject" {
		t.Errorf("reason = %q", res.Reason)
	}
	if gotPath != "/api/p/@_/xy/chat/m1/reject" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestRejectProposal_DiscardHitsDiscardEndpoint(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"data": map[string]any{"message_id": "m1", "reason": "discard"},
		})
	}))
	defer srv.Close()

	c := phase4Client(srv)
	_, err := c.RejectProposal("xy", "m1", "discard")
	if err != nil {
		t.Fatalf("discard: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/discard") {
		t.Errorf("path should end with /discard: %q", gotPath)
	}
}

func TestRejectProposal_InvalidKindRejectedLocally(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", HTTPClient: &http.Client{}, Ctx: context.Background()}
	_, err := c.RejectProposal("xy", "m1", "bogus")
	if err == nil {
		t.Fatal("want validation error for bogus kind")
	}
}

func TestListMessages_ReturnsOldestFirst(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "limit=") {
			t.Errorf("expected limit query, got %q", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"data": []map[string]any{
				{"id": "a", "role": "user", "content": "hi", "status": "ok"},
				{"id": "b", "role": "assistant", "content": "ok", "proposal_status": "pending"},
			},
		})
	}))
	defer srv.Close()

	c := phase4Client(srv)
	msgs, err := c.ListMessages("xy", 20)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 msgs, got %d", len(msgs))
	}
	if msgs[1].ProposalStatus != "pending" {
		t.Errorf("proposal_status = %q", msgs[1].ProposalStatus)
	}
}

func TestBootstrap_StreamsAllEventTypes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		for _, line := range []string{
			`data: {"type":"plan","kind":"sheet","substrate":"csv","slug_suggestion":"guestbook"}` + "\n\n",
			`data: {"type":"content_delta","content":"name,msg\n"}` + "\n\n",
			`data: {"type":"content_final","content":"name,msg\nAda,hi"}` + "\n\n",
			`data: {"type":"schema","schema":[{"name":"name","type":"string"}]}` + "\n\n",
			`data: {"type":"title_suggestion","title":"Launch Guestbook"}` + "\n\n",
			`data: {"type":"usage","prompt_tokens":80,"completion_tokens":120}` + "\n\n",
			"data: [DONE]\n\n",
		} {
			_, _ = io.WriteString(w, line)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	c := phase4Client(srv)
	var seen []string
	err := c.Bootstrap(BootstrapRequest{Describe: "launch guestbook"}, func(e BootstrapSSEEvent) {
		seen = append(seen, e.Type)
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	want := []string{"plan", "content_delta", "content_final", "schema", "title_suggestion", "usage"}
	if len(seen) != len(want) {
		t.Fatalf("want %d events, got %d: %v", len(want), len(seen), seen)
	}
	for i, w := range want {
		if seen[i] != w {
			t.Errorf("event[%d] = %q, want %q", i, seen[i], w)
		}
	}
}

func TestBootstrap_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": map[string]any{"code": "validation", "message": "describe required"},
		})
	}))
	defer srv.Close()

	c := phase4Client(srv)
	err := c.Bootstrap(BootstrapRequest{Describe: ""}, func(e BootstrapSSEEvent) {})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "describe required") {
		t.Errorf("err = %v", err)
	}
}
