package output

import (
	"errors"
	"net"

	"github.com/pura-labs/cli/internal/api"
)

// Exit codes — keep this table in lock-step with the one in PLAN-CLI.md §7
// and the reference CLI projects (basecamp-cli, fizzy-cli, hey-cli).
//
// Agents branch on these in wrapper scripts; changing a value breaks their
// scripts silently. Only add new codes, never renumber.
const (
	ExitOK        = 0 // success
	ExitGeneric   = 1 // unclassified failure (reserved for anything we don't map)
	ExitAuth      = 2 // 401 — user must re-authenticate
	ExitForbidden = 3 // 403 — authenticated but lacks scope / ownership
	ExitNotFound  = 4 // 404
	ExitInvalid   = 5 // 400 validation
	ExitConflict  = 6 // 409
	ExitRateLimit = 7 // 429
	ExitAPI       = 8 // 5xx, network, timeout, malformed response
)

// ExitCodeFor maps any error returned by a command to the table above.
//
// Resolution order:
//  1. nil error → ExitOK
//  2. *api.Error (HTTP status known) → matched by status
//  3. network / URL errors, DNS failures → ExitAPI
//  4. unknown / typed CLI errors → ExitGeneric
//
// Callers just do `os.Exit(output.ExitCodeFor(err))`.
func ExitCodeFor(err error) int {
	if err == nil {
		return ExitOK
	}

	if ae := api.AsError(err); ae != nil {
		switch ae.Status {
		case 400:
			return ExitInvalid
		case 401:
			return ExitAuth
		case 403:
			return ExitForbidden
		case 404:
			return ExitNotFound
		case 409:
			return ExitConflict
		case 429:
			return ExitRateLimit
		}
		// 5xx + Status=0 (parse/envelope-level failures) → ExitAPI.
		if ae.Status >= 500 || ae.Status == 0 {
			return ExitAPI
		}
		// Any remaining 4xx we didn't explicitly model → ExitGeneric so
		// we don't silently map "418 teapot" to ExitAPI.
		return ExitGeneric
	}

	// Bare transport failures — the client never saw an HTTP response.
	// Map to ExitAPI since that's the bucket for "we couldn't reach Pura".
	var netErr net.Error
	if errors.As(err, &netErr) {
		return ExitAPI
	}
	// URL parse failures and stdlib DNS errors come back as *net.OpError /
	// *url.Error wrapping net.Error — caught above. Anything else (file I/O,
	// flag parsing, etc.) stays Generic.
	return ExitGeneric
}
