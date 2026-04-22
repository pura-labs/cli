package commands

import (
	"context"
	"os"

	"github.com/pura-labs/cli/internal/api"
	"github.com/pura-labs/cli/internal/auth"
	"github.com/pura-labs/cli/internal/config"
	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	versionStr = "dev"
	commitStr  = "none"
	dateStr    = "unknown"

	// Global flags
	flagJSON    bool
	flagQuiet   bool
	flagJQ      string
	flagAPIURL  string
	flagToken   string
	flagHandle  string
	flagProfile string
	flagVerbose bool
)

func SetVersion(version, commit, date string) {
	versionStr = version
	commitStr = commit
	dateStr = date
	// Wire Cobra's auto `--version` flag / `-V` shorthand so `pura --version`
	// stops returning "unknown flag". The dedicated `pura version` subcommand
	// still works (it prints the full version + commit + date triple) and
	// remains the canonical path in scripts; --version is the POSIX courtesy.
	rootCmd.Version = version
	rootCmd.SetVersionTemplate("pura {{.Version}}\n")
}

var rootCmd = &cobra.Command{
	Use:   "pura",
	Short: "AI-native content publishing",
	Long:  "Pura — publish, share, and access content natively. 20% features, 80% scenarios.",
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.BoolVar(&flagJSON, "json", false, "Force JSON output")
	pf.BoolVar(&flagQuiet, "quiet", false, "Raw data only (no envelope)")
	pf.StringVar(&flagJQ, "jq", "", "Filter JSON output with jq expression")
	pf.StringVar(&flagAPIURL, "api-url", "", "Override API endpoint")
	pf.StringVar(&flagToken, "token", "", "Override stored token")
	pf.StringVar(&flagHandle, "handle", "", "User handle (accepts alice or @alice; defaults to _)")
	pf.StringVar(&flagProfile, "profile", "", "Use named profile")
	pf.BoolVarP(&flagVerbose, "verbose", "v", false, "Verbose output")

	rootCmd.AddCommand(
		newPushCmd(),
		newNewCmd(),
		newGetCmd(),
		newEditCmd(),
		newRmCmd(),
		newListCmd(),
		newConfigCmd(),
		newDoctorCmd(),
		newVersionCmd(),
		newCompletionCmd(),
		newSkillCmd(),
		newOpenCmd(),
		newPresentCmd(),
		newPreviewCmd(),
		newAuthCmd(),
		newKeysCmd(),
		newVersionsCmd(),
		newClaimCmd(),
		newStatsCmd(),
		newEventsCmd(),
		newChatCmd(),
		newMcpCmd(),
		newToolCmd(),
		newSheetCmd(),
		newBookCmd(),
		newSurfaceCmd(),
	)
}

// Execute runs the root command with a background context. Retained for
// tests and any caller that does not need cancellation.
func Execute() error {
	return rootCmd.Execute()
}

// ExecuteContext runs the root command with the given context, so cobra
// hands each RunE a cmd whose Context() is signal-bound — network calls
// made via newClient() pick that up and cancel on Ctrl-C.
func ExecuteContext(ctx context.Context) error {
	return rootCmd.ExecuteContext(ctx)
}

// loadConfig resolves the layered config.
func loadConfig() *config.Config {
	cfg := config.Load(flagAPIURL, flagToken, flagProfile)
	if tokenOverridden() {
		return cfg
	}

	// Profile-scoped credentials fill in auth metadata when the caller hasn't
	// explicitly overridden it via flags or env.
	if rec, err := auth.NewStore().Load(resolvedProfile(cfg)); err == nil {
		if !cfg.HasExplicitAPIURL() && rec.APIUrl != "" {
			cfg.APIURL = rec.APIUrl
		}
		cfg.Token = rec.Token
		if cfg.Handle == "" && rec.Handle != "" {
			cfg.Handle = rec.Handle
		}
	}

	return cfg
}

// newClient creates an API client from resolved config. Callers pass the
// *cobra.Command so we can thread its Context() onto the client — that's
// how Ctrl-C cancellation reaches in-flight HTTP requests. Tests that
// don't have a cmd can pass nil to get a Background-bound client.
func newClient(cmd *cobra.Command, cfg *config.Config) *api.Client {
	client := api.NewClient(cfg.APIURL, cfg.Token)
	client.Verbose = flagVerbose
	if cmd != nil {
		client.Ctx = cmd.Context()
	}
	// Resolve handle: flag > config > default "_".
	h := flagHandle
	if h == "" {
		h = cfg.Handle
	}
	client.Handle = api.NormalizeHandle(h)
	return client
}

// newWriter creates an output writer based on flags.
func newWriter() *output.Writer {
	format := output.FormatAuto
	if flagJSON || flagJQ != "" {
		format = output.FormatJSON
	} else if flagQuiet {
		format = output.FormatQuiet
	}
	w := output.NewWriter(format)
	if flagJQ != "" {
		w.JQFilter = flagJQ
	}
	return w
}

func tokenOverridden() bool {
	return flagToken != "" || os.Getenv("PURA_TOKEN") != ""
}

func resolvedProfile(cfg *config.Config) string {
	if cfg != nil && cfg.Profile != "" {
		return cfg.Profile
	}
	return "default"
}
