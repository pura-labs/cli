//go:build contract

package api

// Contract-test scaffold — the SSOT drift guard promised by PLAN-CLI.md §8.5.
//
// What this does:
//   - For each API endpoint the CLI consumes, hit a live Pura server
//     (default: PURA_E2E_URL, fallback http://localhost:8787).
//   - Decode the response into the Go types we ship.
//   - Extract the *set of field names* actually present on the wire and
//     compare against a committed snapshot under testdata/contracts/.
//   - New fields on the server → warn but pass (additive, non-breaking).
//   - Missing fields or renamed fields → fail (the CLI would break).
//
// Run with:
//   go test -tags=contract ./internal/api/...          # regular
//   UPDATE_CONTRACTS=1 go test -tags=contract ./...    # regenerate
//
// The contract fixtures live in internal/api/testdata/contracts/
// and are checked into git so a drift shows up as a PR diff.
//
// Why a build tag: contract tests require live HTTP + an auth token to
// hit most endpoints. They're meaningless in a plain `go test ./...`
// run, and CI only fires them against a deployed staging instance.

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const defaultContractURL = "http://localhost:8787"

// contractURL returns the base URL for the Pura server. Falls back to
// wrangler dev on localhost.
func contractURL() string {
	if u := os.Getenv("PURA_E2E_URL"); u != "" {
		return u
	}
	return defaultContractURL
}

// isServerUp pings /health and also validates the Pura signature so a
// stray service on the same port doesn't masquerade as Pura. Same logic
// as the e2e suite — see cli/e2e/e2e_test.go.
func isServerUp() bool {
	client := &http.Client{}
	resp, err := client.Get(contractURL() + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	var body struct {
		OK      bool   `json:"ok"`
		Service string `json:"service"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false
	}
	return body.OK && body.Service == "pura"
}

// contractRequest is one endpoint we want to pin. Keep these small —
// we care about shape (which fields exist), not full semantic coverage.
type contractRequest struct {
	name   string // fixture filename, e.g. "health.json"
	method string // HTTP method
	path   string // relative to contractURL()
	body   []byte // optional request body
	bearer bool   // set true to include Authorization: Bearer <PURA_E2E_TOKEN>
}

// baselineContracts lists the endpoints we contract-test by default.
// Extend thoughtfully — each addition is one more place that fails on
// any server-side rename.
func baselineContracts() []contractRequest {
	return []contractRequest{
		{name: "health.json", method: "GET", path: "/health"},
		// /api/auth/me — only if a token is configured.
		{name: "auth-me.json", method: "GET", path: "/api/auth/me", bearer: true},
		// /api/auth/keys — list
		{name: "auth-keys-list.json", method: "GET", path: "/api/auth/keys", bearer: true},
	}
}

// fieldSet extracts a sorted, newline-separated list of dotted field
// paths from a JSON value. Arrays are flattened to "[].field"; the same
// field appearing in multiple rows collapses to one entry. That keeps
// the fixture stable across payload size changes.
func fieldSet(v any, prefix string, out map[string]struct{}) {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			out[p] = struct{}{}
			fieldSet(val, p, out)
		}
	case []any:
		for _, item := range x {
			p := prefix + "[]"
			fieldSet(item, p, out)
		}
	}
}

// sortedFieldList converts the set to a deterministic string for diffing.
func sortedFieldList(s map[string]struct{}) string {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, "\n") + "\n"
}

func TestContracts(t *testing.T) {
	if !isServerUp() {
		t.Skipf("contract tests need a Pura server at %s — skipping", contractURL())
	}

	token := os.Getenv("PURA_E2E_TOKEN")

	for _, tc := range baselineContracts() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bearer && token == "" {
				t.Skip("PURA_E2E_TOKEN not set — skipping authed contract")
			}
			req, err := http.NewRequest(tc.method, contractURL()+tc.path, nil)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if tc.bearer {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				t.Fatalf("GET %s → %d, want 200", tc.path, resp.StatusCode)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			var parsed any
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Fatalf("parse body: %v — raw: %s", err, string(body))
			}
			fields := map[string]struct{}{}
			fieldSet(parsed, "", fields)
			got := sortedFieldList(fields)

			fixturePath := filepath.Join("testdata", "contracts", tc.name+".fields")
			if os.Getenv("UPDATE_CONTRACTS") == "1" {
				if err := os.MkdirAll(filepath.Dir(fixturePath), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(fixturePath, []byte(got), 0o644); err != nil {
					t.Fatalf("write fixture: %v", err)
				}
				return
			}
			want, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatalf("read fixture %s: %v — first run? set UPDATE_CONTRACTS=1", fixturePath, err)
			}
			if got != string(want) {
				// Distinguish additive drift (new fields) from breaking drift
				// (missing fields). Breaking is a fatal: the CLI WILL start
				// failing for users on the next release.
				gotSet := map[string]struct{}{}
				for _, k := range strings.Split(strings.TrimSpace(got), "\n") {
					gotSet[k] = struct{}{}
				}
				var missing, added []string
				for _, k := range strings.Split(strings.TrimSpace(string(want)), "\n") {
					if _, ok := gotSet[k]; !ok {
						missing = append(missing, k)
					}
				}
				for k := range gotSet {
					wantStr := string(want)
					if !strings.Contains(wantStr, k+"\n") && !strings.HasSuffix(wantStr, k) {
						added = append(added, k)
					}
				}
				if len(missing) > 0 {
					t.Fatalf("contract drift for %s — MISSING fields (breaking):\n  %s\n  added (non-breaking):\n  %s",
						tc.name,
						strings.Join(missing, "\n  "),
						strings.Join(added, "\n  "))
				}
				t.Logf("contract drift for %s — non-breaking additions:\n  %s\n(regenerate with UPDATE_CONTRACTS=1)",
					tc.name,
					strings.Join(added, "\n  "))
			}
		})
	}
}
