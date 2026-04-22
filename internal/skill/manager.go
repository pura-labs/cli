// Package skill manages SKILL.md installation.
//
// Design goals (borrowed from basecamp-cli / fizzy-cli / hey-cli):
//
//   1. One Manager per target directory. Callers that want to install into
//      multiple well-known locations (e.g. ~/.claude/skills/ AND
//      ~/.agents/skills/) just instantiate several Managers.
//
//   2. Reads *first* from the embedded FS shipped with the binary, then
//      from disk (for local development where the repo is next to the
//      binary). That means `pura skill install` works after
//      `curl | install.sh` — the user never needs the repo.
//
//   3. Install is idempotent. Re-installing overwrites the destination,
//      cleaning up tempfiles on failure.
//
// Skill layout on disk matches Anthropic's convention:
//
//   <target>/<name>/SKILL.md

package skill

import (
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pura-labs/cli/skills"
)

// validSkillName matches the kebab/underscore identifier that every builtin
// uses: lowercase letters, digits, underscore, dash. Anchored so a name like
// "../../etc/passwd" or "foo/bar" can't smuggle path segments through.
var validSkillName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// sanitizeSkillName rejects any input that wouldn't be safe to combine with
// filepath.Join against a trusted root. Returns the original name when valid.
func sanitizeSkillName(name string) (string, error) {
	if !validSkillName.MatchString(name) {
		return "", fmt.Errorf("invalid skill name %q — must match [a-z0-9][a-z0-9_-]*", name)
	}
	return name, nil
}

// Manager installs into / reads from a single target directory.
type Manager struct {
	// dir is the target directory. Each skill gets its own <dir>/<name>/
	// subdirectory, with SKILL.md inside.
	dir string

	// source is the embed.FS we pull builtins from. Swappable so tests
	// can use their own fs.FS.
	source fs.FS
}

// NewManager returns a Manager rooted at `~/.config/pura/skills/`.
// Prefer NewManagerWithDir to target ~/.claude/skills/ or similar.
func NewManager() *Manager {
	home, _ := os.UserHomeDir()
	return &Manager{
		dir:    filepath.Join(home, ".config", "pura", "skills"),
		source: skills.FS,
	}
}

// NewManagerWithDir returns a Manager rooted at the given dir. Used when
// installing to a specific agent-ecosystem location.
func NewManagerWithDir(dir string) *Manager {
	return &Manager{dir: dir, source: skills.FS}
}

// newManagerWithSource is an internal test hook — allows swapping the embed
// FS without also touching the filesystem layout.
func newManagerWithSource(dir string, source fs.FS) *Manager {
	return &Manager{dir: dir, source: source}
}

// Dir returns the destination directory (useful for logging what got touched).
func (m *Manager) Dir() string { return m.dir }

// List returns the names of every skill installed at this target, sorted
// by directory entry order (typically alpha on Linux).
func (m *Manager) List() ([]string, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading skills directory: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(m.dir, e.Name(), "SKILL.md")); err == nil {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// InstallBuiltin copies a built-in skill (from the embed.FS) into the
// target directory. Overwrites any existing copy atomically — writes to a
// tempfile, fsyncs, renames.
func (m *Manager) InstallBuiltin(name string) error {
	if _, err := sanitizeSkillName(name); err != nil {
		return err
	}
	entry, ok := LookupBuiltin(name)
	if !ok {
		return fmt.Errorf("no builtin skill named %q", name)
	}
	data, err := fs.ReadFile(m.source, entry.SourcePath)
	if err != nil {
		return fmt.Errorf("reading embedded skill %q: %w", name, err)
	}
	return m.writeSkill(name, data)
}

// InstallFromSource installs from an arbitrary location. `source` may be:
//
//   - "http(s)://…/SKILL.md"   — HTTP GET
//   - local file path           — the file IS SKILL.md
//   - local directory path      — dir contains SKILL.md
//
// Returns a typed error so callers can produce friendlier hints.
func (m *Manager) InstallFromSource(name, source string) error {
	if _, err := sanitizeSkillName(name); err != nil {
		return err
	}
	var data []byte
	var err error

	switch {
	case strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://"):
		data, err = fetchHTTP(source)
	default:
		data, err = readLocal(source)
	}
	if err != nil {
		return err
	}
	return m.writeSkill(name, data)
}

func fetchHTTP(url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching %s: HTTP %d", url, resp.StatusCode)
	}
	// Cap the read to guard against a runaway response (1 MiB SKILL.md is
	// already huge; 4 MiB is a very generous upper bound).
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

func readLocal(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if info.IsDir() {
		path = filepath.Join(path, "SKILL.md")
	}
	return os.ReadFile(path)
}

// writeSkill lays down <dir>/<name>/SKILL.md atomically.
func (m *Manager) writeSkill(name string, data []byte) error {
	destDir := filepath.Join(m.dir, name)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("creating skill directory: %w", err)
	}
	destFile := filepath.Join(destDir, "SKILL.md")

	// Staged write: tmpfile → fsync → rename. Prevents a partial SKILL.md
	// from being observed mid-install by a concurrent agent scan.
	tmp, err := os.CreateTemp(destDir, "SKILL.md.*.tmp")
	if err != nil {
		return fmt.Errorf("creating tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing skill: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing tempfile: %w", err)
	}
	return os.Rename(tmpPath, destFile)
}

// Read returns the contents of an installed skill. Falls back to the
// built-in bundle when nothing is on disk — so a user who never ran
// `install` can still ask `pura skill run pura` and see the doc.
func (m *Manager) Read(name string) ([]byte, error) {
	if _, err := sanitizeSkillName(name); err != nil {
		return nil, err
	}
	destFile := filepath.Join(m.dir, name, "SKILL.md")
	if data, err := os.ReadFile(destFile); err == nil {
		return data, nil
	}
	if entry, ok := LookupBuiltin(name); ok {
		if data, err := fs.ReadFile(m.source, entry.SourcePath); err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("skill %q not found", name)
}

// Remove uninstalls a skill from this target directory. Built-in bundles
// can't be removed (they live in the binary), but we happily remove the
// on-disk copy that was installed from one.
func (m *Manager) Remove(name string) error {
	if _, err := sanitizeSkillName(name); err != nil {
		return err
	}
	target := filepath.Join(m.dir, name)
	if _, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("skill %q is not installed in %s", name, m.dir)
		}
		return err
	}
	return os.RemoveAll(target)
}

// ---- legacy shim ----
//
// Older callers (and tests) used `Install(name, source)`. Keep it as a
// thin dispatcher — new code should use InstallBuiltin / InstallFromSource
// directly because the intent is clearer.

// Install is a convenience: if `source` is empty, install the built-in;
// otherwise delegate to InstallFromSource.
func (m *Manager) Install(name, source string) error {
	if source == "" {
		return m.InstallBuiltin(name)
	}
	return m.InstallFromSource(name, source)
}
