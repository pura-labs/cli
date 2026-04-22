// `pura mcp` (no args) — interactive picker.
//
// Three-step wizard: client → scope (if the client supports multiple) →
// transport (if the client supports multiple). Emits a summary line and
// hands off to the install flow. Stays TTY-only; scripts should call
// `pura mcp install <client>` directly.

package commands

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

func runMcpWizard(cmd *cobra.Command) error {
	w := newWriter()
	if !w.IsTTY {
		w.Error("validation",
			"Interactive wizard requires a TTY",
			"Use `pura mcp install <client>` in scripts.",
			output.WithBreadcrumb("install", "pura mcp install <client>", "Non-interactive install"),
		)
		return errors.New("non-tty wizard")
	}

	var (
		chosenClient    string
		chosenScope     string
		chosenTransport string
	)

	// Step 1: client.
	clientOpts := make([]huh.Option[string], 0, len(mcpClients))
	for _, c := range mcpClients {
		label := fmt.Sprintf("%-14s %s", c.id, c.label)
		clientOpts = append(clientOpts, huh.NewOption(label, c.id))
	}
	if err := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("Install Pura into which MCP client?").
			Description("Nine clients supported. Paths + formats auto-resolved per client.").
			Options(clientOpts...).
			Value(&chosenClient),
	)).Run(); err != nil {
		return wizardDone(err, w)
	}
	c := findClient(chosenClient)

	// Step 2: scope (only if the client supports more than one).
	if len(c.scopes) > 1 {
		scopeOpts := make([]huh.Option[string], 0, len(c.scopes))
		for _, s := range c.scopes {
			desc := "user-wide"
			if s == scopeProject {
				desc = "current project only"
			}
			scopeOpts = append(scopeOpts, huh.NewOption(fmt.Sprintf("%-8s %s", s, desc), string(s)))
		}
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Scope?").
				Options(scopeOpts...).
				Value(&chosenScope),
		)).Run(); err != nil {
			return wizardDone(err, w)
		}
	} else {
		chosenScope = string(c.scopes[0])
	}

	// Step 3: transport (only if the client supports more than one).
	if len(c.transports) > 1 {
		transOpts := make([]huh.Option[string], 0, len(c.transports)+1)
		transOpts = append(transOpts, huh.NewOption(
			fmt.Sprintf("auto    → %s (recommended)", c.defaultTransport), "auto"))
		for _, t := range c.transports {
			transOpts = append(transOpts, huh.NewOption(string(t), string(t)))
		}
		if err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Transport?").
				Description("URL avoids binary path dependency; stdio works even in offline dev.").
				Options(transOpts...).
				Value(&chosenTransport),
		)).Run(); err != nil {
			return wizardDone(err, w)
		}
	} else {
		chosenTransport = string(c.transports[0])
	}

	// Hand off to the install flow.
	mcpInstallClient = chosenClient
	mcpInstallScope = chosenScope
	mcpInstallTransport = chosenTransport
	mcpInstallYes = false // wizard always confirms via its picker; install flow prompts only on overwrite.
	return runMcpInstall(cmd, nil)
}

func wizardDone(err error, w *output.Writer) error {
	if errors.Is(err, huh.ErrUserAborted) {
		fmt.Fprintln(w.Err, "  Cancelled.")
		return nil
	}
	return err
}
