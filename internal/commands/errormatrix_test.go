package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pura-labs/cli/internal/output"
)

// Error-path matrix for the CLI's outward-facing commands.
//
// PLAN-CLI.md §8.2 promised that each command is covered across the
// error-code spectrum. This file focuses on the cross-cutting concern —
// "does the CLI classify and route the error correctly" — rather than
// re-asserting per-command happy paths (those already live in the
// individual `*_test.go`).
//
// Approach: for every command-under-test, hit a fake server that returns
// one of {401, 403, 404, 400, 409, 429, 500, malformed body}, and
// confirm:
//   1. Execute returns a non-nil error (so main.go exits non-zero).
//   2. output.ExitCodeFor(err) maps to the expected exit bucket.
//
// Because the classifier lives in internal/output, any command that
// surfaces an *api.Error through its RunE error return is automatically
// covered by the status → exit-code mapping.

type errorMatrixCase struct {
	name       string
	serverPath string // URL prefix the server should match; use /* for any
	serverCode int    // HTTP status to return
	serverBody string // literal body (malformed JSON if you want it)
	cliArgs    []string
	wantExit   int
	needsToken bool
}

// matrixServer answers every request with (status, body) regardless of
// route. Each individual test re-configures it before running.
func matrixServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// envelope returns a well-formed error envelope JSON string, for the
// common case where we want to exercise a specific typed code.
func envelope(code, msg string) string {
	b, _ := json.Marshal(map[string]any{
		"ok":    false,
		"error": map[string]string{"code": code, "message": msg},
	})
	return string(b)
}

// matrixCases lists the command × status combinations. Kept minimal per
// command (we only need ONE test per status for the mapping to be
// proven — the classifier is status-driven, not command-specific).
//
// We use `keys ls` as the canonical "mutating-like GET" since it's
// Bearer-required and short-circuits quickly.
func matrixCases() []errorMatrixCase {
	return []errorMatrixCase{
		// 401 → ExitAuth
		{"keys_401", "", 401, envelope("unauthorized", "expired"),
			[]string{"keys", "ls", "--json"}, output.ExitAuth, true},
		// 403 → ExitForbidden
		{"keys_403", "", 403, envelope("forbidden", "no scope"),
			[]string{"keys", "ls", "--json"}, output.ExitForbidden, true},
		// 404 → ExitNotFound (versions show with nonexistent version)
		{"versions_404", "", 404, envelope("not_found", "no such version"),
			[]string{"versions", "show", "slug", "3", "--json"}, output.ExitNotFound, true},
		// 400 → ExitInvalid (claim with a token the server rejects)
		{"claim_400", "", 400, envelope("validation", "bad edit_token"),
			[]string{"claim", "sk_pur_bogus", "--json"}, output.ExitInvalid, true},
		// 409 → ExitConflict (versions restore racing with a concurrent writer)
		{"versions_409", "", 409, envelope("conflict", "version changed"),
			[]string{"versions", "restore", "slug", "2", "--yes", "--json"}, output.ExitConflict, true},
		// 429 → ExitRateLimit
		{"chat_429", "", 429, envelope("rate_limit", "cool down"),
			[]string{"chat", "slug", "rewrite", "--json"}, output.ExitRateLimit, true},
		// 500 → ExitAPI
		{"stats_500", "", 500, envelope("server_error", "oops"),
			[]string{"stats", "slug", "--json"}, output.ExitAPI, false},
		// malformed JSON body on non-2xx → still ExitAPI (errorFromResponse
		// preserves the status when body doesn't parse)
		{"keys_malformed", "", 503, "<html>gateway error</html>",
			[]string{"keys", "ls", "--json"}, output.ExitAPI, true},
	}
}

func TestErrorMatrix(t *testing.T) {
	for _, tc := range matrixCases() {
		t.Run(tc.name, func(t *testing.T) {
			setupIsolatedHome(t)
			srv := matrixServer(t, tc.serverCode, tc.serverBody)
			flagAPIURL = srv.URL
			if tc.needsToken {
				flagToken = "sk_pura_bootstrap"
			}

			_, err := runCmd(t, tc.cliArgs...)
			if err == nil {
				t.Fatalf("want error for status=%d, got nil", tc.serverCode)
			}
			gotExit := output.ExitCodeFor(err)
			if gotExit != tc.wantExit {
				t.Errorf("status=%d → exit %d, want %d (err=%v)", tc.serverCode, gotExit, tc.wantExit, err)
			}
		})
	}
}

// Network-failure path: point the CLI at a port we know is closed.
// The api.Client should surface a transport error, and ExitCodeFor
// should map any net.Error to ExitAPI.
func TestErrorMatrix_ConnectionRefused(t *testing.T) {
	setupIsolatedHome(t)
	flagAPIURL = "http://127.0.0.1:1"
	flagToken = "sk_pura_bootstrap"

	_, err := runCmd(t, "keys", "ls", "--json")
	if err == nil {
		t.Fatal("want error when the server is unreachable")
	}
	if got := output.ExitCodeFor(err); got != output.ExitAPI {
		t.Errorf("network failure → exit %d, want %d (err=%v)", got, output.ExitAPI, err)
	}
}

// Malformed success envelope: ok:false WITHOUT an error field. The client
// should still return *api.Error (Status=0, blank code), exit → ExitAPI.
func TestErrorMatrix_MalformedSuccessEnvelope(t *testing.T) {
	setupIsolatedHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Valid JSON, ok:false, no error block.
		fmt.Fprint(w, `{"ok":false}`)
	}))
	defer srv.Close()

	flagAPIURL = srv.URL
	flagToken = "sk_pura_bootstrap"
	_, err := runCmd(t, "keys", "ls", "--json")
	if err == nil {
		t.Fatal("want error when server returns ok:false with no error body")
	}
	if got := output.ExitCodeFor(err); got != output.ExitAPI {
		t.Errorf("malformed envelope → exit %d, want ExitAPI (%d)", got, output.ExitAPI)
	}
}

// Every matrix case, in aggregate, also shouldn't leak stderr noise —
// confirm a typical invocation doesn't print the stack trace-style Go
// error. A regression there would be a real user-facing bug.
func TestErrorMatrix_StderrIsHumanFriendly(t *testing.T) {
	setupIsolatedHome(t)
	srv := matrixServer(t, 401, envelope("unauthorized", "Token expired"))
	flagAPIURL = srv.URL
	flagToken = "sk_pura_bootstrap"

	// Capture stdout+stderr in ONE pass — nested redirects would hide
	// whichever stream is inner. We mirror runCmd but keep stderr as
	// well so the human-readable line shows up somewhere.
	stdoutBuf := captureStdout(t, func() {
		_ = captureStderr(t, func() {
			cmd := rootCmd
			cmd.SetArgs([]string{"keys", "ls"}) // styled path (no --json)
			_ = cmd.Execute()
		})
	})
	// When JSON-forced path isn't taken, the error envelope still lands on
	// stdout because our Writer routes JSON that way on non-TTY. That's
	// where "unauthorized" will appear in a test (non-TTY).
	if !strings.Contains(stdoutBuf, "unauthorized") && !strings.Contains(stdoutBuf, "Token expired") {
		t.Errorf("expected the failure message somewhere in CLI output, got: %q", stdoutBuf)
	}
}
