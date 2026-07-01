package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	ServerURL string `json:"server_url"`
	MachineID string `json:"machine_id"`
	APIKey    string `json:"api_key"`
	Name      string `json:"name"`
	APIPort   int    `json:"api_port"`
	UpdateURL string `json:"update_url"`
}

func DefaultConfig() *Config {
	return &Config{
		ServerURL:  "",
		MachineID:  "",
		APIKey:     generateAPIKey(),
		Name:       "",
		APIPort:    DefaultAPIPort,
		UpdateURL:  DefaultUpdateURL,
	}
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		data = data[3:]
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.ServerURL == "" {
		return nil, fmt.Errorf("server_url is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("api_key is required")
	}
	if cfg.Name == "" {
		cfg.Name, _ = os.Hostname()
	}
	if cfg.APIPort <= 0 || cfg.APIPort > 65535 {
		cfg.APIPort = DefaultAPIPort
	}
	return &cfg, nil
}

func WriteConfig(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func generateAPIKey() string {
	b := make([]byte, 24)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func generateMachineID() string {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "pc"
	}
	b := make([]byte, 2)
	rand.Read(b)
	suffix := hex.EncodeToString(b)
	if len(hostname) > 20 {
		hostname = hostname[:20]
	}
	return sanitizeName(hostname) + "-" + suffix
}

func sanitizeName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "pc"
	}
	return string(out)
}
