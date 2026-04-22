//go:build e2e

// chat_test.go — G6: CLI propose-gate end-to-end against a real Pura server.
//
// These tests exercise the post-Phase-4 CLI semantics:
//   - `pura chat` default = auto-accept (envelope.applied:true, version bumps)
//   - `pura chat --dry-run` = propose + auto-reject (envelope.dry_run:true, version unchanged)
//   - `pura new --describe` = bootstrap SSE + publish in one command
//
// Budget: the anon_create bucket is 10 POST /api/p per hour per IP. To stay
// under it we share a single doc across both chat subtests via t.Run —
// total spend per full e2e run is 2 creates (one for chat, one for new).
// If the IP is rate-limited the sub-tests skip cleanly instead of failing.
//
// Require: live Pura server with OPENROUTER_API_KEY server-side. Tokens
// flow through --token; anon docs live under @_ so we force --handle _ so
// stored user-handle config doesn't leak (the CLI honours saved handle
// unless explicitly overridden — cf. ~/.config/pura/config.json).

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// createAnonDoc POSTs to /api/p and returns (slug, token). On 429 it returns
// ("", "") so the caller can t.Skip cleanly when the anon_create bucket is
// exhausted — common on shared CI/dev IPs.
func createAnonDoc(t *testing.T, title, content string) (string, string) {
	t.Helper()
	body := fmt.Sprintf(
		`{"content":%q,"substrate":"markdown","title":%q}`,
		content, title,
	)
	resp, err := http.Post(apiURL()+"/api/p", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 {
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		t.Skipf("anon_create rate-limited by %s — %s", apiURL(), b.String())
	}
	if resp.StatusCode != 201 {
		var b bytes.Buffer
		_, _ = b.ReadFrom(resp.Body)
		t.Fatalf("create: expected 201, got %d — %s", resp.StatusCode, b.String())
	}
	var out struct {
		OK   bool `json:"ok"`
		Data struct {
			Slug  string `json:"slug"`
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out.Data.Slug, out.Data.Token
}

func deleteDoc(t *testing.T, slug, token string) {
	t.Helper()
	req, _ := http.NewRequest("DELETE", apiURL()+"/api/p/@_/"+slug, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("cleanup delete: %v", err)
		return
	}
	resp.Body.Close()
}

func getRaw(t *testing.T, slug string) string {
	t.Helper()
	resp, err := http.Get(apiURL() + "/@_/" + slug + "/raw")
	if err != nil {
		t.Fatalf("raw: %v", err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return buf.String()
}

// TestE2EChatProposal exercises both `pura chat` paths against one shared
// anon doc — 1 create + 2 AI calls per run — to stay within the anon_create
// bucket. Subtests share the doc via t.Run; the dry-run subtest must run
// FIRST so it can verify the unchanged baseline, then auto-accept mutates.
func TestE2EChatProposal(t *testing.T) {
	if !isServerUp() {
		t.Skip("API server not running at " + apiURL())
	}
	const before = "# CLI Chat Propose Gate\n\nThe original paragraph reads boringly."
	slug, token := createAnonDoc(t, "CLI Chat Propose Gate", before)
	if slug == "" {
		return // createAnonDoc already called t.Skip
	}
	t.Cleanup(func() { deleteDoc(t, slug, token) })

	t.Run("dry_run", func(t *testing.T) {
		// --dry-run must propose, then auto-reject. envelope.dry_run=true,
		// doc bytes identical to the seed, no version bump.
		out, err := puraCmd(
			"chat", slug, "rewrite the paragraph in a dramatic voice",
			"--api-url", apiURL(),
			"--handle", "_",
			"--token", token,
			"--no-stream",
			"--json",
			"--dry-run",
		)
		if err != nil {
			t.Fatalf("pura chat --dry-run: %v\nOutput:\n%s", err, out)
		}
		var env struct {
			OK   bool `json:"ok"`
			Data struct {
				Applied bool `json:"applied"`
				DryRun  bool `json:"dry_run"`
			} `json:"data"`
		}
		if err := json.NewDecoder(strings.NewReader(out)).Decode(&env); err != nil {
			t.Fatalf("envelope decode: %v — raw:\n%s", err, out)
		}
		if !env.OK {
			t.Fatalf("expected ok:true, got:\n%s", out)
		}
		if !env.Data.DryRun {
			t.Fatalf("--dry-run should set dry_run:true, got applied=%v in:\n%s",
				env.Data.Applied, out)
		}
		if env.Data.Applied {
			t.Fatalf("--dry-run must not apply, got applied:true in:\n%s", out)
		}
		raw := getRaw(t, slug)
		if strings.TrimSpace(raw) != strings.TrimSpace(before) {
			t.Fatalf("--dry-run should not mutate the doc; got:\n%s", raw)
		}
	})

	t.Run("auto_accept", func(t *testing.T) {
		out, err := puraCmd(
			"chat", slug, "rewrite the paragraph to be one short exciting sentence",
			"--api-url", apiURL(),
			"--handle", "_",
			"--token", token,
			"--no-stream",
			"--json",
			"--yes",
		)
		if err != nil {
			t.Fatalf("pura chat (auto-accept): %v\nOutput:\n%s", err, out)
		}
		var env struct {
			OK   bool `json:"ok"`
			Data struct {
				Applied   bool   `json:"applied"`
				DryRun    bool   `json:"dry_run"`
				BeforeVer int    `json:"before_version"`
				AfterVer  int    `json:"after_version"`
				MessageID string `json:"message_id"`
			} `json:"data"`
		}
		if err := json.NewDecoder(strings.NewReader(out)).Decode(&env); err != nil {
			t.Fatalf("envelope decode: %v — raw:\n%s", err, out)
		}
		if !env.OK {
			t.Fatalf("expected ok:true, got:\n%s", out)
		}
		if !env.Data.Applied {
			t.Fatalf("default run should set applied:true, got dry_run=%v in:\n%s",
				env.Data.DryRun, out)
		}
		if env.Data.BeforeVer == 0 || env.Data.AfterVer == 0 {
			t.Fatalf("expected numeric before/after versions, got %+v", env.Data)
		}
		if env.Data.AfterVer <= env.Data.BeforeVer {
			t.Fatalf("after_version (%d) must exceed before_version (%d)",
				env.Data.AfterVer, env.Data.BeforeVer)
		}
		if env.Data.MessageID == "" {
			t.Errorf("message_id should be populated")
		}
		raw := getRaw(t, slug)
		if strings.TrimSpace(raw) == strings.TrimSpace(before) {
			t.Fatalf("content unchanged after auto-accept:\n%s", raw)
		}
	})
}

// TestE2ENew_Describe — Phase-4 chat-first `pura new --describe`. The CLI
// drives /api/p/bootstrap (SSE) client-side, then POSTs /api/p with a
// bootstrap_thread payload. The JSON envelope must carry the published
// slug + url.
func TestE2ENew_Describe(t *testing.T) {
	if !isServerUp() {
		t.Skip("API server not running at " + apiURL())
	}

	out, err := puraCmd(
		"new",
		"--api-url", apiURL(),
		"--describe", "a concise note about cloudflare workers durable objects (3 bullets max)",
		"--starter", "blog",
		"--yes",
		"--json",
	)
	if err != nil {
		// Rate-limited anon_create can surface under several error codes
		// ("publish_failed", "api_error", or bare "rate_limit") depending on
		// which layer throws. Skip when any mention of rate_limit appears.
		if strings.Contains(out, "rate_limit") || strings.Contains(out, "Rate limit") {
			t.Skipf("anon_create rate-limited — skipping:\n%s", out)
		}
		t.Fatalf("pura new --describe: %v\nOutput:\n%s", err, out)
	}

	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Slug  string `json:"slug"`
			URL   string `json:"url"`
			Token string `json:"token"`
			Kind  string `json:"kind"`
		} `json:"data"`
	}
	if err := json.NewDecoder(strings.NewReader(out)).Decode(&env); err != nil {
		t.Fatalf("envelope decode: %v — raw:\n%s", err, out)
	}
	if !env.OK || env.Data.Slug == "" || env.Data.URL == "" || env.Data.Token == "" {
		t.Fatalf("expected ok:true + populated slug/url/token, got:\n%s", out)
	}
	if env.Data.Kind != "doc" {
		t.Errorf("expected kind=doc for blog starter, got %q", env.Data.Kind)
	}
	t.Cleanup(func() { deleteDoc(t, env.Data.Slug, env.Data.Token) })

	raw := getRaw(t, env.Data.Slug)
	if len(strings.TrimSpace(raw)) < 50 {
		t.Errorf("draft content suspiciously short (%d chars):\n%s", len(raw), raw)
	}
}
