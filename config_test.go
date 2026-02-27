package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "tenazas-config-*")
	defer os.RemoveAll(tmpDir)

	os.Setenv("TENAZAS_STORAGE_DIR", tmpDir)
	defer os.Unsetenv("TENAZAS_STORAGE_DIR")

	os.Setenv("TENAZAS_TG_TOKEN", "test-token")
	defer os.Unsetenv("TENAZAS_TG_TOKEN")

	os.Setenv("TENAZAS_ALLOWED_IDS", "123, 456")
	defer os.Unsetenv("TENAZAS_ALLOWED_IDS")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.StorageDir != tmpDir {
		t.Errorf("expected storage dir %s, got %s", tmpDir, cfg.StorageDir)
	}

	if cfg.TelegramToken != "test-token" {
		t.Errorf("expected token 'test-token', got %s", cfg.TelegramToken)
	}

	if len(cfg.AllowedUserIDs) != 2 || cfg.AllowedUserIDs[0] != 123 || cfg.AllowedUserIDs[1] != 456 {
		t.Errorf("expected allowed IDs [123, 456], got %v", cfg.AllowedUserIDs)
	}

	// Verify directories created
	if _, err := os.Stat(filepath.Join(tmpDir, "sessions")); os.IsNotExist(err) {
		t.Error("expected sessions directory to be created")
	}
}
