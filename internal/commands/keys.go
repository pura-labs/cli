// `pura keys {ls, create, rm}` — manage API keys bound to the signed-in user.
//
// `create` is the only command that shows the plaintext token — once. We warn
// loudly and emit a breadcrumb that the user must save it immediately.

package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pura-labs/cli/internal/api"
	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	keysCreateName   string
	keysCreateScopes []string
	keysRmYes        bool
)

func resetKeysFlags() {
	keysCreateName = ""
	keysCreateScopes = nil
	keysRmYes = false
}

func newKeysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keys",
		Short: "Manage API keys",
		Long: `List, create, or revoke the API keys bound to your account.

API keys are bearer tokens of the form sk_pura_…. Each carries scopes
(docs:read, docs:write, comments:read, comments:write, subscriptions:manage).
Typical use:
  - CI / agent gets its own key with the minimum scopes it needs.
  - Revoke compromised keys with ` + "`pura keys rm`" + `.`,
	}
	cmd.AddCommand(newKeysLsCmd(), newKeysCreateCmd(), newKeysRmCmd())
	return cmd
}

// ---------- ls ----------

func newKeysLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List API keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			if cfg.Token == "" {
				w.Error("unauthorized", "No token configured",
					"Run `pura auth login` first.",
					output.WithBreadcrumb("retry", "pura auth login", "Sign in"),
				)
				return errors.New("no token")
			}
			client := newClient(cmd, cfg)
			items, err := client.ListKeys()
			if err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}

			if len(items) == 0 {
				w.OK(items,
					output.WithSummary("No API keys yet"),
					output.WithBreadcrumb("create", `pura keys create --name "<label>"`, "Mint a new key"),
				)
				w.Print("  No API keys.\n")
				return nil
			}

			w.OK(items,
				output.WithSummary("%d key(s)", len(items)),
				output.WithBreadcrumb("create", `pura keys create --name "<label>"`, "Mint another key"),
				output.WithBreadcrumb("revoke", "pura keys rm <id|prefix>", "Revoke a key"),
			)

			w.Print("  %-20s %-20s %-28s %s\n", "ID", "PREFIX", "NAME", "CREATED")
			w.Print("  %-20s %-20s %-28s %s\n", "──", "──────", "────", "───────")
			for _, it := range items {
				name := it.Name
				if len(name) > 26 {
					name = name[:26] + "…"
				}
				created := it.CreatedAt
				if len(created) >= 10 {
					created = created[:10]
				}
				w.Print("  %-20s %-20s %-28s %s\n", it.ID, it.Prefix, name, created)
			}
			return nil
		},
	}
}

// ---------- create ----------

func newKeysCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new API key",
		Long: `Mint a new API key. The plaintext token is returned exactly once —
copy it into your secret store immediately. It cannot be retrieved later.`,
		Example: `  pura keys create --name "ci:github-actions"
  pura keys create --name "bot" --scope docs:read --scope docs:write`,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			if cfg.Token == "" {
				w.Error("unauthorized", "No token configured",
					"Run `pura auth login` first.",
					output.WithBreadcrumb("retry", "pura auth login", "Sign in"),
				)
				return errors.New("no token")
			}
			if keysCreateName == "" {
				w.Error("validation", "--name is required",
					"Pass a human-readable label, e.g. --name \"ci:github-actions\".")
				return errors.New("name required")
			}
			scopes := keysCreateScopes
			if len(scopes) == 0 {
				// Sane default for the most common use-case: read + write docs.
				scopes = []string{"docs:read", "docs:write"}
			}

			client := newClient(cmd, cfg)
			resp, err := client.CreateKey(api.CreateKeyRequest{
				Name:   keysCreateName,
				Scopes: scopes,
			})
			if err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}

			w.OK(resp,
				output.WithSummary("Created %s (%s)", resp.Prefix, strings.Join(resp.Scopes, ",")),
				output.WithBreadcrumb("list", "pura keys ls", "Confirm the new key appears"),
				output.WithBreadcrumb("revoke", "pura keys rm "+resp.ID, "Roll back this key"),
			)
			// Stderr warning so humans see it even when stdout is piped to jq.
			fmt.Fprintln(w.Err, "")
			fmt.Fprintln(w.Err, "  ⚠  The token below is shown exactly once — save it now.")
			fmt.Fprintln(w.Err, "")
			// The token itself goes to stdout in styled mode so `| pbcopy` works.
			if !w.IsTTY || flagJSON {
				// In JSON mode the token is already in the envelope; don't
				// duplicate it to a plain line that would corrupt the output.
				return nil
			}
			fmt.Fprintln(w.Out, resp.Token)
			return nil
		},
	}
	cmd.Flags().StringVarP(&keysCreateName, "name", "n", "", "Human-readable label for the key")
	cmd.Flags().StringSliceVar(&keysCreateScopes, "scope", nil, "Scope to grant (repeatable). Default: docs:read,docs:write")
	return cmd
}

// ---------- rm ----------

func newKeysRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rm <id|prefix>",
		Short: "Revoke an API key",
		Long: `Soft-delete an API key. Accepts either the key's id (key_…) or its
prefix (sk_pura_XXXXXXXX); prefixes are resolved against the current
user's key list.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			if cfg.Token == "" {
				w.Error("unauthorized", "No token configured", "Run `pura auth login` first.")
				return errors.New("no token")
			}
			client := newClient(cmd, cfg)

			id, err := resolveKeyTarget(client, args[0])
			if err != nil {
				w.Error("not_found", err.Error(), "Use `pura keys ls` to find the id.")
				return err
			}

			ok, err := confirmMutation(
				w,
				keysRmYes,
				"--yes",
				fmt.Sprintf("Revoke %s?", id),
				"This cannot be undone. Scripts using this key will start failing.",
				"Revoke",
			)
			if err != nil {
				w.Error("confirmation_required",
					"Refusing to revoke a key without confirmation",
					"Re-run with --yes in non-interactive mode.",
				)
				return err
			}
			if !ok {
				w.Print("  Cancelled.\n")
				return nil
			}

			if err := client.RevokeKey(id); err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}

			w.OK(map[string]string{"id": id, "status": "revoked"},
				output.WithSummary("Revoked %s", id),
				output.WithBreadcrumb("list", "pura keys ls", "Confirm removal"),
			)
			w.Print("  Revoked %s\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVar(&keysRmYes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

// resolveKeyTarget accepts either an id (starts with "key_") or a token
// prefix ("sk_pura_XXXXXXXX"). Prefix lookup makes it convenient to paste
// the partial string shown in `pura keys ls`.
func resolveKeyTarget(client *api.Client, target string) (string, error) {
	if strings.HasPrefix(target, "key_") {
		return target, nil
	}
	items, err := client.ListKeys()
	if err != nil {
		return "", err
	}
	for _, it := range items {
		if it.Prefix == target || it.ID == target {
			return it.ID, nil
		}
	}
	return "", fmt.Errorf("no key matches %q", target)
}
