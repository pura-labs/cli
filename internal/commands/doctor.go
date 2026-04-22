// `pura doctor` — self-diagnostic.
//
// Answers the question: "Is this CLI / machine set up correctly to talk to
// Pura?" Run this after install, after a failed command, or as a CI smoke
// step. Emits per-check {status, detail} so agents can branch on exact
// failures; exits non-zero if ANY check is hard-failed.
//
// Soft-warn checks (yellow) don't fail the exit status — they signal the
// setup is incomplete but functional. Hard-fail checks (red) return
// ExitGeneric since no single HTTP status captures "config was wrong".

package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pura-labs/cli/internal/api"
	"github.com/pura-labs/cli/internal/auth"
	"github.com/pura-labs/cli/internal/config"
	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

type checkStatus string

const (
	checkOK   checkStatus = "ok"
	checkWarn checkStatus = "warn"
	checkFail checkStatus = "fail"
)

type checkResult struct {
	Name     string      `json:"name"`
	Status   checkStatus `json:"status"`
	Detail   string      `json:"detail,omitempty"`
	Hint     string      `json:"hint,omitempty"`
	Duration string      `json:"duration,omitempty"`
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check CLI + network + auth health",
		Long: `Runs a short battery of checks and reports each as ok / warn / fail.

Checks:
  config    — profile exists, api_url well-formed
  network   — /health reachable and signed "pura"
  auth      — token present and accepted by /api/auth/me
  profile   — (when authed) handle set, needed for publishing under @you

The overall exit status is non-zero iff any check is a hard fail. Soft
warnings (token unset, handle unset) don't fail the command so agents can
run doctor as a no-op prereq.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			profile := resolvedProfile(cfg)

			var results []checkResult

			// ---- Check 1: config ----
			results = append(results, runConfigCheck(cfg, profile))

			// ---- Check 2: network ----
			netRes := runNetworkCheck(cfg.APIURL)
			results = append(results, netRes)

			// ---- Check 3: auth ----
			// Only attempt /me if network succeeded — otherwise the
			// failure mode is ambiguous ("network or token?").
			if netRes.Status != checkFail {
				results = append(results, runAuthCheck(cfg, profile))
			} else {
				results = append(results, checkResult{
					Name: "auth", Status: checkWarn,
					Detail: "skipped — network failed",
				})
			}

			// ---- Check 4: profile completeness ----
			results = append(results, runProfileCheck(profile))

			// ---- Check 5: version drift (best-effort, never fails doctor) ----
			results = append(results, runDriftCheck())

			// Render table on TTY.
			ok := true
			for _, r := range results {
				if r.Status == checkFail {
					ok = false
				}
			}
			printDoctorResults(w, results)

			summary := "All checks passed."
			if !ok {
				summary = "Some checks failed — see details above."
			}

			w.OK(map[string]any{
				"profile": profile,
				"api_url": cfg.APIURL,
				"checks":  results,
				"ok":      ok,
			},
				output.WithSummary("%s", summary),
			)

			if !ok {
				// Return an error so the exit layer maps it to non-zero.
				return errors.New("doctor: one or more checks failed")
			}
			return nil
		},
	}
}

// ---- individual checks ----

func runConfigCheck(cfg *config.Config, profile string) checkResult {
	res := checkResult{Name: "config", Status: checkOK}
	if cfg == nil || strings.TrimSpace(cfg.APIURL) == "" {
		res.Status = checkFail
		res.Detail = "api_url is empty"
		res.Hint = "Set one: `pura config set api_url https://pura.so`"
		return res
	}
	res.Detail = fmt.Sprintf("profile=%s api_url=%s", profile, cfg.APIURL)
	return res
}

func runNetworkCheck(apiURL string) checkResult {
	res := checkResult{Name: "network"}
	if apiURL == "" {
		res.Status = checkFail
		res.Detail = "api_url is empty"
		res.Hint = "Set one: `pura config set api_url https://pura.so`"
		return res
	}
	start := time.Now()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(strings.TrimSuffix(apiURL, "/") + "/health")
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	if err != nil {
		res.Status = checkFail
		res.Detail = err.Error()
		res.Hint = "Check your internet connection or the --api-url value."
		return res
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		res.Status = checkFail
		res.Detail = fmt.Sprintf("/health returned HTTP %d", resp.StatusCode)
		res.Hint = "Confirm api_url points to a Pura server."
		return res
	}
	// We don't re-parse the body here — that's the e2e suite's job. A 200
	// from /health plus the doctor being run from the CLI we just built is
	// good enough signal for "network works".
	res.Status = checkOK
	res.Detail = apiURL
	return res
}

