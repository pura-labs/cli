package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Extended client-level tests — covers everything beyond the CRUD basics
// that client_test.go exercises: error shaping, keys, versions, stats,
// events, claim, Me, device-flow poll, and the SSE parser. Kept in its own
// file so adding a new endpoint doesn't push client_test.go past a casual
// read, and so the CRUD file stays the obvious entry point for anyone
// ramping on the api package.

func newAPITestServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func writeEnv(w http.ResponseWriter, ok bool, data any) {
	w.Header().Set("Content-Type", "application/json")
	body := map[string]any{"ok": ok}
	if ok {
		body["data"] = data
	} else {
		body["error"] = data
	}
	_ = json.NewEncoder(w).Encode(body)
}

// ---- error shaping ----

func TestErrorFromResponse_EmptyBody(t *testing.T) {
	err := errorFromResponse(503, []byte(""))
	ae, ok := err.(*Error)
	if !ok {
		t.Fatalf("want *Error, got %T", err)
	}
	if ae.Status != 503 {
		t.Errorf("Status = %d, want 503", ae.Status)
	}
}

func TestErrorFromResponse_EnvelopeBody(t *testing.T) {
	body := `{"ok":false,"error":{"code":"rate_limit","message":"slow","hint":"try later"}}`
	err := errorFromResponse(429, []byte(body))
	ae := AsError(err)
	if ae == nil {
		t.Fatal("want AsError → non-nil")
	}
	if ae.Status != 429 || ae.Code != "rate_limit" {
		t.Errorf("wrong fields: %+v", ae)
	}
	if !IsRateLimited(err) {
		t.Error("IsRateLimited should be true")
	}
}

func TestAsError_NilAndPlain(t *testing.T) {
	if AsError(nil) != nil {
		t.Error("nil → nil")
	}
	if AsError(errors.New("plain")) != nil {
		t.Error("plain error → nil")
	}
}

// ---- keys ----

func TestListKeys(t *testing.T) {
	srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/keys" || r.Method != "GET" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer t" {
			t.Errorf("missing bearer: %q", r.Header.Get("Authorization"))
		}
		writeEnv(w, true, []map[string]any{
			{"id": "k1", "name": "n", "prefix": "sk_pura_abcd", "scopes": []string{"docs:read"}},
		})
	}))
	c := NewClient(srv.URL, "t")
	items, err := c.ListKeys()
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(items) != 1 || items[0].ID != "k1" {
		t.Errorf("items = %+v", items)
	}
}

func TestCreateKey(t *testing.T) {
	srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/keys" || r.Method != "POST" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body CreateKeyRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Name != "ci" || len(body.Scopes) != 1 {
			t.Errorf("body lost fields: %+v", body)
		}
		writeEnv(w, true, map[string]any{
			"id": "k1", "name": body.Name, "prefix": "sk_pura_00", "token": "sk_pura_00SECRET", "scopes": body.Scopes,
		})
	}))
	c := NewClient(srv.URL, "t")
	resp, err := c.CreateKey(CreateKeyRequest{Name: "ci", Scopes: []string{"docs:read"}})
	if err != nil {
		t.Fatalf("CreateKey: %v", err)
	}
	if resp.Token != "sk_pura_00SECRET" {
		t.Errorf("token = %q", resp.Token)
	}
}

func TestRevokeKey_ReturnsNilOn204(t *testing.T) {
	srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Fatalf("want DELETE, got %s", r.Method)
		}
		w.WriteHeader(204)
	}))
	c := NewClient(srv.URL, "t")
	if err := c.RevokeKey("k1"); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
}

func TestRevokeKey_Surfaces404(t *testing.T) {
	srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]string{"code": "not_found", "message": "nope"}})
	}))
	c := NewClient(srv.URL, "t")
	err := c.RevokeKey("ghost")
	if !IsNotFound(err) {
		t.Errorf("want IsNotFound, got %v", err)
	}
}

// ---- versions ----

func TestListVersions(t *testing.T) {
	srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/versions") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		writeEnv(w, true, []map[string]any{{"id": "v1", "version": 1}, {"id": "v2", "version": 2}})
	}))
	c := NewClient(srv.URL, "t")
	c.Handle = "_"
	items, err := c.ListVersions("abc")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("want 2, got %d", len(items))
	}
}

