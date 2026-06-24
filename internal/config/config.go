package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/emiliopalmerini/podscribe/internal/apperr"
)

const (
	DefaultBaseURL = "https://api.elevenlabs.io"
	EnvAPIKey      = "ELEVENLABS_API_KEY"
)

type Config struct {
	APIKey  string `json:"api_key,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
}

type Runtime struct {
	APIKey        string
	APIKeySource  string
	BaseURL       string
	BaseURLSource string
	ConfigPath    string
	ConfigFound   bool
}

func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", apperr.Wrap(apperr.CodeConfig, "could not determine home directory", err)
	}
	return filepath.Join(home, ".podscribe", "config.json"), nil
}

func Load() (Config, string, bool, error) {
	path, err := Path()
	if err != nil {
		return Config{}, "", false, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, path, false, nil
	}
	if err != nil {
		return Config{}, path, false, apperr.Wrap(apperr.CodeConfig, fmt.Sprintf("could not read config at %s", path), err)
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, path, true, apperr.Wrap(apperr.CodeConfig, fmt.Sprintf("could not parse config at %s", path), err)
	}
	return cfg, path, true, nil
}

func Save(cfg Config) (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not create config directory %s", filepath.Dir(path)), err)
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", apperr.Wrap(apperr.CodeConfig, "could not encode config", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not write config at %s", path), err)
	}
	return path, nil
}

func Resolve(apiKeyFlag, baseURLFlag string) (Runtime, error) {
	cfg, path, found, err := Load()
	if err != nil {
		return Runtime{}, err
	}

	rt := Runtime{
		ConfigPath:    path,
		ConfigFound:   found,
		BaseURL:       DefaultBaseURL,
		BaseURLSource: "default",
		APIKeySource:  "missing",
	}

	if strings.TrimSpace(cfg.BaseURL) != "" {
		rt.BaseURL = strings.TrimSpace(cfg.BaseURL)
		rt.BaseURLSource = "config"
	}
	if strings.TrimSpace(baseURLFlag) != "" {
		rt.BaseURL = strings.TrimSpace(baseURLFlag)
		rt.BaseURLSource = "flag"
	}

	if strings.TrimSpace(cfg.APIKey) != "" {
		rt.APIKey = strings.TrimSpace(cfg.APIKey)
		rt.APIKeySource = "config"
	}
	if env := strings.TrimSpace(os.Getenv(EnvAPIKey)); env != "" {
		rt.APIKey = env
		rt.APIKeySource = "env"
	}
	if strings.TrimSpace(apiKeyFlag) != "" {
		rt.APIKey = strings.TrimSpace(apiKeyFlag)
		rt.APIKeySource = "flag"
	}

	return rt, nil
}

func RedactedKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "[REDACTED]"
	}
	return key[:4] + "..." + key[len(key)-4:]
}
