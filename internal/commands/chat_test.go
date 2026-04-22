package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// chatSSEServer scripts a fake SSE stream + proposal-related endpoints so
// the chat command can run end-to-end without a real Pura worker. Each
// test hands us the frames to emit and toggles the accept/reject
// responses it wants to see.
type chatSSEServer struct {
	srv             *httptest.Server
	frames          []string
	acceptCalls     int
	acceptStatus    int
	acceptAfter     int
	rejectCalls     int
	rejectStatus    int
	messagesStatus  int
	messagesPayload []map[string]any
}

func newChatSSE(t *testing.T, slug, handle string, frames []string) *chatSSEServer {
	t.Helper()
	fs := &chatSSEServer{
		frames:         frames,
		acceptStatus:   200,
		acceptAfter:    4,
		rejectStatus:   200,
		messagesStatus: 200,
	}
	mux := http.NewServeMux()

	chatPath := fmt.Sprintf("/api/p/@%s/%s/chat", handle, slug)
	mux.HandleFunc(chatPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		for _, f := range fs.frames {
			_, _ = w.Write([]byte(f))
			if flusher != nil {
				flusher.Flush()
			}
		}
	})

	// Accept prefix — we respond with a scriptable status.
	mux.HandleFunc(fmt.Sprintf("/api/p/@%s/%s/chat/", handle, slug), func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/accept") {
			fs.acceptCalls++
			if fs.acceptStatus >= 400 {
				w.WriteHeader(fs.acceptStatus)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok":    false,
					"error": map[string]any{"code": "stale", "message": "doc changed"},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"data": map[string]any{
					"message_id":     "m_test",
					"before_version": 3,
					"after_version":  fs.acceptAfter,
					"version_id":     "v_test",
				},
			})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/reject") || strings.HasSuffix(r.URL.Path, "/discard") {
			fs.rejectCalls++
			if fs.rejectStatus >= 400 {
				w.WriteHeader(fs.rejectStatus)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ok":    false,
					"error": map[string]any{"code": "validation", "message": "already resolved"},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"data": map[string]any{
					"message_id": "m_test",
					"reason":     "reject",
				},
			})
			return
		}
		w.WriteHeader(404)
	})

	// Messages endpoint used by --resolve to find the pending row.
	mux.HandleFunc(fmt.Sprintf("/api/p/@%s/%s/messages", handle, slug), func(w http.ResponseWriter, r *http.Request) {
		if fs.messagesStatus >= 400 {
			w.WriteHeader(fs.messagesStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"data": fs.messagesPayload,
		})
	})

	fs.srv = httptest.NewServer(mux)
	t.Cleanup(fs.srv.Close)
	return fs
}

// frame is a helper to build a well-formed SSE `data:` record.
func frame(e map[string]any) string {
	b, _ := json.Marshal(e)
	return "data: " + string(b) + "\n\n"
}

// --- tests ---

func TestChat_RequiresAuth(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = "http://127.0.0.1:1"
	_, err := runCmd(t, "chat", "xy12", "rewrite this")
	if err == nil || err.Error() != "no token" {
		t.Fatalf("want no token, got %v", err)
	}
}

func TestChat_RejectsEmptyInstruction(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = "http://127.0.0.1:1"
	flagToken = "sk_pura_t"
	_, err := runCmd(t, "chat", "xy12", "   ")
	if err == nil {
		t.Fatal("want error for empty instruction")
	}
}

