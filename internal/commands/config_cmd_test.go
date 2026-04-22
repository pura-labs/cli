package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigSetAndGet(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	// Use a temp dir as home to avoid touching the real config
	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	// Create the config dir
	configDir := filepath.Join(tmpHome, ".config", "pura")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	cmd := rootCmd
	cmd.SetArgs([]string{"config", "set", "theme", "paper"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("config set failed: %v", err)
	}

	cmd.SetArgs([]string{"config", "get", "theme"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("config get failed: %v", err)
	}

	cmd.SetArgs([]string{"config", "set", "handle", "@alice"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("config set handle failed: %v", err)
	}

	cmd.SetArgs([]string{"config", "get", "handle"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("config get handle failed: %v", err)
	}
}

func TestConfigList(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	cmd := rootCmd
	cmd.SetArgs([]string{"config", "list"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("config list failed: %v", err)
	}
}

func TestConfigSet_InvalidKey(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	tmpHome := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", origHome)

	cmd := rootCmd
	cmd.SetArgs([]string{"config", "set", "invalid_key", "value"})
	if err := cmd.Execute(); err == nil {
		t.Error("expected error for invalid config key")
	}
}

func TestConfigSetTokenStoresInProfileCredentials(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cmd := rootCmd
	cmd.SetArgs([]string{"config", "set", "token", "tok_secret", "--profile", "work"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config set token failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpHome, ".config", "pura", "credentials.json"))
	if err != nil {
		t.Fatalf("read credentials: %v", err)
	}
	// Don't pin exact JSON formatting — structure matters, not whitespace.
	s := string(data)
	if !strings.Contains(s, `"work"`) || !strings.Contains(s, `"tok_secret"`) {
		t.Fatalf("credentials should contain work/tok_secret; got: %s", s)
	}
	if !strings.Contains(s, `"version": 1`) {
		t.Fatalf("expected v1 schema marker; got: %s", s)
	}
}
