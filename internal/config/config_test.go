package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg := Load("", "", "")
	if cfg.APIURL != DefaultAPIURL {
		t.Errorf("default APIURL = %q, want %q", cfg.APIURL, DefaultAPIURL)
	}
	if cfg.Theme != "default" {
		t.Errorf("default Theme = %q, want %q", cfg.Theme, "default")
	}
}

func TestLoadFlagOverrides(t *testing.T) {
	t.Setenv("PURA_API_URL", "https://env.example.com")
	cfg := Load("https://flag.example.com", "flag-token", "flag-profile")
	if cfg.APIURL != "https://flag.example.com" {
		t.Errorf("APIURL = %q, want flag override", cfg.APIURL)
	}
	if cfg.Token != "flag-token" {
		t.Errorf("Token = %q, want flag-token", cfg.Token)
	}
	if cfg.Profile != "flag-profile" {
		t.Errorf("Profile = %q, want flag-profile", cfg.Profile)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	t.Setenv("PURA_API_URL", "https://env.example.com")
	t.Setenv("PURA_TOKEN", "env-token")
	t.Setenv("PURA_HANDLE", "@env-user")
	cfg := Load("", "", "")
	if cfg.APIURL != "https://env.example.com" {
		t.Errorf("APIURL = %q, want env override", cfg.APIURL)
	}
	if cfg.Token != "env-token" {
		t.Errorf("Token = %q, want env-token", cfg.Token)
	}
	if cfg.Handle != "@env-user" {
		t.Errorf("Handle = %q, want @env-user", cfg.Handle)
	}
}

func TestSetAndReadGlobalConfig(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	t.Setenv("HOME", tmpDir)
	defer t.Setenv("HOME", origHome)

	if err := Set("api_url", "https://test.pura.so"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}

	path := filepath.Join(tmpDir, ConfigDir, ConfigFileName)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("config file not created")
	}

	cfg, err := readConfigFile(path)
	if err != nil {
		t.Fatalf("readConfigFile() error = %v", err)
	}
	if cfg.APIURL != "https://test.pura.so" {
		t.Errorf("persisted APIURL = %q, want https://test.pura.so", cfg.APIURL)
	}
}

func TestSetInvalidKey(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	err := Set("invalid_key", "value")
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestGetKnownKeys(t *testing.T) {
	cfg := &Config{APIURL: "https://x.com", Token: "tok", Handle: "@alice", Profile: "p", Theme: "paper"}
	tests := []struct {
		key  string
		want string
		ok   bool
	}{
		{"api_url", "https://x.com", true},
		{"token", "tok", true},
		{"handle", "@alice", true},
		{"profile", "p", true},
		{"theme", "paper", true},
		{"unknown", "", false},
	}
	for _, tt := range tests {
		v, ok := Get(cfg, tt.key)
		if ok != tt.ok || v != tt.want {
			t.Errorf("Get(%q) = (%q, %v), want (%q, %v)", tt.key, v, ok, tt.want, tt.ok)
		}
	}
}

func TestMaskToken(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"short", "short"},
		{"sk_pur_abcdefghijklmnop", "sk_pur_abcde..."},
	}
	for _, tt := range tests {
		got := maskToken(tt.in)
		if got != tt.want {
			t.Errorf("maskToken(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAllConfig(t *testing.T) {
	cfg := &Config{APIURL: "https://pura.so", Token: "sk_pur_test1234567890", Handle: "@alice", Theme: "default"}
	all := All(cfg)
	if all["api_url"] != "https://pura.so" {
		t.Errorf("api_url = %q", all["api_url"])
	}
	if all["token"] != "sk_pur_test1..." {
		t.Errorf("token should be masked, got %q", all["token"])
	}
	if all["handle"] != "@alice" {
		t.Errorf("handle = %q, want @alice", all["handle"])
	}
}
