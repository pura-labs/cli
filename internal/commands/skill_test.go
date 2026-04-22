package commands

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSkillCmd_TopLevelWizardRefusesOutsideTTY pins the non-TTY wizard
// guard. Tests never have a TTY, so we should always hit the error branch
// when `pura skill` is invoked with no subcommand.
func TestSkillCmd_TopLevelWizardRefusesOutsideTTY(t *testing.T) {
	setupIsolatedHome(t)
	_, err := runCmd(t, "skill")
	if err == nil {
		t.Fatal("want error when running wizard outside a TTY")
	}
	if err.Error() != "non-tty wizard" {
		t.Errorf("err = %v", err)
	}
}

func TestSkillInstall_BuiltinPura_DefaultTarget(t *testing.T) {
	home := setupIsolatedHome(t)

	_, err := runCmd(t, "skill", "install", "--json")
	if err != nil {
		t.Fatalf("skill install: %v", err)
	}
	// Default target is ~/.claude/skills.
	f := filepath.Join(home, ".claude", "skills", "pura", "SKILL.md")
	info, err := os.Stat(f)
	if err != nil {
		t.Fatalf("SKILL.md not written: %v", err)
	}
	if info.Size() == 0 {
		t.Errorf("SKILL.md is empty")
	}
}

func TestSkillInstall_CustomTarget(t *testing.T) {
	setupIsolatedHome(t)
	custom := t.TempDir()
	_, err := runCmd(t, "skill", "install", "--target", custom, "--json")
	if err != nil {
		t.Fatalf("install --target: %v", err)
	}
	if _, err := os.Stat(filepath.Join(custom, "pura", "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not at custom target: %v", err)
	}
}

func TestSkillInstall_FromLocalSource(t *testing.T) {
	setupIsolatedHome(t)
	src := t.TempDir()
	_ = os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# local"), 0o644)
	target := t.TempDir()
	_, err := runCmd(t, "skill", "install", "my-skill", "--source", src, "--target", target, "--json")
	if err != nil {
		t.Fatalf("install --source: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(target, "my-skill", "SKILL.md"))
	if string(got) != "# local" {
		t.Errorf("content = %q", got)
	}
}

func TestSkillInstall_FromHTTPSource(t *testing.T) {
	setupIsolatedHome(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# remote"))
	}))
	defer srv.Close()

	target := t.TempDir()
	_, err := runCmd(t, "skill", "install", "remote-skill",
		"--source", srv.URL+"/SKILL.md",
		"--target", target, "--json")
	if err != nil {
		t.Fatalf("install http: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(target, "remote-skill", "SKILL.md"))
	if !strings.Contains(string(got), "remote") {
		t.Errorf("content = %q", got)
	}
}

func TestSkillLs_SurfacesBuiltinAndInstalled(t *testing.T) {
	setupIsolatedHome(t)
	// Install into default target (one of knownTargets).
	if _, err := runCmd(t, "skill", "install", "--json"); err != nil {
		t.Fatalf("install default: %v", err)
	}
	out, err := runCmd(t, "skill", "ls", "--json")
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, out)
	}
	env := mustUnmarshalEnvelope(t, out)
	rows, _ := env["data"].([]any)
	if len(rows) < 2 {
		t.Fatalf("want builtin + installed rows, got %+v", rows)
	}
	foundBuiltin := false
	for _, r := range rows {
		if r.(map[string]any)["source"] == "builtin" {
			foundBuiltin = true
			break
		}
	}
	if !foundBuiltin {
		t.Errorf("no builtin row in ls output: %+v", rows)
	}
}

func TestSkillRun_FallsBackToBuiltin(t *testing.T) {
	setupIsolatedHome(t)
	out, err := runCmd(t, "skill", "run", "pura")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// The rewritten SKILL.md has a frontmatter `name: pura` and a header.
	if !strings.Contains(out, "name: pura") && !strings.Contains(out, "Pura — Agent Skill") {
		t.Errorf("unexpected run output (truncated):\n%s", head(out, 400))
	}
}

func TestSkillRun_UnknownFails(t *testing.T) {
	setupIsolatedHome(t)
	_, err := runCmd(t, "skill", "run", "does-not-exist")
	if err == nil {
		t.Fatal("want error for unknown skill")
	}
}

func TestSkillRm_RemovesInstalled(t *testing.T) {
	home := setupIsolatedHome(t)
	if _, err := runCmd(t, "skill", "install", "--json"); err != nil {
		t.Fatalf("install: %v", err)
	}
	target := filepath.Join(home, ".claude", "skills", "pura")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected install at %s", target)
	}
	if _, err := runCmd(t, "skill", "rm", "pura", "--json"); err != nil {
		t.Fatalf("rm: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("skill dir still exists after rm: %v", err)
	}
}

func TestSkillRm_NotFoundFails(t *testing.T) {
	setupIsolatedHome(t)
	_, err := runCmd(t, "skill", "rm", "never-installed", "--json")
	if err == nil {
		t.Fatal("want error when nothing to remove")
	}
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
