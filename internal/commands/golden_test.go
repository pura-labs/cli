package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// resetCobraFlagState walks the command tree and resets every flag's
// Value to its DefValue + flips Changed back to false. Needed after
// invoking `--help` in tests because cobra doesn't un-set the help flag,
// and a sticky help=true makes the very next Execute() print usage
// instead of running the command.
func resetCobraFlagState(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, sub := range cmd.Commands() {
		resetCobraFlagState(sub)
	}
}

// Golden-file snapshots of --help output, one per top-level command.
//
// Why:
//   --help is the discovery surface for humans AND agents. SURFACE.txt
//   tracks command/flag presence; these goldens track the actual
//   text shown to users. Silent UX drift (typos, changed examples,
//   reworded Short/Long) shows up here first.
//
// Regenerating after an intentional change:
//   UPDATE_GOLDEN=1 go test ./internal/commands/... -run TestHelpGolden

var helpCases = []struct {
	name   string
	argv   []string
	golden string // relative to testdata/golden/help/
}{
	{"root", []string{"--help"}, "root.txt"},
	{"auth", []string{"auth", "--help"}, "auth.txt"},
	{"auth_login", []string{"auth", "login", "--help"}, "auth-login.txt"},
	{"keys", []string{"keys", "--help"}, "keys.txt"},
	{"versions", []string{"versions", "--help"}, "versions.txt"},
	{"chat", []string{"chat", "--help"}, "chat.txt"},
	{"stats", []string{"stats", "--help"}, "stats.txt"},
	{"events", []string{"events", "--help"}, "events.txt"},
	{"claim", []string{"claim", "--help"}, "claim.txt"},
	{"doctor", []string{"doctor", "--help"}, "doctor.txt"},
	{"skill", []string{"skill", "--help"}, "skill.txt"},
}

func TestHelpGolden(t *testing.T) {
	for _, tc := range helpCases {
		t.Run(tc.name, func(t *testing.T) {
			resetCommandGlobals()
			defer resetCommandGlobals()

			// Cobra's --help routes through the command's own output writer
			// when set; capture via stdout.
			out := captureStdout(t, func() {
				cmd := rootCmd
				cmd.SetArgs(tc.argv)
				// Cobra returns nil for --help; any non-nil err is a real bug.
				if err := cmd.Execute(); err != nil {
					t.Fatalf("cmd Execute(%v) returned err=%v", tc.argv, err)
				}
			})
			// CRITICAL: wipe cobra flag state so subsequent tests don't see
			// a sticky `help=true` and mistakenly get the help page back.
			t.Cleanup(func() { resetCobraFlagState(rootCmd) })

			// Normalize — strip trailing whitespace so a mis-wrapped line
			// doesn't look like a content change.
			got := normalizeHelp(out)

			goldenPath := filepath.Join("testdata", "golden", "help", tc.golden)

			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
					t.Fatalf("mkdir golden dir: %v", err)
				}
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v (first run? set UPDATE_GOLDEN=1)", goldenPath, err)
			}
			if got != string(want) {
				t.Errorf("golden mismatch for %s\n--- want\n%s\n--- got\n%s",
					tc.golden, string(want), got)
			}
		})
	}
}

// normalizeHelp trims trailing spaces on each line and ensures the output
// ends with exactly one newline. Cobra's renderer is otherwise stable
// enough across versions that we don't need heavier normalization.
func normalizeHelp(s string) string {
	var out strings.Builder
	for _, line := range strings.Split(s, "\n") {
		out.WriteString(strings.TrimRight(line, " \t"))
		out.WriteString("\n")
	}
	// Collapse run-on trailing newlines.
	return strings.TrimRight(out.String(), "\n") + "\n"
}
