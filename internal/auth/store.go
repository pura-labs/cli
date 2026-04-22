// Package auth owns credential persistence for the CLI.
//
// A *Record* bundles a token with the metadata we need to drive the UI and
// follow-up API calls without re-hitting the server. Records are addressed
// by *profile* name so `pura --profile work` and `pura --profile personal`
// stay cleanly separated.
//
// Storage:
//
//	Everything lives in a single file: ~/.config/pura/credentials.json,
//	mode 0600 (owner read/write). No OS keyring, no dbus, no Secret Service —
//	the file is deliberately simple to reason about, cat-able for debugging,
//	and works identically on macOS, Linux (including headless), Windows, and
//	inside Docker / CI images.
//
//	On shared machines the user can mount `~/.config/pura` on an encrypted
//	volume; we don't try to layer our own secret manager on top.
//
// Schema (credentials.json):
//
//	{
//	  "version": 1,
//	  "profiles": {
//	    "default": {
//	      "token":      "sk_pura_...",
//	      "handle":     "cardbingo",
//	      "api_url":    "https://pura.so",
//	      "user_id":    "abc123",
//	      "key_id":     "key_xyz",
//	      "key_prefix": "sk_pura_deadbeef"
//	    }
//	  }
//	}
//
// Writes are atomic: we stage to a sibling tempfile with 0600 and then
// rename in place. That guarantees readers never observe a half-written
// file and a crash mid-write cannot leave garbage at the canonical path.
//
// Project is pre-launch, so we're free to break the file format. No
// migration code — if you're reading an old file, delete it.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Record is the full credential bundle for one profile.
//
// All fields are optional except Token when the caller is about to make a
// server call; callers decide their own required-field policy.
type Record struct {
	Token     string `json:"token,omitempty"`
	Handle    string `json:"handle,omitempty"`
	APIUrl    string `json:"api_url,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	KeyID     string `json:"key_id,omitempty"`
	KeyPrefix string `json:"key_prefix,omitempty"`
}

// ErrNotFound is returned when a profile has no stored credentials.
var ErrNotFound = errors.New("no credentials for profile")

type fileSchema struct {
	Version  int                `json:"version"`
	Profiles map[string]*Record `json:"profiles"`
}

// Store manages persistent credentials. The zero value is not usable —
// call NewStore (or newStoreWithDir in tests).
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore creates a Store anchored at ~/.config/pura/.
func NewStore() *Store {
	home, _ := os.UserHomeDir()
	return &Store{dir: filepath.Join(home, ".config", "pura")}
}

// newStoreWithDir creates a Store rooted at a custom directory. Used by
// tests to avoid touching the user's real config dir.
func newStoreWithDir(dir string) *Store {
	return &Store{dir: dir}
}

// credPath returns the canonical file location.
func (s *Store) credPath() string {
	return filepath.Join(s.dir, "credentials.json")
}

// readFile returns the parsed file, or an empty skeleton if it doesn't exist.
// Callers should not create "no credentials" rows by mistake — check error.
func (s *Store) readFile() (*fileSchema, error) {
	data, err := os.ReadFile(s.credPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &fileSchema{Version: 1, Profiles: map[string]*Record{}}, nil
		}
		return nil, err
	}
	var f fileSchema
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing credentials: %w", err)
	}
	if f.Profiles == nil {
		f.Profiles = map[string]*Record{}
	}
	return &f, nil
}

// writeFile atomically serializes f to disk with 0600 permissions. Uses a
// tempfile + rename so partial writes can never expose a truncated file and
// a crash mid-write leaves the previous good file in place.
func (s *Store) writeFile(f *fileSchema) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	f.Version = 1
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling credentials: %w", err)
	}

	// Stage to a tempfile in the same directory (so rename is atomic on POSIX).
	tmp, err := os.CreateTemp(s.dir, "credentials.json.*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	// Remove the stale tempfile if anything past this point errors out.
	defer func() { _ = os.Remove(tmpPath) }()

	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod tempfile: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing tempfile: %w", err)
	}
	if err := os.Rename(tmpPath, s.credPath()); err != nil {
		return fmt.Errorf("atomic rename: %w", err)
	}
	// Belt & suspenders: ensure perms survive the rename on filesystems that
	// don't preserve mode across ReplaceFile (e.g. Windows).
	return os.Chmod(s.credPath(), 0o600)
}

// Save persists the full record for a profile.
// Safe to call concurrently across profiles.
func (s *Store) Save(profile string, rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(profile, rec)
}

// saveLocked does the real work for Save. Caller must hold s.mu.
// Split out so other locked paths (SetToken) can compose without
// re-acquiring the mutex.
func (s *Store) saveLocked(profile string, rec Record) error {
	f, err := s.readFile()
	if err != nil {
		return err
	}
	stored := rec
	f.Profiles[profile] = &stored
	return s.writeFile(f)
}

// Load returns the record for a profile. Returns ErrNotFound if the profile
// doesn't exist or has no token.
func (s *Store) Load(profile string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.readFile()
	if err != nil {
		return Record{}, err
	}
	rec, ok := f.Profiles[profile]
	if !ok || rec == nil {
		return Record{}, ErrNotFound
	}
	if rec.Token == "" {
		// Partial row with metadata but no secret. Commands should not
		// attempt to call the server with an empty Bearer, so treat this
		// as "not signed in" for callers.
		return *rec, ErrNotFound
	}
	return *rec, nil
}

// Delete removes a profile. Missing profile is NOT an error — Delete is
// idempotent. Callers who need "it existed" semantics can Load first.
func (s *Store) Delete(profile string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.readFile()
	if err != nil {
		return err
	}
	if _, ok := f.Profiles[profile]; !ok {
		return nil
	}
	delete(f.Profiles, profile)
	return s.writeFile(f)
}

// List returns all known profile names (sorted order not guaranteed).
func (s *Store) List() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.readFile()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(f.Profiles))
	for name := range f.Profiles {
		out = append(out, name)
	}
	return out, nil
}

// LoadToken is Load() projected onto Token — convenience for call sites
// that only care about the Authorization value.
func (s *Store) LoadToken(profile string) (string, error) {
	rec, err := s.Load(profile)
	if err != nil {
		return "", err
	}
	return rec.Token, nil
}

// SetToken updates only the Token for `profile`, preserving every other
// field that might already be set. Safe when the caller doesn't know the
// full record — e.g. `pura config set token=...`.
//
// Holds the lock for the full read-modify-write so a concurrent SetToken
// or Save for the same profile can't race and clobber the other's update.
func (s *Store) SetToken(profile, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := s.readFile()
	if err != nil {
		return err
	}
	var existing Record
	if rec, ok := f.Profiles[profile]; ok && rec != nil {
		existing = *rec
	}
	existing.Token = token
	return s.saveLocked(profile, existing)
}
