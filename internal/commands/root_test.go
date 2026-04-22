package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pura-labs/cli/internal/config"
)

// resetCommandGlobals is the single entry point tests use to zero every
// package-level flag the commands/ tree keeps. Cobra re-uses the same
// *Command instances across `cobra.Execute()` calls, so flag values are
// sticky and will bleed into the next subtest unless explicitly reset.
// Every subcommand that owns its own flag var must expose a resetXxxFlags()
// helper and call it in from here; that way individual tests only ever
// need to call resetCommandGlobals() (see callers in *_test.go).
func resetCommandGlobals() {
	flagJSON = false
	flagQuiet = false
	flagJQ = ""
	flagAPIURL = ""
	flagToken = ""
	flagHandle = ""
	flagProfile = ""
	flagVerbose = false
	resetAuthFlags()
	resetKeysFlags()
	resetVersionsFlags()
	resetObservabilityFlags()
	resetChatFlags()
	resetSkillFlags()
	resetMcpFlags()
	resetToolFlags()
	resetSheetFlags()
}

// writeCredsFile emits a credentials.json in the v1 schema.
func writeCredsFile(t *testing.T, dir, profile, token, handle, apiURL string) {
	t.Helper()
	body := `{"version":1,"profiles":{"` + profile + `":{"token":"` + token + `"`
	if handle != "" {
		body += `,"handle":"` + handle + `"`
	}
	if apiURL != "" {
		body += `,"api_url":"` + apiURL + `"`
	}
	body += `}}}`
	if err := os.MkdirAll(filepath.Join(dir, ".config", "pura"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(dir, ".config", "pura", "credentials.json"),
		[]byte(body),
		0o600,
	); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
}

func writeLocalConfigFile(t *testing.T, dir, apiURL string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, config.LocalConfigDir), 0o700); err != nil {
		t.Fatalf("mkdir local config: %v", err)
	}
	body := `{"api_url":"` + apiURL + `"}`
	if err := os.WriteFile(
		filepath.Join(dir, config.LocalConfigDir, config.ConfigFileName),
		[]byte(body),
		0o600,
	); err != nil {
		t.Fatalf("write local config: %v", err)
	}
}

func TestLoadConfigLoadsProfileTokenFromStore(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	writeCredsFile(t, tmpHome, "work", "tok_work", "", "")

	flagProfile = "work"
	cfg := loadConfig()
	if cfg.Token != "tok_work" {
		t.Fatalf("Token = %q, want tok_work", cfg.Token)
	}
}

func TestLoadConfigUsesDefaultProfileToken(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	writeCredsFile(t, tmpHome, "default", "tok_default", "alice", "https://self-hosted.example")

	cfg := loadConfig()
	if cfg.Token != "tok_default" {
		t.Fatalf("Token = %q, want tok_default", cfg.Token)
	}
	if cfg.Handle != "alice" {
		t.Fatalf("Handle = %q, want alice (loadConfig should surface stored handle)", cfg.Handle)
	}
	if cfg.APIURL != "https://self-hosted.example" {
		t.Fatalf("APIURL = %q, want https://self-hosted.example", cfg.APIURL)
	}
}

func TestLoadConfigKeepsExplicitAPIURLOverride(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	writeCredsFile(t, tmpHome, "default", "tok_default", "alice", "https://stored.example")

	flagAPIURL = "https://flag.example"
	cfg := loadConfig()
	if cfg.APIURL != "https://flag.example" {
		t.Fatalf("APIURL = %q, want https://flag.example", cfg.APIURL)
	}
}

func TestLoadConfigKeepsProjectAPIURLOverStoredCredentialMetadata(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmpHome); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldWD)
	})
	writeCredsFile(t, tmpHome, "default", "tok_default", "alice", "https://stored.example")
	writeLocalConfigFile(t, tmpHome, "https://project.example")

	cfg := loadConfig()
	if cfg.APIURL != "https://project.example" {
		t.Fatalf("APIURL = %q, want https://project.example", cfg.APIURL)
	}
}

func TestNewClientNormalizesHandle(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	cfg := loadConfig()
	cfg.APIURL = "https://pura.so"
	cfg.Handle = "  @config-user  "

	client := newClient(nil, cfg)
	if client.Handle != "config-user" {
		t.Fatalf("Handle = %q, want config-user", client.Handle)
	}
}

func TestNewClientHandleFlagOverridesConfig(t *testing.T) {
	resetCommandGlobals()
	defer resetCommandGlobals()

	flagHandle = "@flag-user"
	client := newClient(nil, &config.Config{
		APIURL: "https://pura.so",
		Handle: "config-user",
	})
	if client.Handle != "flag-user" {
		t.Fatalf("Handle = %q, want flag-user", client.Handle)
	}
}
