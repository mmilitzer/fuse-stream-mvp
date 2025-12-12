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
	FetchMode             string `toml:"fetch_mode"`              // "temp-file" (default, recommended) or "range-lru" (experimental)
	TempDir               string `toml:"temp_dir"`                // directory for temp files (temp-file mode only)
	ChunkSize             int    `toml:"chunk_size"`              // chunk size in bytes
	MaxConcurrentRequests int    `toml:"max_concurrent_requests"` // max concurrent HTTP requests
	CacheSize             int    `toml:"cache_size"`              // LRU cache size in chunks (range-lru mode only)
	
	// System integration configuration
	EnableAppNap bool `toml:"enable_app_nap"` // Prevent App Nap when filesystem is mounted (macOS only, default: false)
	
	// Debug configuration
	DebugSkipEviction      bool `toml:"debug_skip_eviction"`       // Skip eviction in Release to isolate eviction bugs (default: false)
	DebugFSKitSingleThread bool `toml:"debug_fskit_single_thread"` // Use single-threaded FUSE mode for debugging (default: false)
}

func Load() (*Config, error) {
	cfg := &Config{
		APIBase:               "https://api.xvid.com/v1",
		FetchMode:             "temp-file", // default (range-lru is experimental)
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
	if v := os.Getenv("FSMVP_ENABLE_APP_NAP"); v != "" {
		cfg.EnableAppNap = v == "true" || v == "1" || v == "yes"
	}

	return cfg, nil
}
