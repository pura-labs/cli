// `pura auth ...` — credential management.
//
// Subcommands:
//
//	login     — OAuth 2.0 Device Authorization Grant (RFC 8628).
//	            Opens a browser to /login/device, polls until approved,
//	            stores the resulting long-lived API key in
//	            ~/.config/pura/credentials.json (mode 0600).
//	login --token <t>  — bypass the device flow for CI: stash a key directly.
//	logout    — wipe credentials for the active profile.
//	status    — show the active profile's stored state (optionally verify
//	            with the server via GET /api/auth/me).
//	token     — print the stored token to stdout. Interactive guard; pipe
//	            users can pass --yes.
package commands

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/pura-labs/cli/internal/api"
	"github.com/pura-labs/cli/internal/auth"
	"github.com/pura-labs/cli/internal/output"
)

// clientName returns the label we attach to keys minted by this CLI. Used by
// the user to recognize "which machine" each key came from in the dashboard.
func clientName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return "cli:" + host
}

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage credentials",
		Long:  "Sign in, sign out, inspect or rotate the token used by pura CLI.",
	}
	cmd.AddCommand(
		newAuthLoginCmd(),
		newAuthLogoutCmd(),
		newAuthStatusCmd(),
		newAuthTokenCmd(),
		newAuthRefreshCmd(),
	)
	return cmd
}

// ---------- refresh ----------

// newAuthRefreshCmd rotates the currently-stored token.
//
// Flow (best-effort, NOT atomic across the server boundary):
//  1. Read record → get KeyID, (re-)fetch name+scopes from /api/auth/keys
//     so a rotate inherits whatever was last configured.
//  2. POST /api/auth/keys → mint new token.
//  3. Save new token to the profile (credentials.json).
//  4. DELETE /api/auth/keys/<old> → revoke the old key.
//
// Failure handling — each step is best-effort; the server has no "rotate
// atomically" primitive so we expose the seam honestly:
//   - Step 2 fails → abort, old token still works.
//   - Step 3 fails → try to revoke the just-minted token so we don't leak;
//     if that fails too, surface both errors.
//   - Step 4 fails → warn but keep new token. The old key stays valid on
//     the server; the user is told to clean it up with `pura keys rm <old>`.
//     This is a security-hygiene regression (two live keys instead of one)
//     but preferable to corrupting the credentials file.
func newAuthRefreshCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Rotate the stored token",
		Long: "Mint a new API key with the same name and scopes as the current one,\n" +
			"save it, then revoke the old key. Use this after a suspected leak or as\n" +
			"routine hygiene.\n" +
			"\n" +
			"Atomicity: best-effort. The new key is created first. If the save-to-disk\n" +
			"step fails we try to revoke the just-minted key; if the old-key revoke at\n" +
			"the end fails we keep the new token and report what to clean up. In the\n" +
			"worst case you end up with two live keys until you run `pura keys rm`.",
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			profile := resolvedProfile(cfg)

			rec, err := auth.NewStore().Load(profile)
			if err != nil {
				w.Error("not_signed_in",
					"No credentials for this profile",
					"Run `pura auth login` first.",
					output.WithBreadcrumb("retry", "pura auth login", "Sign in"),
				)
				return err
			}
			if rec.KeyID == "" {
				w.Error("validation",
					"This profile has no key_id recorded — cannot auto-rotate",
					"Sign in again with `pura auth login` so the CLI remembers which key to rotate.",
					output.WithBreadcrumb("retry", "pura auth login", "Sign in"),
				)
				return errors.New("no key_id")
			}

			client := api.NewClient(cfg.APIURL, rec.Token)
			client.Verbose = flagVerbose
			client.Ctx = cmd.Context()

			// Recover the old key's name + scopes so the new one inherits.
			keys, err := client.ListKeys()
			if err != nil {
				w.Error("api_error", err.Error(), "Check `pura auth status --verify`.")
				return err
			}
			var old *api.ApiKeyListItem
			for i := range keys {
				if keys[i].ID == rec.KeyID {
					old = &keys[i]
					break
				}
			}
			if old == nil {
				w.Error("not_found",
					"Current key_id is not in your active key list — it may already be revoked.",
					"Sign in again with `pura auth login`.",
					output.WithBreadcrumb("retry", "pura auth login", "Sign in"),
				)
				return errors.New("key missing server-side")
			}

			if !yes {
				displayName := old.Name
				if displayName == "" {
					displayName = old.Prefix
				}
				fmt.Fprintf(w.Err, "  Rotating key %s (prefix %s) …\n", displayName, old.Prefix)
			}

			// 1) Mint the replacement. Append " (rotated)" so humans can tell
			//    new from old in the dashboard until the old one is gone.
			newResp, err := client.CreateKey(api.CreateKeyRequest{
				Name:   deriveRotatedName(old.Name),
				Scopes: old.Scopes,
			})
			if err != nil {
				w.Error("api_error", "Could not mint replacement key", err.Error())
				return err
			}

			// 2) Save before we try to revoke the old one — losing the new
			//    token to a disk error is worse than leaving the old one alive.
			newRec := rec
			newRec.Token = newResp.Token
			newRec.KeyID = newResp.ID
			newRec.KeyPrefix = newResp.Prefix
			if err := auth.NewStore().Save(profile, newRec); err != nil {
				// Best-effort: revoke the just-minted key so we don't leak it.
				_ = client.RevokeKey(newResp.ID)
				w.Error("save_failed",
					"Minted a new token but failed to save — rolled back",
					err.Error(),
				)
				return err
			}

			// 3) Revoke the old key using the NEW token (it has the same
			//    scopes, and the old one may already be scoped-out of future
			//    calls depending on server policy).
			client.Token = newResp.Token
			if err := client.RevokeKey(old.ID); err != nil {
				fmt.Fprintf(w.Err,
					"  ⚠ new token saved, but could not revoke old key %s: %v\n"+
						"     Run `pura keys rm %s --yes` to clean up.\n",
					old.ID, err, old.ID,
				)
			}

			w.OK(map[string]any{
				"profile": profile,
				"old_key": old.ID,
				"new_key": newResp.ID,
				"prefix":  newResp.Prefix,
				"scopes":  newResp.Scopes,
			},
				output.WithSummary("Rotated %s → %s", old.Prefix, newResp.Prefix),
				output.WithBreadcrumb("verify", "pura auth status --verify", "Confirm the new token works"),
				output.WithBreadcrumb("list", "pura keys ls", "Confirm the old key is gone"),
			)
			fmt.Fprintf(w.Err, "  ✓ Rotated (new prefix %s, profile=%s)\n", newResp.Prefix, profile)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the pre-rotation confirmation log line")
	return cmd
}

