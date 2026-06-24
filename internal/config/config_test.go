package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAuthPrecedence(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(EnvAPIKey, "env-key")

	if _, err := Save(Config{APIKey: "config-key", BaseURL: "https://config.example"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	rt, err := Resolve("flag-key", "https://flag.example")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if rt.APIKey != "flag-key" || rt.APIKeySource != "flag" {
		t.Fatalf("API key precedence = %q via %q", rt.APIKey, rt.APIKeySource)
	}
	if rt.BaseURL != "https://flag.example" || rt.BaseURLSource != "flag" {
		t.Fatalf("base URL precedence = %q via %q", rt.BaseURL, rt.BaseURLSource)
	}
}

func TestResolveUsesEnvBeforeConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(EnvAPIKey, "env-key")

	if _, err := Save(Config{APIKey: "config-key"}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	rt, err := Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if rt.APIKey != "env-key" || rt.APIKeySource != "env" {
		t.Fatalf("API key = %q via %q, want env", rt.APIKey, rt.APIKeySource)
	}
}

func TestResolveMissingAuthReportsDefaultBaseURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAPIKey, "")

	rt, err := Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if rt.APIKey != "" || rt.APIKeySource != "missing" {
		t.Fatalf("auth = %q via %q, want missing", rt.APIKey, rt.APIKeySource)
	}
	if rt.BaseURL != DefaultBaseURL || rt.BaseURLSource != "default" {
		t.Fatalf("base URL = %q via %q, want default", rt.BaseURL, rt.BaseURLSource)
	}
	if rt.ConfigPath != filepath.Join(home, ".podscribe", "config.json") {
		t.Fatalf("config path = %q", rt.ConfigPath)
	}
}

func TestSaveWritesPrivateConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, err := Save(Config{APIKey: "secret", BaseURL: DefaultBaseURL})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config permissions = %o, want 0600", got)
	}
}
