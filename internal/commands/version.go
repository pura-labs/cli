package commands

import (
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version info",
		Run: func(cmd *cobra.Command, args []string) {
			w := newWriter()
			info := map[string]string{
				"version": versionStr,
				"commit":  commitStr,
				"date":    dateStr,
				"go":      runtime.Version(),
				"os":      runtime.GOOS,
				"arch":    runtime.GOARCH,
			}
			// When ldflags injection didn't run (version==dev and commit==none),
			// surface that to both the envelope and the styled line so users
			// filing bug reports aren't left guessing about whether they're on
			// a release build. Goreleaser always overrides both fields.
			if isUntaggedBuild() {
				info["build"] = "unstamped"
				w.Print("  ⚠  Unstamped build — version info is not a real release.\n")
				w.Print("     Use `pura` from a goreleaser-built binary, or set -X main.version=... at build time.\n")
			}
			w.OK(info)
			w.Print("  pura %s (%s) built %s\n", versionStr, commitStr[:min(7, len(commitStr))], dateStr)
			w.Print("  %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
		},
	}
}

// isUntaggedBuild reports whether the binary was compiled without ldflags
// version injection. If both version AND commit still carry the hardcoded
// fallback values, goreleaser didn't run — this is a `go build` artifact,
// not a release.
func isUntaggedBuild() bool {
	return versionStr == "dev" && commitStr == "none"
}