// deriveRotatedName tags a key name so successive rotations don't all
// collide on "cli:host". We strip any prior " (rotated N)" suffix and
// append a fresh counter — keeps the set of key names in the dashboard
// readable over time.
func deriveRotatedName(oldName string) string {
	if oldName == "" {
		return "cli (rotated)"
	}
	// Drop a trailing "(rotated N)" / "(rotated)" so names don't grow.
	trimmed := oldName
	for {
		idx := strings.LastIndex(trimmed, " (rotated")
		if idx < 0 {
			break
		}
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	return trimmed + " (rotated)"
}

// ---------- login ----------

// Auth subcommand flag state lives at package scope so tests can reset it
// between cobra.Execute() invocations (cobra keeps flag values sticky on a
// re-used *Command tree).
var (
	authLoginToken     string
	authLoginNoBrowser bool
	authLoginScopes    []string
	authLoginTimeout   int
	authStatusVerify   bool
	authTokenYes       bool
)

// resetAuthFlags clears every auth-subcommand flag to its zero value.
// Called by resetCommandGlobals() in tests.
func resetAuthFlags() {
	authLoginToken = ""
	authLoginNoBrowser = false
	authLoginScopes = nil
	authLoginTimeout = 600
	authStatusVerify = false
	authTokenYes = false
}

func newAuthLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in via device flow",
		Long: `Sign in to Pura.

Interactive (default):
  Opens your browser to authorize a long-lived API key via the OAuth 2.0
  Device Authorization Grant. The CLI polls until you approve, then stores
  the resulting token in ~/.config/pura/credentials.json (mode 0600).

Non-interactive (CI / agent):
  Pass --token to skip the flow and save a pre-issued key directly. Pair
  with --profile to keep multiple identities separated.`,
		Example: `  pura auth login
  pura auth login --no-browser
  pura auth login --token sk_pura_xxxxxxxx --profile ci`,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			profile := resolvedProfile(cfg)

			// Non-interactive path: user supplied the token.
			if authLoginToken != "" {
				return saveTokenOnly(cmd, w, profile, cfg.APIURL, authLoginToken)
			}

			client := api.NewClient(cfg.APIURL, "")
			client.Verbose = flagVerbose
			client.Ctx = cmd.Context()

			start, err := client.DeviceStart(api.DeviceStartRequest{
				ClientName: clientName(),
				Scopes:     authLoginScopes,
			})
			if err != nil {
				w.Error("device_start_failed", "Could not start device flow", err.Error())
				return err
			}

			// User-facing output lives on stderr so stdout stays clean for the
			// final envelope. This matters for pipes (`pura auth login | jq`).
			fmt.Fprintf(w.Err, "\n  Visit: %s\n", start.VerificationURL)
			fmt.Fprintf(w.Err, "  Code:  %s\n\n", start.UserCode)
			fmt.Fprintf(w.Err, "  Or scan: %s\n\n", start.VerificationURLComplete)

			// Skip the browser launch when we can see the environment won't
			// have one — SSH sessions without X-forwarding, CI runners,
			// containers with no display server. Avoids confusing errors
			// like "xdg-open: not found" leaking to the user's stderr when
			// the right answer was always "visit the URL".
			noBrowser := authLoginNoBrowser || isHeadlessEnv()
			if !noBrowser {
				if err := launchBrowser(start.VerificationURLComplete); err != nil {
					// Browser failure is non-fatal — user can still visit manually.
					fmt.Fprintf(w.Err, "  (could not open browser: %v — visit the URL above)\n\n", err)
				}
			} else if !authLoginNoBrowser {
				// User didn't explicitly pass --no-browser, so tell them why
				// we skipped the launch and what they can do.
				fmt.Fprintf(w.Err, "  (headless environment detected — visit the URL above to approve)\n\n")
			}

			approved, err := pollUntilResolved(cmd, client, start, authLoginTimeout)
			if err != nil {
				w.Error("device_poll_failed", "Sign-in did not complete", err.Error())
				return err
			}

			rec := auth.Record{
				Token:     approved.Token,
				Handle:    approved.User.Handle,
				APIUrl:    cfg.APIURL,
				UserID:    approved.User.ID,
				KeyID:     approved.KeyID,
				KeyPrefix: approved.TokenPrefix,
			}
			if err := auth.NewStore().Save(profile, rec); err != nil {
				w.Error("save_failed", "Could not persist token", err.Error())
				return err
			}

			email := approved.User.Email
			handle := approved.User.Handle
			signedInAs := email
			if handle != "" {
				signedInAs = "@" + handle
			}

			w.OK(map[string]any{
				"profile": profile,
				"handle":  handle,
				"user_id": approved.User.ID,
				"key_id":  approved.KeyID,
				"prefix":  approved.TokenPrefix,
				"scopes":  approved.Scopes,
			},
				output.WithSummary("Signed in as %s (profile=%s)", signedInAs, profile),
				output.WithBreadcrumb("list", "pura ls", "List your documents"),
				output.WithBreadcrumb("publish", "pura push <file>", "Publish a new document"),
				output.WithBreadcrumb("status", "pura auth status --verify", "Verify the token with the server"),
			)
			fmt.Fprintf(w.Err, "  ✓ Signed in as %s (profile=%s, key=%s)\n", signedInAs, profile, approved.TokenPrefix)
			return nil
		},
	}

	cmd.Flags().StringVar(&authLoginToken, "token", "", "Store this token directly, skipping the browser flow (CI-friendly)")
	cmd.Flags().BoolVar(&authLoginNoBrowser, "no-browser", false, "Print the URL but do not attempt to open a browser")
	cmd.Flags().StringSliceVar(&authLoginScopes, "scope", nil, "Scopes to request (repeatable). Default: docs:read,docs:write")
	cmd.Flags().IntVar(&authLoginTimeout, "timeout", 600, "Maximum seconds to wait for authorization")

	return cmd
}

