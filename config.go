package main

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

type Config struct {
	StorageDir     string  `json:"storage_dir"`
	TelegramToken  string  `json:"telegram_token"`
	AllowedUserIDs []int64 `json:"allowed_user_ids"`
	UpdateInterval int     `json:"update_interval"`
	GeminiBinPath  string  `json:"gemini_bin_path"`
	MaxLoops       int     `json:"max_loops"`
}

func getDefaultStoragePath() string {
	usr, _ := user.Current()
	return filepath.Join(usr.HomeDir, DefaultStorageDir)
}

func loadConfig() (*Config, error) {
	storageDir := os.Getenv("TENAZAS_STORAGE_DIR")
	if storageDir == "" {
		storageDir = getDefaultStoragePath()
	}

	cfg := &Config{
		StorageDir:     storageDir,
		UpdateInterval: DefaultTgInterval,
		GeminiBinPath:  "gemini",
		MaxLoops:       DefaultMaxLoops,
	}

	cfgPath := filepath.Join(cfg.StorageDir, ConfigFileName)
	if data, err := os.ReadFile(cfgPath); err == nil {
		json.Unmarshal(data, cfg)
	}

	if envToken := os.Getenv("TENAZAS_TG_TOKEN"); envToken != "" {
		cfg.TelegramToken = envToken
	}
	if envIDs := os.Getenv("TENAZAS_ALLOWED_IDS"); envIDs != "" {
		cfg.AllowedUserIDs = nil // Clear if provided by ENV to override file
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

	os.MkdirAll(cfg.StorageDir, 0755)
	os.MkdirAll(filepath.Join(cfg.StorageDir, "sessions"), 0755)
	os.MkdirAll(filepath.Join(cfg.StorageDir, "skills"), 0755)
	os.MkdirAll(filepath.Join(cfg.StorageDir, "heartbeats"), 0755)

	return cfg, nil
}
