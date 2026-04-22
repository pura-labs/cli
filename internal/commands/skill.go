// `pura skill` — install the bundled SKILL.md into agent-ecosystem
// locations so Claude Code / OpenCode / Codex / bespoke agents can
// discover it.
//
// Design:
//   - `pura skill` with no args opens an interactive wizard (huh) that
//     lets the user pick ONE known-good target directory.
//   - `pura skill install` is the non-interactive form, scripts-friendly.
//   - `pura skill ls` shows every install (scanned across every known
//     target so users can find stale copies).
//   - `pura skill run <name>` prints a SKILL.md (for piping to `pbcopy`
//     or to let an LLM ingest it on the fly).
//   - `pura skill rm <name>` removes the on-disk copy — builtins always
//     remain recoverable since they live in the binary.

package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/pura-labs/cli/internal/output"
	"github.com/pura-labs/cli/internal/skill"
	"github.com/spf13/cobra"
)

// Known agent-ecosystem locations, in the order we present them in the
// wizard. Each has a short label for the menu plus the actual path
// template; we expand `~` and `./` at use-time.
type skillTarget struct {
	Label string
	Dir   string
	Help  string
}

func knownTargets() []skillTarget {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	return []skillTarget{
		{"Claude Code (global)", filepath.Join(home, ".claude", "skills"), "Visible to every Claude Code session for this user."},
		{"Claude Code (project)", filepath.Join(cwd, ".claude", "skills"), "Only Claude Code sessions in this directory."},
		{"Agents Shared", filepath.Join(home, ".agents", "skills"), "Generic cross-agent location; many tools scan it."},
		{"OpenCode", filepath.Join(home, ".config", "opencode", "skills"), "OpenCode's skill directory."},
		{"Pura CLI (private)", filepath.Join(home, ".config", "pura", "skills"), "Not seen by other agents; useful as a staging copy."},
	}
}

// skill subcommand flag state.
var (
	skillInstallTarget string
	skillInstallSource string
	skillRmAllTargets  bool
)

func resetSkillFlags() {
	skillInstallTarget = ""
	skillInstallSource = ""
	skillRmAllTargets = false
}

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Install the Pura agent skill (SKILL.md) into a known location",
		Long: `Install the bundled SKILL.md into one of several known locations so
Claude Code, OpenCode, or any generic AI agent can discover it.

Run ` + "`pura skill`" + ` (no args) for an interactive picker, or use the
subcommands for scripting.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Top-level run = interactive wizard.
			return runSkillWizard(cmd)
		},
	}
	cmd.AddCommand(
		newSkillLsCmd(),
		newSkillInstallCmd(),
		newSkillRunCmd(),
		newSkillRmCmd(),
	)
	return cmd
}

// ---------- wizard ----------

func runSkillWizard(cmd *cobra.Command) error {
	w := newWriter()
	if !w.IsTTY {
		w.Error("validation",
			"Interactive wizard requires a TTY",
			"Use `pura skill install --target <path>` in scripts.",
			output.WithBreadcrumb("install", "pura skill install --target ~/.claude/skills", "Non-interactive install"),
		)
		return errors.New("non-tty wizard")
	}

	targets := knownTargets()
	options := make([]huh.Option[string], 0, len(targets))
	for _, t := range targets {
		// huh renders label + description via .Option — keep text pithy.
		options = append(options, huh.NewOption(fmt.Sprintf("%-24s %s", t.Label, t.Dir), t.Dir))
	}

	var chosen string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Install the Pura skill where?").
				Description("Each target is scanned by a different class of agent runtime.").
				Options(options...).
				Value(&chosen),
		),
	)
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(w.Err, "  Cancelled.")
			return nil
		}
		return err
	}
	mgr := skill.NewManagerWithDir(chosen)
	if err := mgr.InstallBuiltin("pura"); err != nil {
		w.Error("skill_error", err.Error(), "")
		return err
	}
	w.OK(map[string]string{
		"name":   "pura",
		"target": chosen,
	},
		output.WithSummary("Installed pura skill → %s", chosen),
		output.WithBreadcrumb("list", "pura skill ls", "See every installed copy"),
		output.WithBreadcrumb("remove", "pura skill rm pura --target "+chosen, "Uninstall this copy"),
	)
	fmt.Fprintf(w.Err, "  ✓ Installed to %s/pura/SKILL.md\n", chosen)
	return nil
}

// ---------- ls ----------

func newSkillLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List installed skills across every known target",
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			type row struct {
				Name   string `json:"name"`
				Target string `json:"target"`
				Source string `json:"source"` // "builtin" | "disk"
			}
			var rows []row

			// Always surface builtins first — they're present in the binary
			// even before any install ran.
			for _, b := range skill.BuiltinSkills() {
				rows = append(rows, row{Name: b.Name, Target: "<builtin>", Source: "builtin"})
			}

			for _, t := range knownTargets() {
				names, _ := skill.NewManagerWithDir(t.Dir).List()
				for _, n := range names {
					rows = append(rows, row{Name: n, Target: t.Dir, Source: "disk"})
				}
			}

			sort.Slice(rows, func(i, j int) bool {
				if rows[i].Name != rows[j].Name {
					return rows[i].Name < rows[j].Name
				}
				return rows[i].Target < rows[j].Target
			})

			w.OK(rows,
				output.WithSummary("%d skill row(s)", len(rows)),
				output.WithBreadcrumb("install", "pura skill", "Pick a target and install"),
			)
			w.Print("  %-14s %-10s %s\n", "NAME", "SOURCE", "TARGET")
			w.Print("  %-14s %-10s %s\n", "────", "──────", "──────")
			for _, r := range rows {
				w.Print("  %-14s %-10s %s\n", r.Name, r.Source, r.Target)
			}
			return nil
		},
	}
}

// ---------- install ----------

func newSkillInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install [name]",
		Short: "Install a skill (defaults to the built-in pura skill)",
		Long: `Non-interactive install.