func TestGetVersion_And_Restore(t *testing.T) {
	srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/restore") && r.Method == "POST":
			writeEnv(w, true, map[string]any{"version": 9, "content": "restored"})
		case strings.Contains(r.URL.Path, "/versions/"):
			writeEnv(w, true, map[string]any{"version": 3, "content": "v3 body"})
		default:
			w.WriteHeader(404)
		}
	}))
	c := NewClient(srv.URL, "t")
	c.Handle = "_"

	v, err := c.GetVersion("abc", 3)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if v.Content != "v3 body" || v.Version != 3 {
		t.Errorf("GetVersion lost fields: %+v", v)
	}

	r, err := c.RestoreVersion("abc", 3)
	if err != nil {
		t.Fatalf("RestoreVersion: %v", err)
	}
	if r.Version != 9 {
		t.Errorf("Restore: want v9, got %d", r.Version)
	}
}

// ---- stats / events ----

func TestGetStats_BasicAndDetailed(t *testing.T) {
	srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("detail") == "full" {
			writeEnv(w, true, map[string]any{"views": 5, "unique_countries": 2, "view_types": map[string]int{"page": 4}, "bot_ratio": 0.1})
			return
		}
		writeEnv(w, true, map[string]int{"views": 42})
	}))
	c := NewClient(srv.URL, "t")
	c.Handle = "_"

	basic, err := c.GetStats("abc")
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if basic.Views != 42 {
		t.Errorf("basic views = %d", basic.Views)
	}

	full, err := c.GetDetailedStats("abc")
	if err != nil {
		t.Fatalf("GetDetailedStats: %v", err)
	}
	if full.UniqueCountries != 2 {
		t.Errorf("detailed lost fields: %+v", full)
	}
}

func TestListEvents_BuildsQueryString(t *testing.T) {
	srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("since") != "7" || q.Get("limit") != "10" || q.Get("kinds") != "doc.updated" {
			t.Errorf("query string lost fields: %q", r.URL.RawQuery)
		}
		writeEnv(w, true, map[string]any{"cursor": 7, "events": []any{}})
	}))
	c := NewClient(srv.URL, "t")
	c.Handle = "_"
	_, err := c.ListEvents("abc", EventsOptions{Since: 7, Limit: 10, Kinds: "doc.updated"})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
}

// ---- claim ----

func TestClaim(t *testing.T) {
	srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/claim" || r.Method != "POST" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body ClaimRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.EditToken == "" {
			t.Error("edit_token missing from body")
		}
		writeEnv(w, true, map[string]int{"claimed": 2})
	}))
	c := NewClient(srv.URL, "t")
	r, err := c.Claim("sk_pur_xyz")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if r.Claimed != 2 {
		t.Errorf("Claimed = %d", r.Claimed)
	}
}

// ---- Me ----

func TestMe_Authed(t *testing.T) {
	srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t" {
			t.Errorf("missing bearer")
		}
		writeEnv(w, true, map[string]any{"id": "u", "email": "a@b", "handle": "a", "via": "api_key"})
	}))
	c := NewClient(srv.URL, "t")
	me, err := c.Me()
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if me.Via != "api_key" || me.Handle != "a" {
		t.Errorf("Me lost fields: %+v", me)
	}
}

// ---- device-flow poll ----

func TestDevicePoll_StatusDispatches(t *testing.T) {
	scenarios := []struct {
		name           string
		status         int
		body           string
		wantErrCode    string
		wantRetryAfter int
		wantOK         bool
	}{
		{
			name:   "approved",
			status: 200,
			body:   `{"ok":true,"data":{"token":"sk_pura_t","token_prefix":"sk_pura_","key_id":"k","scopes":[],"user":{"id":"u","handle":"h","email":"e"}}}`,
			wantOK: true,
		},
		{
			name:        "pending",
			status:      401,
			body:        `{"ok":false,"error":{"code":"authorization_pending","message":"wait"}}`,
			wantErrCode: "authorization_pending",
		},
		{
			name:           "slow_down",
			status:         429,
			body:           `{"ok":false,"error":{"code":"slow_down","message":"ease up","retry_after":7}}`,
			wantErrCode:    "slow_down",
			wantRetryAfter: 7,
		},
		{
			name:        "expired",
			status:      410,
			body:        `{"ok":false,"error":{"code":"expired","message":"gone"}}`,
			wantErrCode: "expired",
		},
	}
	for _, tc := range scenarios {
		t.Run(tc.name, func(t *testing.T) {
			srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			c := NewClient(srv.URL, "")
			res, err := c.DevicePoll("dc")
			if err != nil {
				t.Fatalf("DevicePoll returned error (should surface via res.Error): %v", err)
			}
			if tc.wantOK {
				if res.Approved == nil {
					t.Errorf("want Approved set")
				}
				return
			}
			if res.Error == nil || res.Error.Code != tc.wantErrCode {
				t.Errorf("want error code %q, got %+v", tc.wantErrCode, res.Error)
			}
			if tc.wantRetryAfter > 0 && res.Error.RetryAfter != tc.wantRetryAfter {
				t.Errorf("want retry_after=%d, got %+v", tc.wantRetryAfter, res.Error)
			}
		})
	}
}

