// `pura versions {ls, show, diff, restore}` — version history for a doc.
//
// The server already keeps every prior version (manual saves, AI edits,
// and prior restores). These commands are the read + rollback surface.
//
// `diff` renders a unified diff between two versions. ANSI color only when
// stdout is a TTY; pipes get plain text. The diff algorithm is the standard
// LCS-based hunk builder — no third-party dep, ~80 LoC, enough for docs
// the size humans actually edit.

package commands

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/pura-labs/cli/internal/output"
	"github.com/spf13/cobra"
)

var (
	versionsRestoreYes bool
	versionsDiffColor  string // auto | always | never
)

func resetVersionsFlags() {
	versionsRestoreYes = false
	versionsDiffColor = "auto"
}

func newVersionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "versions",
		Short: "Inspect and restore document versions",
		Long: `Every manual save or AI edit creates a new version server-side. Use
these commands to audit history and roll back when needed.

  pura versions ls <slug>           List versions (newest first)
  pura versions show <slug> <N>     Dump version N's content
  pura versions diff <slug> A B     Unified diff between two versions
  pura versions restore <slug> N    Roll forward to version N`,
	}
	cmd.AddCommand(
		newVersionsLsCmd(),
		newVersionsShowCmd(),
		newVersionsDiffCmd(),
		newVersionsRestoreCmd(),
	)
	return cmd
}

// ---------- ls ----------

func newVersionsLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <slug>",
		Short: "List versions of a document",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			if cfg.Token == "" {
				w.Error("unauthorized", "No token configured", "Run `pura auth login`.",
					output.WithBreadcrumb("retry", "pura auth login", "Sign in"))
				return errors.New("no token")
			}
			slug := args[0]
			items, err := newClient(cmd, cfg).ListVersions(slug)
			if err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}

			if len(items) == 0 {
				w.OK(items, output.WithSummary("No versions yet"))
				w.Print("  No versions.\n")
				return nil
			}

			latest := items[0].Version
			w.OK(items,
				output.WithSummary("%d version(s) — latest v%d", len(items), latest),
				output.WithBreadcrumb("show", fmt.Sprintf("pura versions show %s %d", slug, latest), "View the latest version"),
				output.WithBreadcrumb("diff", fmt.Sprintf("pura versions diff %s %d %d", slug, latest-1, latest), "Diff against previous"),
			)
			w.Print("  %-5s %-8s %-6s %s\n", "VER", "BY", "ORIGIN", "CREATED")
			w.Print("  %-5s %-8s %-6s %s\n", "───", "──", "──────", "───────")
			for _, v := range items {
				origin := v.Origin
				if origin == "" {
					origin = "-"
				}
				created := v.CreatedAt
				if len(created) >= 16 {
					created = created[:16]
				}
				w.Print("  %-5d %-8s %-6s %s\n", v.Version, v.CreatedBy, origin, created)
			}
			return nil
		},
	}
}

// ---------- show ----------

func newVersionsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <slug> <version>",
		Short: "Print a specific version's content",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			if cfg.Token == "" {
				w.Error("unauthorized", "No token configured", "Run `pura auth login`.")
				return errors.New("no token")
			}
			slug := args[0]
			n, err := parseVersionArg(args[1])
			if err != nil {
				w.Error("validation", err.Error(), "")
				return err
			}
			v, err := newClient(cmd, cfg).GetVersion(slug, n)
			if err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}

			// On a TTY we print the content on stdout without envelope wrap
			// so users can `| less`. --json / non-TTY emits the full envelope.
			if !w.IsTTY || flagJSON || flagJQ != "" {
				w.OK(v,
					output.WithSummary("version %d (by %s)", v.Version, v.CreatedBy),
					output.WithBreadcrumb("restore", fmt.Sprintf("pura versions restore %s %d", slug, n), "Roll forward to this version"),
				)
				return nil
			}
			fmt.Fprint(w.Out, v.Content)
			if !strings.HasSuffix(v.Content, "\n") {
				fmt.Fprintln(w.Out, "")
			}
			return nil
		},
	}
}

