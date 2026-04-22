package skill

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// stubFS lets tests inject a tiny in-memory FS in place of the embedded
// skills/ tree. Keeps tests from depending on the real SKILL.md (which
// would couple them to copy we want to revise freely).
func stubFS(t *testing.T, path string, body []byte) *fstest.MapFS {
	t.Helper()
	return &fstest.MapFS{
		path: &fstest.MapFile{Data: body, Mode: 0o644},
	}
}

func TestLookupBuiltin(t *testing.T) {
	e, ok := LookupBuiltin("pura")
	if !ok || e.Name != "pura" {
		t.Errorf("LookupBuiltin(pura) = %+v ok=%v", e, ok)
	}
	if _, ok := LookupBuiltin("does-not-exist"); ok {
		t.Error("want ok=false for missing name")
	}
}

func TestInstallBuiltin_WritesFileAtomically(t *testing.T) {
	dir := t.TempDir()
	fs := stubFS(t, "pura/SKILL.md", []byte("hello skill"))
	mgr := newManagerWithSource(dir, fs)

	if err := mgr.InstallBuiltin("pura"); err != nil {
		t.Fatalf("InstallBuiltin: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "pura", "SKILL.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello skill" {
		t.Errorf("content = %q", string(got))
	}

	// No tempfiles left behind.
	entries, _ := os.ReadDir(filepath.Join(dir, "pura"))
	for _, e := range entries {
		if e.Name() == "SKILL.md" {
			continue
		}
		t.Errorf("stale file: %s", e.Name())
	}
}

func TestInstallBuiltin_UnknownNameErrors(t *testing.T) {
	dir := t.TempDir()
	mgr := newManagerWithSource(dir, stubFS(t, "pura/SKILL.md", []byte("x")))
	if err := mgr.InstallBuiltin("nope"); err == nil {
		t.Fatal("want error for missing builtin")
	}
}

func TestInstallBuiltin_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	mgr := newManagerWithSource(dir, stubFS(t, "pura/SKILL.md", []byte("v1")))
	_ = mgr.InstallBuiltin("pura")

	mgr = newManagerWithSource(dir, stubFS(t, "pura/SKILL.md", []byte("v2 newer")))
	if err := mgr.InstallBuiltin("pura"); err != nil {
		t.Fatalf("second install: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pura", "SKILL.md"))
	if string(got) != "v2 newer" {
		t.Errorf("want overwrite, got %q", string(got))
	}
}

func TestInstallFromSource_HTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# from http"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	mgr := NewManagerWithDir(dir)
	if err := mgr.InstallFromSource("remote", srv.URL+"/SKILL.md"); err != nil {
		t.Fatalf("InstallFromSource http: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "remote", "SKILL.md"))
	if string(got) != "# from http" {
		t.Errorf("content = %q", string(got))
	}
}

func TestInstallFromSource_HTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	mgr := NewManagerWithDir(t.TempDir())
	if err := mgr.InstallFromSource("x", srv.URL); err == nil {
		t.Fatal("want error on 500")
	}
}

func TestInstallFromSource_LocalDir(t *testing.T) {
	src := t.TempDir()
	_ = os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# from dir"), 0o644)
	dir := t.TempDir()
	mgr := NewManagerWithDir(dir)
	if err := mgr.InstallFromSource("local", src); err != nil {
		t.Fatalf("InstallFromSource dir: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "local", "SKILL.md"))
	if string(got) != "# from dir" {
		t.Errorf("content = %q", string(got))
	}
}

func TestInstallFromSource_LocalFile(t *testing.T) {
	src := t.TempDir()
	file := filepath.Join(src, "custom.md")
	_ = os.WriteFile(file, []byte("# direct file"), 0o644)
	dir := t.TempDir()
	mgr := NewManagerWithDir(dir)
	if err := mgr.InstallFromSource("customised", file); err != nil {
		t.Fatalf("InstallFromSource file: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "customised", "SKILL.md"))
	if string(got) != "# direct file" {
		t.Errorf("content = %q", string(got))
	}
}

func TestList_EmptyAndPopulated(t *testing.T) {
	dir := t.TempDir()
	mgr := newManagerWithSource(dir, stubFS(t, "pura/SKILL.md", []byte("x")))

	names, err := mgr.List()
	if err != nil || len(names) != 0 {
		t.Errorf("empty: err=%v names=%v", err, names)
	}

	_ = mgr.InstallBuiltin("pura")
	names, _ = mgr.List()
	if len(names) != 1 || names[0] != "pura" {
		t.Errorf("after install: %v", names)
	}
}

func TestList_IgnoresDirsWithoutSkillFile(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "bogus"), 0o755)
	names, _ := NewManagerWithDir(dir).List()
	if len(names) != 0 {
		t.Errorf("want empty, got %v", names)
	}
}

func TestRead_PrefersInstalled_ThenBuiltin(t *testing.T) {
	dir := t.TempDir()
	fs := stubFS(t, "pura/SKILL.md", []byte("builtin"))
	mgr := newManagerWithSource(dir, fs)

	// With nothing installed we fall back to the embed bundle.
	got, err := mgr.Read("pura")
	if err != nil || string(got) != "builtin" {
		t.Fatalf("fallback to builtin failed: %q err=%v", got, err)
	}

	// Install a disk copy with different content — Read prefers disk.
	_ = mgr.writeSkill("pura", []byte("disk"))
	got, _ = mgr.Read("pura")
	if string(got) != "disk" {
		t.Errorf("want disk copy, got %q", got)
	}
}

func TestRead_UnknownNameErrors(t *testing.T) {
	mgr := newManagerWithSource(t.TempDir(), stubFS(t, "pura/SKILL.md", []byte("x")))
	if _, err := mgr.Read("ghost"); err == nil {
		t.Fatal("want error for unknown skill")
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	mgr := newManagerWithSource(dir, stubFS(t, "pura/SKILL.md", []byte("x")))
	_ = mgr.InstallBuiltin("pura")

	if err := mgr.Remove("pura"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pura")); !os.IsNotExist(err) {
		t.Errorf("dir still exists: %v", err)
	}
}

func TestRemove_NotInstalled(t *testing.T) {
	if err := NewManagerWithDir(t.TempDir()).Remove("nope"); err == nil {
		t.Error("want error when skill not installed")
	}
}

func TestInstall_Convenience_RoutesOnSource(t *testing.T) {
	// Empty source → builtin.
	dir := t.TempDir()
	mgr := newManagerWithSource(dir, stubFS(t, "pura/SKILL.md", []byte("bx")))
	if err := mgr.Install("pura", ""); err != nil {
		t.Fatalf("Install convenience: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "pura", "SKILL.md"))
	if string(got) != "bx" {
		t.Errorf("content = %q", got)
	}

	// Non-empty source → InstallFromSource path.
	dir2 := t.TempDir()
	src := t.TempDir()
	_ = os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("from-src"), 0o644)
	if err := NewManagerWithDir(dir2).Install("x", src); err != nil {
		t.Fatalf("Install with source: %v", err)
	}
	got, _ = os.ReadFile(filepath.Join(dir2, "x", "SKILL.md"))
	if string(got) != "from-src" {
		t.Errorf("content = %q", got)
	}
}

func TestDir(t *testing.T) {
	d := t.TempDir()
	if NewManagerWithDir(d).Dir() != d {
		t.Error("Dir() should round-trip")
	}
}
