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
	
	// Fetch mode configuration
	FetchMode             string `toml:"fetch_mode"`              // "range-lru" or "temp-file"
	TempDir               string `toml:"temp_dir"`                // directory for temp files
	ChunkSize             int    `toml:"chunk_size"`              // chunk size in bytes
	MaxConcurrentRequests int    `toml:"max_concurrent_requests"` // max concurrent HTTP requests
	CacheSize             int    `toml:"cache_size"`              // LRU cache size in chunks
}

func Load() (*Config, error) {
	cfg := &Config{
		APIBase:               "https://api.xvid.com/v1",
		FetchMode:             "range-lru", // default
		ChunkSize:             4194304,     // 4MB default
		MaxConcurrentRequests: 4,
		CacheSize:             8,
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

	// Apply environment variable overrides
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
	if v := os.Getenv("FSMVP_FETCH_MODE"); v != "" {
		cfg.FetchMode = v
	}
	if v := os.Getenv("FSMVP_TEMP_DIR"); v != "" {
		cfg.TempDir = v
	}

	return cfg, nil
}
