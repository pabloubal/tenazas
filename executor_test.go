package main

import (
	"os"
	"testing"
)

func TestExecutorRun(t *testing.T) {
	// Create a dummy "gemini" script that outputs JSONL
	dummyScript := `#!/bin/bash
echo '{"type": "init", "session_id": "gemini-123"}'
echo '{"type": "message", "content": "Hello "}'
echo '{"type": "message", "content": "world!"}'
`
	tmpFile, err := os.CreateTemp("", "dummy-gemini-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(dummyScript); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	os.Chmod(tmpFile.Name(), 0755)

	storageDir, _ := os.MkdirTemp("", "tenazas-exec-test-*")
	defer os.RemoveAll(storageDir)

	exec := NewExecutor(tmpFile.Name(), storageDir)

	var capturedSID string
	var capturedChunks []string

	response, err := exec.Run("", "test prompt", ".", "", false, func(chunk string) {
		capturedChunks = append(capturedChunks, chunk)
	}, func(sid string) {
		capturedSID = sid
	})

	if err != nil {
		t.Fatalf("Executor.Run failed: %v", err)
	}

	if response != "Hello world!" {
		t.Errorf("expected 'Hello world!', got '%s'", response)
	}

	if capturedSID != "gemini-123" {
		t.Errorf("expected session ID 'gemini-123', got '%s'", capturedSID)
	}

	if len(capturedChunks) != 2 || capturedChunks[0] != "Hello " || capturedChunks[1] != "world!" {
		t.Errorf("unexpected chunks: %v", capturedChunks)
	}
}
