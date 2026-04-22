package commands

import (
	"fmt"

	"github.com/pura-labs/cli/internal/auth"
	"github.com/pura-labs/cli/internal/config"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
		Long:  "Get, set, or list configuration values.",
	}

	cmd.AddCommand(
		newConfigSetCmd(),
		newConfigGetCmd(),
		newConfigListCmd(),
	)

	return cmd
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a config value",
		Example: `  pura config set token sk_pur_xxxx
  pura config set handle @alice
  pura config set api_url https://pura.so
  pura config set theme paper`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			key, value := args[0], args[1]

			if key == "token" {
				profile := resolvedProfile(loadConfig())
				// SetToken merges with any existing handle / api_url / user_id
				// so `pura config set token …` doesn't nuke the rest of the record.
				if err := auth.NewStore().SetToken(profile, value); err != nil {
					w.Error("config_error", "Failed to save token", err.Error())
					return err
				}

				w.OK(map[string]string{"profile": profile, "status": "saved"})
				w.Print("  Saved token for profile %s\n", profile)
				return nil
			}

			if err := config.Set(key, value); err != nil {
				w.Error("config_error", fmt.Sprintf("Invalid key: %s", key), "Valid keys: api_url, token, handle, profile, theme")
				return err
			}

			w.OK(map[string]string{key: value})
			w.Print("  Set %s = %s\n", key, value)
			return nil
		},
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <key>",
		Short:   "Get a config value",
		Example: `  pura config get handle`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			key := args[0]

			value, ok := config.Get(cfg, key)
			if !ok {
				w.Error("config_error", fmt.Sprintf("Unknown key: %s", key), "Valid keys: api_url, token, handle, profile, theme")
				return fmt.Errorf("unknown key: %s", key)
			}

			w.OK(map[string]string{key: value})
			w.Print("  %s = %s\n", key, value)
			return nil
		},
	}
}

func newConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all config values",
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			all := config.All(cfg)

			w.OK(all)
			for k, v := range all {
				if v == "" {
					v = "(not set)"
				}
				w.Print("  %-10s %s\n", k, v)
			}
			return nil
		},
	}
}