// ---- SSE parser ----

func TestParseSSE_ConsumesEventsThenDone(t *testing.T) {
	// Phase 4: the stream protocol replaced `version` with `proposal`;
	// parseSSE itself is protocol-agnostic but we exercise the new shape.
	stream := "" +
		":hb\n\n" + // heartbeat ignored
		"event: ignored\n\n" + // event prefix ignored
		`data: {"type":"token","content":"hi"}` + "\n\n" +
		`data: {"type":"proposal","message_id":"m1","status":"pending","diff_summary":"+1 line"}` + "\n\n" +
		"data: [DONE]\n\n"
	var got []ChatSSEEvent
	err := parseSSE(strings.NewReader(stream), func(e ChatSSEEvent) {
		got = append(got, e)
	}, false)
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 events, got %d: %+v", len(got), got)
	}
	if got[0].Type != "token" || got[0].Content != "hi" {
		t.Errorf("bad first event: %+v", got[0])
	}
	if got[1].Type != "proposal" || got[1].DiffSummary != "+1 line" {
		t.Errorf("bad second event: %+v", got[1])
	}
}

func TestParseSSE_MalformedLineIsSkipped(t *testing.T) {
	stream := "data: not-json\n\ndata: {\"type\":\"token\",\"content\":\"ok\"}\n\ndata: [DONE]\n\n"
	var got []ChatSSEEvent
	err := parseSSE(strings.NewReader(stream), func(e ChatSSEEvent) { got = append(got, e) }, false)
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	if len(got) != 1 || got[0].Content != "ok" {
		t.Errorf("expected 1 clean event, got: %+v", got)
	}
}

func TestParseSSE_UnexpectedEOFWithoutDone(t *testing.T) {
	stream := "data: {\"type\":\"token\",\"content\":\"ok\"}\n\n"
	err := parseSSE(strings.NewReader(stream), func(ChatSSEEvent) {}, false)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("parseSSE err = %v, want unexpected EOF", err)
	}
}

// ---- additional coverage for less-exercised methods ----

func TestChat_WiresBearerAndStreamsEvents(t *testing.T) {
	srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/chat") {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer t" {
			t.Errorf("missing bearer")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, `data: {"type":"token","content":"hi"}`+"\n\n")
		if fl != nil {
			fl.Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	c := NewClient(srv.URL, "t")
	c.Handle = "_"
	var events []ChatSSEEvent
	if err := c.Chat("abc", ChatRequest{Instruction: "go"}, func(e ChatSSEEvent) { events = append(events, e) }); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(events) != 1 || events[0].Content != "hi" {
		t.Errorf("events = %+v", events)
	}
}

func TestChat_Surfaces401AsError(t *testing.T) {
	srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": map[string]string{"code": "unauthorized", "message": "no"}})
	}))
	c := NewClient(srv.URL, "t")
	c.Handle = "_"
	err := c.Chat("abc", ChatRequest{Instruction: "go"}, func(ChatSSEEvent) {})
	if !IsUnauthorized(err) {
		t.Errorf("want IsUnauthorized, got %v", err)
	}
}