With no args and no --source, installs the built-in pura SKILL.md.
Pass --source <url-or-path> to install an external skill; defaults to
installing as "pura" unless a name is provided.`,
		Example: `  pura skill install                               # pura → default target
  pura skill install --target ~/.claude/skills     # pura → chosen target
  pura skill install my-skill --source ./path      # external → local dir
  pura skill install my-skill --source https://…   # external → URL`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()

			name := "pura"
			if len(args) == 1 {
				name = args[0]
			}

			target := skillInstallTarget
			if target == "" {
				home, _ := os.UserHomeDir()
				target = filepath.Join(home, ".claude", "skills")
			}
			mgr := skill.NewManagerWithDir(target)

			var err error
			if skillInstallSource != "" {
				err = mgr.InstallFromSource(name, skillInstallSource)
			} else {
				err = mgr.InstallBuiltin(name)
			}
			if err != nil {
				w.Error("skill_error", err.Error(), "")
				return err
			}

			w.OK(map[string]string{
				"name":   name,
				"target": target,
				"source": skillInstallSource,
			},
				output.WithSummary("Installed %s → %s", name, target),
				output.WithBreadcrumb("list", "pura skill ls", "See every installed copy"),
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&skillInstallTarget, "target", "", "Destination directory (default: ~/.claude/skills)")
	cmd.Flags().StringVar(&skillInstallSource, "source", "", "External source (URL or local path); omit for the built-in")
	return cmd
}

// ---------- run ----------

func newSkillRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run <name>",
		Short: "Print a skill's SKILL.md to stdout",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			name := args[0]
			// Search every known target, then the built-in bundle.
			for _, t := range knownTargets() {
				data, err := skill.NewManagerWithDir(t.Dir).Read(name)
				if err == nil {
					writeSkillContent(w, data)
					return nil
				}
			}
			// Last chance: the built-in bundle directly.
			data, err := skill.NewManagerWithDir(os.TempDir()).Read(name)
			if err != nil {
				w.Error("not_found", err.Error(), "List installed skills with `pura skill ls`.",
					output.WithBreadcrumb("list", "pura skill ls", "Find what's installed"))
				return err
			}
			writeSkillContent(w, data)
			return nil
		},
	}
}

func writeSkillContent(w *writerLike, data []byte) {
	fmt.Fprint(w.Out, string(data))
	if !strings.HasSuffix(string(data), "\n") {
		fmt.Fprintln(w.Out)
	}
}

// ---------- rm ----------

func newSkillRmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove"},
		Short:   "Uninstall a skill copy",
		Long: `Remove an on-disk installation. Builtins stay available in the binary —
you can re-install with ` + "`pura skill install`" + ` at any time.

Without --target, removes from every known directory where the skill is
installed. Pass --target to limit to one.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			name := args[0]

			var removed []string
			tried := knownTargets()
			if skillInstallTarget != "" {
				tried = []skillTarget{{Label: "user", Dir: skillInstallTarget}}
			}
			for _, t := range tried {
				mgr := skill.NewManagerWithDir(t.Dir)
				if err := mgr.Remove(name); err == nil {
					removed = append(removed, t.Dir)
				}
			}
			if len(removed) == 0 {
				w.Error("not_found",
					fmt.Sprintf("Skill %q was not installed in any known target", name),
					"Run `pura skill ls` to see what's installed.",
					output.WithBreadcrumb("list", "pura skill ls", "See installed skills"),
				)
				return errors.New("not installed")
			}
			w.OK(map[string]any{"name": name, "removed_from": removed},
				output.WithSummary("Removed %s from %d target(s)", name, len(removed)),
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&skillInstallTarget, "target", "", "Limit removal to this directory")
	return cmd
}