func TestChat_AutoAcceptsProposal(t *testing.T) {
	setupIsolatedHome(t)
	fs := newChatSSE(t, "xy12", "_", []string{
		frame(map[string]any{"type": "message", "message_id": "m1", "before_version": 3}),
		frame(map[string]any{"type": "token", "content": "Hel"}),
		frame(map[string]any{"type": "token", "content": "lo"}),
		frame(map[string]any{"type": "proposal", "message_id": "m1", "status": "pending", "diff_summary": "+1 line", "destructive": false}),
		frame(map[string]any{"type": "usage", "model": "claude-haiku-4-5", "prompt_tokens": 10, "completion_tokens": 3}),
		"data: [DONE]\n\n",
	})
	fs.acceptAfter = 4
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_t"

	out, err := runCmd(t, "chat", "xy12", "tighten the intro", "--json")
	if err != nil {
		t.Fatalf("chat: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)

	if data["applied"] != true {
		t.Errorf("applied should be true")
	}
	if data["after_version"].(float64) != 4 {
		t.Errorf("after_version = %v", data["after_version"])
	}
	if fs.acceptCalls != 1 {
		t.Errorf("acceptCalls = %d, want 1", fs.acceptCalls)
	}
	if fs.rejectCalls != 0 {
		t.Errorf("rejectCalls = %d, want 0", fs.rejectCalls)
	}
}

func TestChat_DryRunRejects(t *testing.T) {
	setupIsolatedHome(t)
	fs := newChatSSE(t, "xy12", "_", []string{
		frame(map[string]any{"type": "message", "message_id": "m1", "before_version": 7}),
		frame(map[string]any{"type": "proposal", "message_id": "m1", "status": "pending", "diff_summary": "+1 row", "destructive": false}),
		"data: [DONE]\n\n",
	})
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_t"

	out, err := runCmd(t, "chat", "xy12", "experiment", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("chat --dry-run: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)

	if data["applied"] != false {
		t.Errorf("applied should be false on dry-run")
	}
	if data["dry_run"] != true {
		t.Errorf("dry_run should be true")
	}
	if fs.rejectCalls != 1 {
		t.Errorf("rejectCalls = %d, want 1 on --dry-run", fs.rejectCalls)
	}
	if fs.acceptCalls != 0 {
		t.Errorf("acceptCalls should be 0 on --dry-run")
	}
}

func TestChat_NoopProposalReportsNoChange(t *testing.T) {
	setupIsolatedHome(t)
	fs := newChatSSE(t, "xy12", "_", []string{
		frame(map[string]any{"type": "message", "message_id": "m1", "before_version": 2}),
		frame(map[string]any{"type": "token", "content": "ok"}),
		frame(map[string]any{"type": "noop", "message_id": "m1", "message": "nothing to change"}),
		"data: [DONE]\n\n",
	})
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_t"

	out, err := runCmd(t, "chat", "xy12", "verify schema", "--json")
	if err != nil {
		t.Fatalf("chat: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)
	if data["applied"] != false {
		t.Errorf("applied should be false on noop")
	}
	if fs.acceptCalls != 0 || fs.rejectCalls != 0 {
		t.Errorf("noop should not call accept or reject")
	}
}

func TestChat_AcceptStaleReturnsError(t *testing.T) {
	setupIsolatedHome(t)
	fs := newChatSSE(t, "xy12", "_", []string{
		frame(map[string]any{"type": "message", "message_id": "m1", "before_version": 3}),
		frame(map[string]any{"type": "proposal", "message_id": "m1", "status": "pending", "diff_summary": "+1", "destructive": false}),
		"data: [DONE]\n\n",
	})
	fs.acceptStatus = 409
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_t"

	_, err := runCmd(t, "chat", "xy12", "go", "--json")
	if err == nil {
		t.Fatal("want error when accept fails with 409 stale")
	}
	if fs.acceptCalls != 1 {
		t.Errorf("acceptCalls = %d, want 1", fs.acceptCalls)
	}
}

func TestChat_SurfaceStreamErrorEvent(t *testing.T) {
	setupIsolatedHome(t)
	fs := newChatSSE(t, "xy12", "_", []string{
		frame(map[string]any{"type": "message", "message_id": "m1", "before_version": 1}),
		frame(map[string]any{"type": "token", "content": "partial "}),
		frame(map[string]any{"type": "error", "message": "model quota exceeded", "error_code": "quota"}),
		"data: [DONE]\n\n",
	})
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_t"

	_, err := runCmd(t, "chat", "xy12", "go", "--json")
	if err == nil {
		t.Fatal("want error when stream carries `type:error`")
	}
	if !strings.Contains(err.Error(), "model quota exceeded") {
		t.Errorf("err didn't preserve message: %v", err)
	}
}

func TestChat_ServerRejectsUpfront(t *testing.T) {
	setupIsolatedHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    false,
			"error": map[string]string{"code": "unauthorized", "message": "bad token"},
		})
	}))
	defer srv.Close()

	flagAPIURL = srv.URL
	flagToken = "sk_pura_t"

	_, err := runCmd(t, "chat", "xy12", "go", "--json")
	if err == nil {
		t.Fatal("want 401 to surface as error")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("err = %v", err)
	}
}

func TestChat_RequiresProposalOrNoopEventBeforeSuccess(t *testing.T) {
	setupIsolatedHome(t)
	fs := newChatSSE(t, "xy12", "_", []string{
		frame(map[string]any{"type": "message", "message_id": "m1", "before_version": 2}),
		frame(map[string]any{"type": "token", "content": "partial"}),
		"data: [DONE]\n\n",
	})
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_t"

	_, err := runCmd(t, "chat", "xy12", "go", "--json")
	if err == nil {
		t.Fatal("want error when stream ends without proposal or noop")
	}
	if !strings.Contains(err.Error(), "no proposal event") {
		t.Errorf("err = %v", err)
	}
}

func TestChat_409PendingExistsWithResolve(t *testing.T) {
	setupIsolatedHome(t)

	// First call: 409. Second call (after resolve): success.
	var chatCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/p/@_/xy12/chat", func(w http.ResponseWriter, r *http.Request) {
		chatCalls++
		if chatCalls == 1 {
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok":    false,
				"error": map[string]any{"code": "pending_exists", "message": "previous proposal still pending"},
			})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher, _ := w.(http.Flusher)
		for _, f := range []string{
			frame(map[string]any{"type": "message", "message_id": "m2", "before_version": 5}),
			frame(map[string]any{"type": "proposal", "message_id": "m2", "status": "pending", "diff_summary": "+1", "destructive": false}),
			"data: [DONE]\n\n",
		} {
			_, _ = w.Write([]byte(f))
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	// Messages list returns the stuck pending.
	mux.HandleFunc("/api/p/@_/xy12/messages", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"data": []map[string]any{
				{"id": "m_pending", "role": "assistant", "proposal_status": "pending"},
			},
		})
	})
	// Reject the stuck pending.
	mux.HandleFunc("/api/p/@_/xy12/chat/m_pending/reject", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"data": map[string]any{"message_id": "m_pending", "reason": "reject"},
		})
	})
	// Accept for the retry.
	mux.HandleFunc("/api/p/@_/xy12/chat/m2/accept", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"data": map[string]any{
				"message_id":     "m2",
				"before_version": 5,
				"after_version":  6,
				"version_id":     "v_new",
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	flagAPIURL = srv.URL
	flagToken = "sk_pura_t"

	out, err := runCmd(t, "chat", "xy12", "go", "--resolve=reject", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if chatCalls != 2 {
		t.Errorf("chat should have been called twice (first 409 then retry); got %d", chatCalls)
	}
}

// parseSSE itself — unit-level coverage of the line parser.
func TestParseSSE_IgnoresCommentsAndUnknownLines(t *testing.T) {
	// We test the exported package behavior via chatSSEServer indirectly,
	// but this end-to-end "weird input" guard is cheap and prevents
	// regressions in parseSSE().
	setupIsolatedHome(t)
	fs := newChatSSE(t, "xy12", "_", []string{
		":heartbeat\n\n",
		"event: ignored\n\n",
		"random garbage\n\n",
		frame(map[string]any{"type": "message", "message_id": "m1", "before_version": 2}),
		frame(map[string]any{"type": "token", "content": "ok"}),
		frame(map[string]any{"type": "proposal", "message_id": "m1", "status": "pending", "diff_summary": "x", "destructive": false}),
		"data: [DONE]\n\n",
	})
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_t"

	out, err := runCmd(t, "chat", "xy12", "go", "--json")
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)
	if data["applied"] != true {
		t.Errorf("applied should be true after noisy stream")
	}
}
