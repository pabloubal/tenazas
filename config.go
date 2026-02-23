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
	DefaultPort        = 18789
	DefaultTgInterval  = 500 // ms
	DefaultPageSize    = 5
	DefaultStorageDir  = ".tenazas"
	ConfigFileName     = "config.json"
)

type Config struct {
	StorageDir      string   `json:"storage_dir"`
	TelegramToken   string   `json:"telegram_token"`
	AllowedUserIDs  []int64  `json:"allowed_user_ids"`
	UpdateInterval  int      `json:"update_interval"` // ms
	GeminiBinPath   string   `json:"gemini_bin_path"`
}

func getDefaultStoragePath() string {
	usr, _ := user.Current()
	return filepath.Join(usr.HomeDir, DefaultStorageDir)
}

func loadConfig() (*Config, error) {
	cfg := &Config{
		StorageDir:     getDefaultStoragePath(),
		UpdateInterval: DefaultTgInterval,
		GeminiBinPath:  "gemini",
	}

	// 1. Try file
	cfgPath := filepath.Join(cfg.StorageDir, ConfigFileName)
	if data, err := os.ReadFile(cfgPath); err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}

	// 2. Env Overrides
	if envToken := os.Getenv("TENAZAS_TG_TOKEN"); envToken != "" {
		cfg.TelegramToken = envToken
	}
	if envIDs := os.Getenv("TENAZAS_ALLOWED_IDS"); envIDs != "" {
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

	// Ensure storage directory exists
	if err := os.MkdirAll(cfg.StorageDir, 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(cfg.StorageDir, "sessions"), 0755); err != nil {
		return nil, err
	}

	return cfg, nil
}
