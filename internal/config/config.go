package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type AccountConfig struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	ChatURL     string `yaml:"chat_url" json:"chat_url"`
	StorageDir  string `yaml:"storage_dir" json:"storage_dir"`
	Proxy       string `yaml:"proxy" json:"proxy"`
	CDPURL      string `yaml:"cdp_url" json:"cdp_url"`
	Enabled     bool   `yaml:"enabled" json:"enabled"`
}

type GatewayConfig struct {
	Port         int             `yaml:"port" json:"port"`
	APIKey       string          `yaml:"api_key" json:"api_key"`
	DefaultModel string          `yaml:"default_model" json:"default_model"`
	Headless     bool            `yaml:"headless" json:"headless"`
	DisableImages bool           `yaml:"disable_images" json:"disable_images"`
	TimeoutSec   int             `yaml:"timeout_sec" json:"timeout_sec"`
	Accounts     []AccountConfig `yaml:"accounts" json:"accounts"`
}

func DefaultConfig() GatewayConfig {
	return GatewayConfig{
		Port:          8765,
		APIKey:        "",
		DefaultModel:  "muse/muse_01",
		Headless:      true,
		DisableImages: true,
		TimeoutSec:    300,
		Accounts: []AccountConfig{
			{
				ID:         "muse_01",
				Name:       "Primary Muse Account",
				ChatURL:    "https://muse.ai",
				StorageDir: filepath.Join(".", "sessions", "muse_01"),
				Enabled:    true,
			},
		},
	}
}

func LoadConfig(path string) (*GatewayConfig, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &cfg, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func SaveConfig(path string, cfg *GatewayConfig) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
