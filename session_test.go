package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalDirCreation(t *testing.T) {
	// Setup a temporary CWD
	cwd, err := os.MkdirTemp("", "tenazas-test-cwd-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cwd)

	sess := &Session{
		ID:  "test-session",
		CWD: cwd,
	}

	// We'll add a method sess.EnsureLocalDir()
	localDir, err := sess.EnsureLocalDir()
	if err != nil {
		t.Errorf("failed to ensure local dir: %v", err)
	}

	expectedDir := filepath.Join(cwd, ".tenazas")
	if localDir != expectedDir {
		t.Errorf("expected %s, got %s", expectedDir, localDir)
	}

	if _, err := os.Stat(expectedDir); os.IsNotExist(err) {
		t.Errorf("expected directory %s to exist", expectedDir)
	}
}
