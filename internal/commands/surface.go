// `pura _surface` — hidden command that walks the cobra tree and prints
// a stable, greppable summary of every user-visible command + flag.
//
// Committed to cli/SURFACE.txt so diffs surface unintentional UX drift.
// The `make surface` target regenerates; review in a PR to see whether
// a renamed flag or added subcommand was intentional.

package commands

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newSurfaceCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "_surface",
		Short:  "Print the stable command + flag surface (for SURFACE.txt)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			walkSurface(cmd.OutOrStdout(), rootCmd, "")
			return nil
		},
	}
}

func walkSurface(w io.Writer, c *cobra.Command, prefix string) {
	if c.Hidden || c.Name() == "help" {
		return
	}

	name := c.Name()
	if name == "pura" {
		name = ""
	}
	path := strings.TrimSpace(prefix + " " + name)
	if path != "" {
		line := "pura " + path
		if c.Short != "" {
			line += "  — " + c.Short
		}
		fmt.Fprintln(w, line)

		var flags []string
		c.Flags().VisitAll(func(f *pflag.Flag) {
			flags = append(flags, renderSurfaceFlag(f))
		})
		sort.Strings(flags)
		for _, f := range flags {
			fmt.Fprintln(w, "    "+f)
		}
	}

	// Stable order — prevents diff churn that isn't real.
	subs := append([]*cobra.Command{}, c.Commands()...)
	sort.Slice(subs, func(i, j int) bool { return subs[i].Name() < subs[j].Name() })
	for _, sub := range subs {
		walkSurface(w, sub, path)
	}
}

func renderSurfaceFlag(f *pflag.Flag) string {
	out := "--" + f.Name
	if f.Shorthand != "" {
		out = "-" + f.Shorthand + "/" + out
	}
	if f.Value.Type() != "bool" {
		out += " <" + f.Value.Type() + ">"
	}
	if f.Usage != "" {
		out += "  " + f.Usage
	}
	return out
}
