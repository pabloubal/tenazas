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
	BinPath string            `json:"bin_path"`
	Models  map[string]string `json:"models,omitempty"` // tier → model name (high/medium/low)
}

// ChannelConfig holds settings for an external communication channel.
type ChannelConfig struct {
	Type           string  `json:"type"`                       // "telegram" or "disabled"
	Token          string  `json:"token,omitempty"`            // bot token
	AllowedUserIDs []int64 `json:"allowed_user_ids,omitempty"` // whitelist
	UpdateInterval int     `json:"update_interval,omitempty"`  // ms between streaming edits
}

type Config struct {
	// Core
	StorageDir string `json:"storage_dir"`
	MaxLoops   int    `json:"max_loops"`

	// Clients
	DefaultClient    string                  `json:"default_client"`
	DefaultModelTier string                  `json:"default_model_tier,omitempty"`
	Clients          map[string]ClientConfig `json:"clients,omitempty"`

	// Communication
	Channel ChannelConfig `json:"channel"`

	// Legacy (read for backward compat, not written by onboard)
	GeminiBinPath string `json:"gemini_bin_path,omitempty"`
}

// legacyConfig is used to detect and migrate old flat-field config formats.
type legacyConfig struct {
	Channel        json.RawMessage `json:"channel,omitempty"`
	TelegramToken  string          `json:"telegram_token,omitempty"`
	AllowedUserIDs []int64         `json:"allowed_user_ids,omitempty"`
	UpdateInterval int             `json:"update_interval,omitempty"`
	GeminiBinPath  string          `json:"gemini_bin_path,omitempty"`
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
		StorageDir:    storageDir,
		MaxLoops:      DefaultMaxLoops,
		DefaultClient: "gemini",
	}

	cfgPath := filepath.Join(cfg.StorageDir, ConfigFileName)
	data, fileErr := os.ReadFile(cfgPath)
	if fileErr == nil {
		// First pass: unmarshal into the current struct (handles new format).
		json.Unmarshal(data, cfg)

		// Second pass: detect legacy flat fields and migrate into Channel.
		var legacy legacyConfig
		json.Unmarshal(data, &legacy)

		if cfg.Channel.Type == "" {
			// "channel" was a string in older configs (e.g., "telegram" or "disabled").
			if len(legacy.Channel) > 0 && legacy.Channel[0] == '"' {
				var channelStr string
				json.Unmarshal(legacy.Channel, &channelStr)
				cfg.Channel.Type = channelStr
			}
			// Migrate flat telegram fields into Channel.
			if legacy.TelegramToken != "" && cfg.Channel.Token == "" {
				cfg.Channel.Token = legacy.TelegramToken
			}
			if len(legacy.AllowedUserIDs) > 0 && len(cfg.Channel.AllowedUserIDs) == 0 {
				cfg.Channel.AllowedUserIDs = legacy.AllowedUserIDs
			}
			if legacy.UpdateInterval > 0 && cfg.Channel.UpdateInterval == 0 {
				cfg.Channel.UpdateInterval = legacy.UpdateInterval
			}
			// Infer type from token if still empty.
			if cfg.Channel.Type == "" && cfg.Channel.Token != "" {
				cfg.Channel.Type = "telegram"
			}
		}

		// Migrate legacy GeminiBinPath into Clients map.
		if len(cfg.Clients) == 0 && legacy.GeminiBinPath != "" {
			cfg.Clients = map[string]ClientConfig{
				"gemini": {BinPath: legacy.GeminiBinPath},
			}
		}
	}

	// Environment variable overrides.
	if envToken := os.Getenv("TENAZAS_TG_TOKEN"); envToken != "" {
		cfg.Channel.Token = envToken
		if cfg.Channel.Type == "" {
			cfg.Channel.Type = "telegram"
		}
	}
	if envIDs := os.Getenv("TENAZAS_ALLOWED_IDS"); envIDs != "" {
		cfg.Channel.AllowedUserIDs = nil
		for _, s := range strings.Split(envIDs, ",") {
			if id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
				cfg.Channel.AllowedUserIDs = append(cfg.Channel.AllowedUserIDs, id)
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

	// Apply defaults for unset fields.
	if cfg.DefaultClient == "" {
		cfg.DefaultClient = "gemini"
	}
	if cfg.Channel.UpdateInterval == 0 {
		cfg.Channel.UpdateInterval = DefaultTgInterval
	}

	os.MkdirAll(cfg.StorageDir, 0755)
	os.MkdirAll(filepath.Join(cfg.StorageDir, "sessions"), 0755)
	os.MkdirAll(filepath.Join(cfg.StorageDir, "tasks"), 0755)
	os.MkdirAll(filepath.Join(cfg.StorageDir, "skills"), 0755)
	os.MkdirAll(filepath.Join(cfg.StorageDir, "heartbeats"), 0755)

	return cfg, nil
}