func TestUpdate_SendsBearerAndBody(t *testing.T) {
	srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Fatalf("want PUT, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer t" {
			t.Errorf("missing bearer")
		}
		var body UpdateRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Title != "new-title" {
			t.Errorf("body lost title: %+v", body)
		}
		writeEnv(w, true, map[string]any{"slug": "abc", "title": body.Title})
	}))
	c := NewClient(srv.URL, "t")
	c.Handle = "_"
	resp, err := c.Update("abc", UpdateRequest{Title: "new-title"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if resp.Title != "new-title" {
		t.Errorf("Update lost title: %+v", resp)
	}
}

func TestDeviceStart_ReturnsPair(t *testing.T) {
	srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		// Can't reference srv.URL here — fill in via the request Host so the
		// returned URLs loop back through the same test server.
		origin := "http://" + r.Host
		writeEnv(w, true, map[string]any{
			"device_code": "dc", "user_code": "AAAA-1111",
			"verification_url":          origin + "/login/device",
			"verification_url_complete": origin + "/login/device?code=AAAA-1111",
			"expires_in":                600,
			"interval":                  2,
		})
	}))
	c := NewClient(srv.URL, "")
	start, err := c.DeviceStart(DeviceStartRequest{ClientName: "cli:test"})
	if err != nil {
		t.Fatalf("DeviceStart: %v", err)
	}
	if start.DeviceCode != "dc" || start.UserCode != "AAAA-1111" {
		t.Errorf("start lost fields: %+v", start)
	}
}

// ---- SSE edge cases (PLAN §8.2: heartbeat · server-drop) ----

// TestParseSSE_HeartbeatsDoNotAbortStream: CF Workers emit `:keep-alive`
// every ~20s so the edge doesn't close idle connections. The parser must
// treat comment lines as no-ops and keep consuming data frames that
// surround them.
func TestParseSSE_HeartbeatsDoNotAbortStream(t *testing.T) {
	stream := "" +
		":heartbeat\n\n" +
		":keep-alive\n\n" +
		`data: {"type":"token","content":"hello"}` + "\n\n" +
		":heartbeat\n\n" + // mid-stream heartbeat
		`data: {"type":"proposal","message_id":"m1","status":"pending"}` + "\n\n" +
		"data: [DONE]\n\n"

	var got []ChatSSEEvent
	err := parseSSE(strings.NewReader(stream), func(e ChatSSEEvent) { got = append(got, e) }, false)
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	if len(got) != 2 || got[0].Content != "hello" || got[1].Type != "proposal" {
		t.Errorf("events lost across heartbeats: %+v", got)
	}
}

// TestChat_HeartbeatBridgesLongSilence: the full stack (http client +
// parseSSE + caller) must tolerate heartbeat-only bursts before the first
// real frame. Catches any future buffering regression that would stall
// the event loop waiting for a bigger read.
func TestChat_HeartbeatBridgesLongSilence(t *testing.T) {
	srv := newAPITestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fl, _ := w.(http.Flusher)
		// Burst of heartbeats emitted as separate small writes.
		for i := 0; i < 5; i++ {
			_, _ = io.WriteString(w, ":keep-alive\n\n")
			if fl != nil {
				fl.Flush()
			}
		}
		_, _ = io.WriteString(w, `data: {"type":"token","content":"after-heartbeats"}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	c := NewClient(srv.URL, "t")
	c.Handle = "_"
	var events []ChatSSEEvent
	if err := c.Chat("abc", ChatRequest{Instruction: "go"}, func(e ChatSSEEvent) { events = append(events, e) }); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(events) != 1 || events[0].Content != "after-heartbeats" {
		t.Errorf("missed the post-heartbeat event: %+v", events)
	}
}

func TestErrorMethodAndPredicates(t *testing.T) {
	e := &Error{Status: 404, Code: "not_found", Message: "missing"}
	if msg := e.Error(); !strings.Contains(msg, "not_found") {
		t.Errorf("Error() = %q", msg)
	}
	if !IsNotFound(e) || IsUnauthorized(e) || IsForbidden(e) || IsConflict(e) || IsValidation(e) || IsRateLimited(e) {
		t.Errorf("predicates mis-routed: %+v", e)
	}
	eWithHint := &Error{Status: 409, Code: "conflict", Message: "m", Hint: "h"}
	if !strings.Contains(eWithHint.Error(), "Hint: h") {
		t.Errorf("Error() should include hint: %q", eWithHint.Error())
	}
	if !IsConflict(eWithHint) {
		t.Error("IsConflict(409) should be true")
	}
}
