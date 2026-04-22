package auth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestSaveAndLoadFullRecord(t *testing.T) {
	store := newStoreWithDir(t.TempDir())

	in := Record{
		Token:     "sk_pura_abc123",
		Handle:    "demo",
		APIUrl:    "https://pura.so",
		UserID:    "u_1",
		KeyID:     "k_1",
		KeyPrefix: "sk_pura_abc12345",
	}
	if err := store.Save("default", in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load("default")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != in {
		t.Errorf("Load mismatch\ngot:  %+v\nwant: %+v", got, in)
	}
}

func TestLoadMissingReturnsErrNotFound(t *testing.T) {
	store := newStoreWithDir(t.TempDir())
	_, err := store.Load("nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestLoadTokenlessProfileReturnsErrNotFound(t *testing.T) {
	// A row without a token is "metadata only" and must not be mistaken for
	// an authenticated session.
	store := newStoreWithDir(t.TempDir())
	if err := store.Save("default", Record{Handle: "h", APIUrl: "https://pura.so"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	_, err := store.Load("default")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	store := newStoreWithDir(t.TempDir())

	if err := store.Save("work", Record{Token: "t"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Delete("work"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Deleting again must succeed silently.
	if err := store.Delete("work"); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
	if _, err := store.Load("work"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete, want ErrNotFound, got %v", err)
	}
}

func TestMultipleProfilesAreIndependent(t *testing.T) {
	store := newStoreWithDir(t.TempDir())

	want := map[string]string{"default": "tdefault", "work": "twork", "staging": "tstage"}
	for name, tok := range want {
		if err := store.Save(name, Record{Token: tok, Handle: name + "-h"}); err != nil {
			t.Fatalf("Save(%q): %v", name, err)
		}
	}
	for name, tok := range want {
		got, err := store.Load(name)
		if err != nil {
			t.Fatalf("Load(%q): %v", name, err)
		}
		if got.Token != tok {
			t.Errorf("Load(%q).Token = %q, want %q", name, got.Token, tok)
		}
		if got.Handle != name+"-h" {
			t.Errorf("Load(%q).Handle = %q, want %q", name, got.Handle, name+"-h")
		}
	}
}

func TestFilePermissionsAre0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits don't apply on Windows")
	}
	dir := t.TempDir()
	store := newStoreWithDir(dir)

	if err := store.Save("default", Record{Token: "x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "credentials.json"))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file perms = %o, want 0600", got)
	}
}

func TestSetTokenMergesWithExistingRecord(t *testing.T) {
	store := newStoreWithDir(t.TempDir())

	if err := store.Save("default", Record{Handle: "h", APIUrl: "https://pura.so"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := store.Load("default"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound before token set, got %v", err)
	}
	if err := store.SetToken("default", "sk_pura_new"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}
	got, err := store.Load("default")
	if err != nil {
		t.Fatalf("Load after SetToken: %v", err)
	}
	if got.Token != "sk_pura_new" || got.Handle != "h" || got.APIUrl != "https://pura.so" {
		t.Errorf("merge lost fields: %+v", got)
	}
}

func TestLoadTokenShortcut(t *testing.T) {
	store := newStoreWithDir(t.TempDir())

	_ = store.Save("default", Record{Token: "hello"})
	got, err := store.LoadToken("default")
	if err != nil {
		t.Fatalf("LoadToken: %v", err)
	}
	if got != "hello" {
		t.Errorf("LoadToken = %q, want %q", got, "hello")
	}
}

func TestListReturnsProfiles(t *testing.T) {
	store := newStoreWithDir(t.TempDir())

	for _, name := range []string{"default", "work"} {
		_ = store.Save(name, Record{Token: "t"})
	}
	names, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
	}
	if !seen["default"] || !seen["work"] {
		t.Errorf("List missing profiles: got %v", names)
	}
}

// TestConcurrentWritesDoNotCorruptFile pins the atomic-rename invariant:
// even under racey writers, the canonical file is always parseable — we
// never observe a truncated / partially-written JSON.
func TestConcurrentWritesDoNotCorruptFile(t *testing.T) {
	store := newStoreWithDir(t.TempDir())

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = store.Save("p", Record{Token: "t", Handle: "h"})
				_, _ = store.Load("p")
				_ = i * j
			}
		}()
	}
	wg.Wait()

	got, err := store.Load("p")
	if err != nil {
		t.Fatalf("Load after concurrent writes: %v", err)
	}
	if got.Token != "t" || got.Handle != "h" {
		t.Errorf("final record corrupted: %+v", got)
	}
}

// TestNoTempfilesLeftBehind covers the defer-cleanup path: after a successful
// Save, the only file should be credentials.json — no .tmp stragglers.
func TestNoTempfilesLeftBehind(t *testing.T) {
	dir := t.TempDir()
	store := newStoreWithDir(dir)
	for i := 0; i < 5; i++ {
		_ = store.Save("p", Record{Token: "t"})
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() == "credentials.json" {
			continue
		}
		t.Errorf("unexpected leftover file: %s", e.Name())
	}
}
