// `pura claim <edit_token>` — attach anonymously-published docs to the
// signed-in account. The server matches every doc that still carries the
// given edit_token and reassigns their user_id + handle to the caller.
//
// UX note: we intentionally take the edit_token as a positional arg rather
// than reading it from the local store. Anon push auto-saves the token into
// the profile *only as a token*, but it's also the plaintext used to edit
// the doc — we don't want a stray `pura claim` without an argument to
// silently hoover up whatever token happens to be in the active profile.
package commands

import (
	"errors"
	"fmt"

	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

func newClaimCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "claim <edit_token>",
		Short: "Attach anonymously-published docs to your account",
		Long: `Claim every document created anonymously with the given edit_token.

Typical flow:
  1. ` + "`pura push --stdin < note.md`" + ` (anon) → prints an edit_token
  2. ` + "`pura auth login`" + `                      → sign in
  3. ` + "`pura claim <edit_token>`" + `              → all anon docs show up under your handle

Prerequisites: the signed-in user must already have a public handle set.
Use the dashboard or ` + "`pura config set handle`" + ` to pick one first.`,
		Example: `  pura claim sk_pur_deadbeef01234567890abcdef0123456`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			if cfg.Token == "" {
				w.Error("unauthorized",
					"You must be signed in to claim documents",
					"Run `pura auth login` first.",
					output.WithBreadcrumb("retry", "pura auth login", "Sign in via device flow"),
				)
				return errors.New("no token")
			}
			editToken := args[0]
			if editToken == "" {
				w.Error("validation", "edit_token is required", "")
				return errors.New("edit_token required")
			}

			resp, err := newClient(cmd, cfg).Claim(editToken)
			if err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}

			if resp.Claimed == 0 {
				w.OK(resp,
					output.WithSummary("No documents matched that edit_token"),
					output.WithBreadcrumb("list", "pura ls", "See your documents"),
				)
				fmt.Fprintln(w.Err, "  No documents claimed — token did not match any anonymous docs.")
				return nil
			}

			w.OK(resp,
				output.WithSummary("Claimed %d document(s)", resp.Claimed),
				output.WithBreadcrumb("list", "pura ls", "Verify the claimed docs"),
			)
			fmt.Fprintf(w.Err, "  ✓ Claimed %d document(s).\n", resp.Claimed)
			return nil
		},
	}
}