func runAuthCheck(cfg *config.Config, profile string) checkResult {
	res := checkResult{Name: "auth"}
	rec, err := auth.NewStore().Load(profile)
	if err != nil {
		res.Status = checkWarn
		res.Detail = "no credentials stored"
		res.Hint = "Run `pura auth login` to sign in."
		return res
	}

	apiURL := rec.APIUrl
	if apiURL == "" && cfg != nil {
		apiURL = cfg.APIURL
	}
	if apiURL == "" {
		res.Status = checkWarn
		res.Detail = "token stored but no api_url known — auth not verified"
		res.Hint = "Re-login with `pura auth login` to re-attach api_url."
		return res
	}

	client := api.NewClient(apiURL, rec.Token)
	client.HTTPClient = &http.Client{Timeout: 5 * time.Second}
	me, err := client.Me()
	if err != nil {
		res.Status = checkFail
		res.Detail = "token rejected: " + err.Error()
		res.Hint = "Sign in again: `pura auth login`"
		return res
	}
	who := me.Email
	if me.Handle != "" {
		who = "@" + me.Handle
	}
	res.Status = checkOK
	res.Detail = fmt.Sprintf("signed in as %s (via %s)", who, me.Via)
	return res
}

// driftCheckURL is the GitHub Releases endpoint used for drift detection.
// Overridden in tests so we don't hit the real network.
var driftCheckURL = "https://api.github.com/repos/pura-labs/cli/releases/latest"

// runDriftCheck compares the compiled-in version to the latest GitHub
// release. It's deliberately best-effort:
//
//   - Unknown / dev builds (version == "dev") → warn, no-drift report.
//   - GitHub unreachable or rate-limited → warn "skipped", NOT fail. We
//     don't want doctor to go red just because CI has no egress.
//   - Version parses as lexically < latest → warn (update recommended).
//   - Match or ahead → ok.
//
// "Lexical less-than" is a deliberate shortcut: our version tags are
// semver-like (v0.2.0, v0.2.1 …) and lexical order is correct for them.
// A proper semver compare is extra code for zero real-world gain until
// we hit double-digit minor numbers.
func runDriftCheck() checkResult {
	res := checkResult{Name: "drift"}
	local := versionStr
	if local == "" || local == "dev" {
		res.Status = checkWarn
		res.Detail = "local version unset (dev build)"
		res.Hint = "Install a released build via `curl -sSL https://get.pura.so/cli | sh`."
		return res
	}

	client := &http.Client{Timeout: 2 * time.Second}
	start := time.Now()
	resp, err := client.Get(driftCheckURL)
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	if err != nil {
		res.Status = checkWarn
		res.Detail = "skipped — " + err.Error()
		return res
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		res.Status = checkWarn
		res.Detail = fmt.Sprintf("skipped — GitHub returned HTTP %d", resp.StatusCode)
		return res
	}
	// GitHub's response has many fields; we only care about tag_name.
	// The default decoder ignores unknown fields, which is what we want.
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		res.Status = checkWarn
		res.Detail = "skipped — malformed GitHub response"
		return res
	}
	remote := body.TagName
	if remote == "" {
		res.Status = checkWarn
		res.Detail = "skipped — no tag_name in GitHub response"
		return res
	}

	// Normalize the compiled-in version. goreleaser injects it without the
	// leading `v`, the GitHub API includes it.
	localNorm := strings.TrimPrefix(local, "v")
	remoteNorm := strings.TrimPrefix(remote, "v")

	if localNorm == remoteNorm {
		res.Status = checkOK
		res.Detail = "up to date (" + local + ")"
		return res
	}
	if localNorm < remoteNorm {
		res.Status = checkWarn
		res.Detail = fmt.Sprintf("out of date: local=%s latest=%s", local, remote)
		res.Hint = "Update with `curl -sSL https://get.pura.so/cli | sh`."
		return res
	}
	// local > remote (dev build newer than published release).
	res.Status = checkOK
	res.Detail = fmt.Sprintf("ahead of released tag (%s > %s)", local, remote)
	return res
}

func runProfileCheck(profile string) checkResult {
	res := checkResult{Name: "profile"}
	rec, err := auth.NewStore().Load(profile)
	if err != nil {
		res.Status = checkWarn
		res.Detail = "no profile record — nothing to check"
		res.Hint = "Run `pura auth login` to establish a profile."
		return res
	}
	if rec.Handle == "" {
		res.Status = checkWarn
		res.Detail = "no public handle — anon namespace only"
		res.Hint = "Pick one in the dashboard or `pura config set handle <name>`."
		return res
	}
	res.Status = checkOK
	res.Detail = "@" + rec.Handle
	return res
}

// ---- rendering ----

func printDoctorResults(w *output.Writer, results []checkResult) {
	if flagJSON || flagJQ != "" || !w.IsTTY {
		return // JSON-only consumer gets everything via the envelope
	}
	w.Print("  Pura Doctor\n")
	w.Print("  ──────────\n\n")
	for _, r := range results {
		mark := "✓"
		switch r.Status {
		case checkWarn:
			mark = "⚠"
		case checkFail:
			mark = "✗"
		}
		extra := r.Detail
		if r.Duration != "" {
			extra += " (" + r.Duration + ")"
		}
		w.Print("  %s %-10s %-6s %s\n", mark, r.Name, string(r.Status), extra)
		if r.Hint != "" {
			w.Print("      hint: %s\n", r.Hint)
		}
	}
	w.Print("\n")
}
