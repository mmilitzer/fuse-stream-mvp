package config

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"
)

type Config struct {
	APIBase      string `toml:"api_base"`
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
	Mountpoint   string `toml:"mountpoint"`
}

func Load() (*Config, error) {
	cfg := &Config{
		APIBase: "https://api.xvid.com/v1",
	}

	if runtime.GOOS == "darwin" {
		cfg.Mountpoint = "/Volumes/FuseStream"
	} else {
		cfg.Mountpoint = "/mnt/fusestream"
	}

	home, err := os.UserHomeDir()
	if err == nil {
		configPath := filepath.Join(home, ".fuse-stream-mvp", "config.toml")
		if _, err := os.Stat(configPath); err == nil {
			if _, err := toml.DecodeFile(configPath, cfg); err != nil {
				return nil, err
			}
		}
	}

	if v := os.Getenv("FSMVP_API_BASE"); v != "" {
		cfg.APIBase = v
	}
	if v := os.Getenv("FSMVP_CLIENT_ID"); v != "" {
		cfg.ClientID = v
	}
	if v := os.Getenv("FSMVP_CLIENT_SECRET"); v != "" {
		cfg.ClientSecret = v
	}
	if v := os.Getenv("FSMVP_MOUNTPOINT"); v != "" {
		cfg.Mountpoint = v
	}

	return cfg, nil
}