// saveTokenOnly is the --token bypass: verify against /me, then store.
func saveTokenOnly(cmd *cobra.Command, w *output.Writer, profile, apiURL, token string) error {
	client := api.NewClient(apiURL, token)
	if cmd != nil {
		client.Ctx = cmd.Context()
	}
	me, err := client.Me()
	if err != nil {
		w.Error("verify_failed", "Token was rejected by the server", err.Error())
		return err
	}
	rec := auth.Record{
		Token:  token,
		Handle: me.Handle,
		APIUrl: apiURL,
		UserID: me.ID,
	}
	if err := auth.NewStore().Save(profile, rec); err != nil {
		w.Error("save_failed", "Could not persist token", err.Error())
		return err
	}
	displayName := me.Email
	if me.Handle != "" {
		displayName = "@" + me.Handle
	}
	w.OK(map[string]any{
		"profile": profile,
		"handle":  me.Handle,
		"user_id": me.ID,
		"via":     "token",
	},
		output.WithSummary("Stored token for %s (profile=%s)", displayName, profile),
		output.WithBreadcrumb("list", "pura ls", "List your documents"),
	)
	fmt.Fprintf(w.Err, "  ✓ Stored token for %s (profile=%s)\n", displayName, profile)
	return nil
}

// pollUntilResolved drives the RFC 8628 polling loop.
func pollUntilResolved(cmd *cobra.Command, client *api.Client, start *api.DeviceStartResponse, timeoutSec int) (*api.DevicePollApproved, error) {
	interval := time.Duration(start.Interval) * time.Second
	if interval < time.Second {
		interval = 2 * time.Second
	}
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)

	// Graceful Ctrl-C: return a friendly error instead of stack trace.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// Poll once immediately — there's a small chance the user was fast.
	for {
		select {
		case <-sigCh:
			return nil, errors.New("canceled")
		default:
		}

		res, err := client.DevicePoll(start.DeviceCode)
		if err != nil {
			// Transient network error — don't abort the whole flow.
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("timed out after %ds: %w", timeoutSec, err)
			}
			select {
			case <-ticker.C:
			case <-sigCh:
				return nil, errors.New("canceled")
			}
			continue
		}
		if res.Approved != nil {
			return res.Approved, nil
		}
		code := ""
		if res.Error != nil {
			code = res.Error.Code
		}
		switch code {
		case "authorization_pending":
			// Normal — keep polling.
		case "slow_down":
			// Server asked for more breathing room. Back off.
			if res.Error != nil && res.Error.RetryAfter > 0 {
				interval = time.Duration(res.Error.RetryAfter) * time.Second
			} else {
				interval += 2 * time.Second
			}
			ticker.Reset(interval)
		case "expired":
			return nil, errors.New("device code expired — run `pura auth login` again")
		case "access_denied":
			return nil, errors.New("authorization was denied")
		default:
			if res.Error != nil {
				return nil, fmt.Errorf("%s: %s", res.Error.Code, res.Error.Message)
			}
			return nil, fmt.Errorf("unexpected poll response (status %d)", res.Status)
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out after %ds", timeoutSec)
		}
		select {
		case <-ticker.C:
		case <-sigCh:
			return nil, errors.New("canceled")
		}
	}
}