// ---------- diff ----------

func newVersionsDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff <slug> <A> [<B>]",
		Short: "Unified diff between two versions (B defaults to latest)",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			if cfg.Token == "" {
				w.Error("unauthorized", "No token configured", "Run `pura auth login`.")
				return errors.New("no token")
			}
			slug := args[0]
			a, err := parseVersionArg(args[1])
			if err != nil {
				w.Error("validation", err.Error(), "")
				return err
			}
			client := newClient(cmd, cfg)

			var b int
			if len(args) == 3 {
				b, err = parseVersionArg(args[2])
				if err != nil {
					w.Error("validation", err.Error(), "")
					return err
				}
			} else {
				items, err := client.ListVersions(slug)
				if err != nil {
					w.Error("api_error", err.Error(), "")
					return err
				}
				if len(items) == 0 {
					w.Error("not_found", "No versions yet", "")
					return errors.New("no versions")
				}
				b = items[0].Version
			}

			va, err := client.GetVersion(slug, a)
			if err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}
			vb, err := client.GetVersion(slug, b)
			if err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}

			useColor := resolveColor(versionsDiffColor, w.IsTTY)
			diff := unifiedDiff(va.Content, vb.Content, fmt.Sprintf("v%d", a), fmt.Sprintf("v%d", b), useColor)

			if !w.IsTTY || flagJSON || flagJQ != "" {
				w.OK(map[string]any{
					"slug": slug,
					"a":    a,
					"b":    b,
					"diff": diff,
				},
					output.WithSummary("diff v%d..v%d", a, b),
					output.WithBreadcrumb("restore", fmt.Sprintf("pura versions restore %s %d", slug, a), "Roll back to version A"),
				)
				return nil
			}
			fmt.Fprint(w.Out, diff)
			return nil
		},
	}
	cmd.Flags().StringVar(&versionsDiffColor, "color", "auto", "Color output: auto | always | never")
	return cmd
}

// ---------- restore ----------

func newVersionsRestoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore <slug> <version>",
		Short: "Roll forward to a previous version",
		Long: `Restore creates a NEW version whose content mirrors the target version.
History is never rewritten — the prior state is always recoverable.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			w := newWriter()
			cfg := loadConfig()
			if cfg.Token == "" {
				w.Error("unauthorized", "No token configured", "Run `pura auth login`.")
				return errors.New("no token")
			}
			slug := args[0]
			n, err := parseVersionArg(args[1])
			if err != nil {
				w.Error("validation", err.Error(), "")
				return err
			}

			ok, err := confirmMutation(
				w,
				versionsRestoreYes,
				"--yes",
				fmt.Sprintf("Restore %s to v%d?", slug, n),
				"A new forward-rolling version will be created; older versions remain.",
				"Restore",
			)
			if err != nil {
				w.Error("confirmation_required",
					"Refusing to restore without confirmation",
					"Re-run with --yes in non-interactive mode.",
				)
				return err
			}
			if !ok {
				w.Print("  Cancelled.\n")
				return nil
			}

			v, err := newClient(cmd, cfg).RestoreVersion(slug, n)
			if err != nil {
				w.Error("api_error", err.Error(), "")
				return err
			}

			w.OK(v,
				output.WithSummary("Restored %s to v%d (new version v%d)", slug, n, v.Version),
				output.WithBreadcrumb("view", "pura open "+slug, "Open in browser"),
				output.WithBreadcrumb("history", "pura versions ls "+slug, "Confirm the new version"),
			)
			w.Print("  Restored %s to v%d → new v%d\n", slug, n, v.Version)
			return nil
		},
	}
	cmd.Flags().BoolVar(&versionsRestoreYes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

// ---------- helpers ----------

func parseVersionArg(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("version must be a positive integer, got %q", s)
	}
	return n, nil
}

// resolveColor centralizes the --color flag precedence.
//
//	"always" — force ANSI even on pipes (for tools that ingest color intentionally)
//	"never"  — strip even on a TTY
//	"auto"   — color iff TTY
func resolveColor(flag string, isTTY bool) bool {
	switch flag {
	case "always":
		return true
	case "never":
		return false
	default:
		return isTTY
	}
}

// unifiedDiff is a lightweight LCS-based diff. Returns a unified-format
// string suitable for human consumption; the point is "close enough to
// `diff -u`" without pulling in a dependency.
func unifiedDiff(a, b, labelA, labelB string, color bool) string {
	aLines := splitLines(a)
	bLines := splitLines(b)
	ops := diffLCS(aLines, bLines)

	var buf strings.Builder
	buf.WriteString("--- " + labelA + "\n")
	buf.WriteString("+++ " + labelB + "\n")

	// Group ops into hunks with 3 lines of context — standard `diff -u` feel.
	const context = 3
	i := 0
	for i < len(ops) {
		// Skip runs of identical lines outside any active hunk.
		if ops[i].kind == opEq {
			// Check: is there a non-Eq within `context` of here?
			runEnd := i
			for runEnd < len(ops) && ops[runEnd].kind == opEq {
				runEnd++
			}
			if runEnd == len(ops) {
				break
			}
			if runEnd-i > context {
				i = runEnd - context
			}
		}

		// Hunk header: find end (next long Eq-run or EOF).
		hStart := i
		j := i
		for j < len(ops) {
			if ops[j].kind == opEq {
				runEnd := j
				for runEnd < len(ops) && ops[runEnd].kind == opEq {
					runEnd++
				}
				if runEnd-j > context*2 {
					j += context // include trailing context
					break
				}
				j = runEnd
				continue
			}
			j++
		}
		if j > len(ops) {
			j = len(ops)
		}
		hunk := ops[hStart:j]

		// Count lines for the hunk header.
		var aCount, bCount int
		for _, o := range hunk {
			if o.kind != opAdd {
				aCount++
			}
			if o.kind != opDel {
				bCount++
			}
		}
		aStart := hStart
		// Compute line numbers by counting prior non-add / non-del ops.
		aLine, bLine := 1, 1
		for _, o := range ops[:hStart] {
			if o.kind != opAdd {
				aLine++
			}
			if o.kind != opDel {
				bLine++
			}
		}
		buf.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", aLine, aCount, bLine, bCount))
		for _, o := range hunk {
			switch o.kind {
			case opEq:
				buf.WriteString(" " + o.line + "\n")
			case opDel:
				buf.WriteString(colorize("-", "31", color) + o.line + colorReset(color) + "\n")
			case opAdd:
				buf.WriteString(colorize("+", "32", color) + o.line + colorReset(color) + "\n")
			}
		}
		i = j
		_ = aStart
	}
	return buf.String()
}

type diffOpKind int

const (
	opEq  diffOpKind = iota
	opDel            // in a, not in b
	opAdd            // in b, not in a
)

type diffOp struct {
	kind diffOpKind
	line string
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	if strings.HasSuffix(s, "\n") {
		s = s[:len(s)-1]
	}
	return strings.Split(s, "\n")
}

// diffLCS computes a classic longest-common-subsequence diff. O(n*m) time
// and space — fine for docs up to a few thousand lines. Trades memory for
// code clarity; we're diffing prose, not megabytes of logs.
func diffLCS(a, b []string) []diffOp {
	n, m := len(a), len(b)
	// dp[i][j] = LCS length of a[:i], b[:j]
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] >= dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	// Walk back to produce ops.
	var ops []diffOp
	i, j := n, m
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && a[i-1] == b[j-1]:
			ops = append([]diffOp{{opEq, a[i-1]}}, ops...)
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			ops = append([]diffOp{{opAdd, b[j-1]}}, ops...)
			j--
		default:
			ops = append([]diffOp{{opDel, a[i-1]}}, ops...)
			i--
		}
	}
	return ops
}

func colorize(prefix, code string, on bool) string {
	if !on {
		return prefix
	}
	return "\x1b[" + code + "m" + prefix
}

func colorReset(on bool) string {
	if !on {
		return ""
	}
	return "\x1b[0m"
}
