// Built-in skill catalog.
//
// Content is embedded in the binary (via cli/skills/embed.go) so the CLI
// stays self-contained after `curl | install.sh` or a release archive.
// The registry maps user-facing names to their paths inside that embed FS;
// Manager.InstallBuiltin is what actually copies the bytes out.

package skill

// Entry describes one installable skill.
type Entry struct {
	Name        string
	Description string
	// SourcePath is the path inside the embed.FS (e.g. "pura/SKILL.md").
	// Empty for entries that came from a user-provided URL or local dir.
	SourcePath string
}

// BuiltinSkills returns every skill bundled with this CLI build. Today
// it's one; keeping the shape as a list means adding siblings is additive.
func BuiltinSkills() []Entry {
	return []Entry{
		{
			Name:        "pura",
			Description: "Drive the Pura CLI end-to-end — publish, AI edit, version, claim, stats.",
			SourcePath:  "pura/SKILL.md",
		},
	}
}

// LookupBuiltin returns the Entry with the matching name or ok=false.
func LookupBuiltin(name string) (Entry, bool) {
	for _, e := range BuiltinSkills() {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}