// launchBrowser best-effort launches the user's default browser. Works on
// mac/linux/windows. Failures are caller-tolerated — we always print the URL.
func launchBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default: // linux, bsd, etc.
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// isHeadlessEnv returns true when we're almost certainly running without a
// display server — SSH into a headless box, a CI runner, a container, or a
// Docker build. Not perfect (Wayland+Xwayland, macOS ssh forwarding, WSL)
// but catches the common cases where launching a browser just errors out.
//
// Detection is platform-specific:
//   - Linux/BSD: no DISPLAY *and* no WAYLAND_DISPLAY means no GUI session.
//   - macOS/Windows: there's always a window server for interactive users;
//     we assume non-headless unless CI=true is set.
func isHeadlessEnv() bool {
	if os.Getenv("CI") != "" {
		return true
	}
	switch runtime.GOOS {
	case "darwin", "windows":
		return false
	default:
		return os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == ""
	}
}

// ---------- logout ----------

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Sign out — wipe stored credentials for this profile",
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			profile := resolvedProfile(loadConfig())
			store := auth.NewStore()
			rec, loadErr := store.Load(profile)
			if err := store.Delete(profile); err != nil {
				w.Error("logout_failed", "Could not delete credentials", err.Error())
				return err
			}
			msg := "Signed out"
			data := map[string]any{"profile": profile, "status": "signed_out"}
			if loadErr == nil && rec.KeyPrefix != "" {
				data["prefix"] = rec.KeyPrefix
				msg = fmt.Sprintf("Signed out (key %s)", rec.KeyPrefix)
			}
			w.OK(data)
			fmt.Fprintf(w.Err, "  %s · profile=%s\n", msg, profile)
			return nil
		},
	}
}

