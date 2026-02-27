package config

import (
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DefaultTgInterval = 500 // ms
	DefaultPageSize   = 5
	DefaultMaxLoops   = 5
	DefaultStorageDir = ".tenazas"
	ConfigFileName    = "config.json"
)

// ClientConfig holds settings for a single coding-agent client.
type ClientConfig struct {
	BinPath string `json:"bin_path"`
}

type Config struct {
	StorageDir     string                  `json:"storage_dir"`
	TelegramToken  string                  `json:"telegram_token"`
	AllowedUserIDs []int64                 `json:"allowed_user_ids"`
	UpdateInterval int                     `json:"update_interval"`
	GeminiBinPath  string                  `json:"gemini_bin_path"`
	MaxLoops       int                     `json:"max_loops"`
	DefaultClient  string                  `json:"default_client,omitempty"`
	Clients        map[string]ClientConfig `json:"clients,omitempty"`
	Channel        string                  `json:"channel,omitempty"`
}

func GetDefaultStoragePath() string {
	usr, _ := user.Current()
	return filepath.Join(usr.HomeDir, DefaultStorageDir)
}

func Load() (*Config, error) {
	storageDir := os.Getenv("TENAZAS_STORAGE_DIR")
	if storageDir == "" {
		storageDir = GetDefaultStoragePath()
	}

	cfg := &Config{
		StorageDir:     storageDir,
		UpdateInterval: DefaultTgInterval,
		GeminiBinPath:  "gemini",
		MaxLoops:       DefaultMaxLoops,
		DefaultClient:  "gemini",
	}

	cfgPath := filepath.Join(cfg.StorageDir, ConfigFileName)
	if data, err := os.ReadFile(cfgPath); err == nil {
		json.Unmarshal(data, cfg)
	}

	if envToken := os.Getenv("TENAZAS_TG_TOKEN"); envToken != "" {
		cfg.TelegramToken = envToken
	}
	if envIDs := os.Getenv("TENAZAS_ALLOWED_IDS"); envIDs != "" {
		cfg.AllowedUserIDs = nil
		ids := strings.Split(envIDs, ",")
		for _, idStr := range ids {
			if id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64); err == nil {
				cfg.AllowedUserIDs = append(cfg.AllowedUserIDs, id)
			}
		}
	}
	if envDir := os.Getenv("TENAZAS_STORAGE_DIR"); envDir != "" {
		cfg.StorageDir = envDir
	}
	if envLoops := os.Getenv("TENAZAS_MAX_LOOPS"); envLoops != "" {
		if l, err := strconv.Atoi(envLoops); err == nil {
			cfg.MaxLoops = l
		}
	}

	// Backward compatibility: populate Clients map from legacy GeminiBinPath
	if len(cfg.Clients) == 0 {
		cfg.Clients = map[string]ClientConfig{
			"gemini": {BinPath: cfg.GeminiBinPath},
		}
	}
	if cfg.DefaultClient == "" {
		cfg.DefaultClient = "gemini"
	}

	os.MkdirAll(cfg.StorageDir, 0755)
	os.MkdirAll(filepath.Join(cfg.StorageDir, "sessions"), 0755)
	os.MkdirAll(filepath.Join(cfg.StorageDir, "tasks"), 0755)
	os.MkdirAll(filepath.Join(cfg.StorageDir, "skills"), 0755)
	os.MkdirAll(filepath.Join(cfg.StorageDir, "heartbeats"), 0755)

	return cfg, nil
}
