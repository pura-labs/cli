package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Single fake for both stats and events, covering the routes the CLI calls.
type obsFake struct {
	srv      *httptest.Server
	events   []map[string]any // in id order
	detailed bool             // true → /stats serves detailed shape when ?detail=full
}

func newObsFake(t *testing.T, slug, handle string) *obsFake {
	t.Helper()
	fs := &obsFake{}
	mux := http.NewServeMux()

	// /api/p/@h/s/stats
	mux.HandleFunc(fmt.Sprintf("/api/p/@%s/%s/stats", handle, slug), func(w http.ResponseWriter, r *http.Request) {
		q, _ := url.ParseQuery(r.URL.RawQuery)
		if q.Get("detail") == "full" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"data": map[string]any{
					"views":            17,
					"unique_countries": 4,
					"view_types":       map[string]int{"page": 10, "raw": 5, "ctx": 2},
					"bot_ratio":        0.18,
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"data": map[string]int{"views": 17},
		})
	})

	// /api/p/@h/s/events
	mux.HandleFunc(fmt.Sprintf("/api/p/@%s/%s/events", handle, slug), func(w http.ResponseWriter, r *http.Request) {
		q, _ := url.ParseQuery(r.URL.RawQuery)
		var since int
		if s := q.Get("since"); s != "" {
			fmt.Sscanf(s, "%d", &since)
		}
		var filtered []map[string]any
		for _, e := range fs.events {
			if int(e["id"].(float64)) > since {
				filtered = append(filtered, e)
			}
		}
		cursor := since
		if len(filtered) > 0 {
			cursor = int(filtered[len(filtered)-1]["id"].(float64))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":   true,
			"data": map[string]any{"cursor": cursor, "events": filtered},
		})
	})

	fs.srv = httptest.NewServer(mux)
	t.Cleanup(fs.srv.Close)
	return fs
}

func (fs *obsFake) addEvent(id int, kind string) {
	fs.events = append(fs.events, map[string]any{
		"id":         float64(id),
		"kind":       kind,
		"created_at": "2026-04-17T10:00:00Z",
		"props":      nil,
	})
}

// ---- stats tests ----

func TestStats_PublicBasic(t *testing.T) {
	setupIsolatedHome(t)
	fs := newObsFake(t, "xy12", "_")
	flagAPIURL = fs.srv.URL

	out, err := runCmd(t, "stats", "xy12", "--json")
	if err != nil {
		t.Fatalf("stats: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)
	if data["views"].(float64) != 17 {
		t.Errorf("views = %v, want 17", data["views"])
	}
	// Breadcrumb should suggest --detail.
	crumbs, _ := env["breadcrumbs"].([]any)
	if len(crumbs) == 0 {
		t.Errorf("want breadcrumbs on basic stats")
	}
}

func TestStats_DetailRequiresAuth(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = "http://127.0.0.1:1"

	_, err := runCmd(t, "stats", "xy12", "--detail", "--json")
	if err == nil || err.Error() != "no token" {
		t.Fatalf("want no token, got %v", err)
	}
}

func TestStats_DetailReturnsBreakdown(t *testing.T) {
	setupIsolatedHome(t)
	fs := newObsFake(t, "xy12", "_")
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_owner"

	out, err := runCmd(t, "stats", "xy12", "--detail", "--json")
	if err != nil {
		t.Fatalf("stats --detail: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)
	if data["unique_countries"].(float64) != 4 {
		t.Errorf("unique_countries = %v", data["unique_countries"])
	}
	if data["bot_ratio"].(float64) <= 0 {
		t.Errorf("bot_ratio should be > 0")
	}
}

// ---- events tests ----

func TestEvents_ReturnsOneshotPage(t *testing.T) {
	setupIsolatedHome(t)
	fs := newObsFake(t, "xy12", "_")
	fs.addEvent(1, "doc.updated")
	fs.addEvent(2, "comment.added")
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_owner"

	out, err := runCmd(t, "events", "xy12", "--json")
	if err != nil {
		t.Fatalf("events: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	data := env["data"].(map[string]any)
	events := data["events"].([]any)
	if len(events) != 2 {
		t.Errorf("want 2 events, got %d", len(events))
	}
	if data["cursor"].(float64) != 2 {
		t.Errorf("cursor = %v, want 2", data["cursor"])
	}
}

func TestEvents_RespectsSinceCursor(t *testing.T) {
	setupIsolatedHome(t)
	fs := newObsFake(t, "xy12", "_")
	fs.addEvent(1, "doc.updated")
	fs.addEvent(2, "comment.added")
	fs.addEvent(3, "version.restored")
	flagAPIURL = fs.srv.URL
	flagToken = "sk_pura_owner"

	out, err := runCmd(t, "events", "xy12", "--since", "1", "--json")
	if err != nil {
		t.Fatalf("events --since: %v", err)
	}
	env := mustUnmarshalEnvelope(t, out)
	events := env["data"].(map[string]any)["events"].([]any)
	if len(events) != 2 {
		t.Fatalf("want 2 post-cursor events, got %d", len(events))
	}
	first := events[0].(map[string]any)
	if first["kind"] != "comment.added" {
		t.Errorf("first event = %v, want comment.added", first["kind"])
	}
}

func TestEvents_RequiresAuth(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = "http://127.0.0.1:1"

	_, err := runCmd(t, "events", "xy12", "--json")
	if err == nil || err.Error() != "no token" {
		t.Fatalf("want no token, got %v", err)
	}
	// Quiet the linter / strings import.
	_ = strings.ToLower("")
}
