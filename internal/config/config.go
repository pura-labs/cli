package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	DefaultAPIURL  = "https://pura.so"
	ConfigDir      = ".config/pura"
	ConfigFileName = "config.json"
	LocalConfigDir = ".pura"
)

// Config holds all resolved configuration values.
type Config struct {
	APIURL  string `json:"api_url"`
	Token   string `json:"token,omitempty"`
	Handle  string `json:"handle,omitempty"`
	Profile string `json:"profile,omitempty"`
	Theme   string `json:"theme,omitempty"`

	apiURLExplicit bool `json:"-"`
}

// Load resolves config with layered priority: flags > env > local > global > defaults.
func Load(flagAPIURL, flagToken, flagProfile string) *Config {
	cfg := &Config{
		APIURL: DefaultAPIURL,
		Theme:  "default",
	}

	// Layer 1: Global config
	if global, err := readConfigFile(globalConfigPath()); err == nil {
		if global.APIURL != "" {
			cfg.apiURLExplicit = true
		}
		mergeConfig(cfg, global)
	}

	// Layer 2: Local config (walk up from cwd)
	if local, err := readConfigFile(localConfigPath()); err == nil {
		if local.APIURL != "" {
			cfg.apiURLExplicit = true
		}
		mergeConfig(cfg, local)
	}

	// Layer 3: Environment variables
	if v := os.Getenv("PURA_API_URL"); v != "" {
		cfg.APIURL = v
		cfg.apiURLExplicit = true
	}
	if v := os.Getenv("PURA_TOKEN"); v != "" {
		cfg.Token = v
	}
	if v := os.Getenv("PURA_HANDLE"); v != "" {
		cfg.Handle = v
	}
	if v := os.Getenv("PURA_PROFILE"); v != "" {
		cfg.Profile = v
	}

	// Layer 4: CLI flags (highest priority)
	if flagAPIURL != "" {
		cfg.APIURL = flagAPIURL
		cfg.apiURLExplicit = true
	}
	if flagToken != "" {
		cfg.Token = flagToken
	}
	if flagProfile != "" {
		cfg.Profile = flagProfile
	}

	return cfg
}

// HasExplicitAPIURL reports whether APIURL came from config, env, or flags
// rather than the built-in default.
func (cfg *Config) HasExplicitAPIURL() bool {
	return cfg != nil && cfg.apiURLExplicit
}

// Set writes a key-value pair to the global config file.
func Set(key, value string) error {
	path := globalConfigPath()
	cfg, _ := readConfigFile(path)
	if cfg == nil {
		cfg = &Config{APIURL: DefaultAPIURL}
	}

	switch key {
	case "api_url":
		cfg.APIURL = value
	case "token":
		cfg.Token = value
	case "handle":
		cfg.Handle = value
	case "profile":
		cfg.Profile = value
	case "theme":
		cfg.Theme = value
	default:
		return &os.PathError{Op: "set", Path: key, Err: os.ErrInvalid}
	}

	return writeConfigFile(path, cfg)
}

// Get reads a key from the resolved config.
func Get(cfg *Config, key string) (string, bool) {
	switch key {
	case "api_url":
		return cfg.APIURL, true
	case "token":
		return cfg.Token, true
	case "handle":
		return cfg.Handle, true
	case "profile":
		return cfg.Profile, true
	case "theme":
		return cfg.Theme, true
	default:
		return "", false
	}
}

// All returns all config key-value pairs.
func All(cfg *Config) map[string]string {
	return map[string]string{
		"api_url": cfg.APIURL,
		"token":   maskToken(cfg.Token),
		"handle":  cfg.Handle,
		"profile": cfg.Profile,
		"theme":   cfg.Theme,
	}
}

func globalConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ConfigDir, ConfigFileName)
}

func localConfigPath() string {
	dir, _ := os.Getwd()
	for {
		path := filepath.Join(dir, LocalConfigDir, ConfigFileName)
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func readConfigFile(path string) (*Config, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func writeConfigFile(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func mergeConfig(dst, src *Config) {
	if src.APIURL != "" {
		dst.APIURL = src.APIURL
	}
	if src.Token != "" {
		dst.Token = src.Token
	}
	if src.Handle != "" {
		dst.Handle = src.Handle
	}
	if src.Profile != "" {
		dst.Profile = src.Profile
	}
	if src.Theme != "" {
		dst.Theme = src.Theme
	}
}

func maskToken(token string) string {
	if len(token) <= 12 {
		return token
	}
	return token[:12] + "..."
}