// ---------- status ----------

func newAuthStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show current auth state",
		Long: `Report the profile, stored token prefix, and (optionally) verify the
token with the server.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			profile := resolvedProfile(cfg)
			rec, err := auth.NewStore().Load(profile)
			if err != nil {
				w.Error("not_signed_in",
					"No credentials for this profile",
					"Run `pura auth login` to sign in.",
					output.WithBreadcrumb("retry", "pura auth login", "Sign in via device flow"),
				)
				return err
			}
			data := map[string]any{
				"profile":    profile,
				"api_url":    cfg.APIURL,
				"handle":     rec.Handle,
				"user_id":    rec.UserID,
				"key_id":     rec.KeyID,
				"key_prefix": rec.KeyPrefix,
				"verified":   false,
			}

			if authStatusVerify {
				client := api.NewClient(cfg.APIURL, rec.Token)
				client.Ctx = cmd.Context()
				me, merr := client.Me()
				if merr != nil {
					w.Error("verify_failed",
						"Stored token was rejected",
						merr.Error(),
						output.WithBreadcrumb("retry", "pura auth login", "Re-sign-in"),
					)
					return merr
				}
				data["verified"] = true
				data["handle"] = me.Handle
				data["user_id"] = me.ID
				data["email"] = me.Email
				data["via"] = me.Via
			}

			w.OK(data,
				output.WithSummary("Signed in (profile=%s)", profile),
				output.WithBreadcrumb("list", "pura ls", "List your documents"),
				output.WithBreadcrumb("verify", "pura auth status --verify", "Ping the server to confirm token is still valid"),
			)
			suffix := rec.KeyPrefix
			if suffix == "" && rec.Token != "" {
				if len(rec.Token) >= 8 {
					suffix = rec.Token[:8] + "…"
				} else {
					suffix = "********"
				}
			}
			fmt.Fprintf(w.Err, "  profile:  %s\n", profile)
			fmt.Fprintf(w.Err, "  api_url:  %s\n", cfg.APIURL)
			if rec.Handle != "" {
				fmt.Fprintf(w.Err, "  handle:   @%s\n", rec.Handle)
			}
			fmt.Fprintf(w.Err, "  token:    %s\n", suffix)
			if authStatusVerify {
				fmt.Fprintf(w.Err, "  verified: yes\n")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&authStatusVerify, "verify", false, "Call the server to confirm the token is still valid")
	return cmd
}

// ---------- token ----------

func newAuthTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Print the stored token (use with care)",
		Long: `Print the current profile's token to stdout.

Guards against accidental disclosure: when stdout is a TTY, the command
refuses unless --yes is passed. Non-interactive pipes are allowed by default
because scripts are the typical consumer (e.g. export PURA_TOKEN=$(pura auth token)).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			profile := resolvedProfile(loadConfig())
			rec, err := auth.NewStore().Load(profile)
			if err != nil {
				w.Error("not_signed_in", "No credentials for this profile", "Run `pura auth login` to sign in.")
				return err
			}
			if w.IsTTY && !authTokenYes {
				w.Error("confirmation_required",
					"Refusing to print token to a terminal",
					"Pass --yes to confirm, or redirect stdout to a file.")
				return errors.New("confirmation required")
			}
			// Plain stdout on purpose — callers pipe this directly.
			fmt.Fprintln(w.Out, strings.TrimSpace(rec.Token))
			return nil
		},
	}
	cmd.Flags().BoolVar(&authTokenYes, "yes", false, "Confirm printing to a terminal")
	return cmd
}
