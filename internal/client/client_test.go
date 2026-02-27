package client

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// ---------------------------------------------------------------------------
// Registry tests
// ---------------------------------------------------------------------------

func TestRegisteredClients(t *testing.T) {
	names := RegisteredClients()
	sort.Strings(names)
	if len(names) != 2 {
		t.Fatalf("expected 2 registered clients, got %d: %v", len(names), names)
	}
	if names[0] != "claude-code" || names[1] != "gemini" {
		t.Fatalf("unexpected client names: %v", names)
	}
}

func TestNewClient_Gemini(t *testing.T) {
	c, err := NewClient("gemini", "/usr/bin/gemini", "/tmp/log")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Name() != "gemini" {
		t.Fatalf("expected name 'gemini', got %q", c.Name())
	}
	if _, ok := c.(*GeminiClient); !ok {
		t.Fatal("expected *GeminiClient type")
	}
}

func TestNewClient_ClaudeCode(t *testing.T) {
	c, err := NewClient("claude-code", "/usr/bin/claude", "/tmp/log")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Name() != "claude-code" {
		t.Fatalf("expected name 'claude-code', got %q", c.Name())
	}
	if _, ok := c.(*ClaudeCodeClient); !ok {
		t.Fatal("expected *ClaudeCodeClient type")
	}
}

func TestNewClient_Unknown(t *testing.T) {
	_, err := NewClient("unknown-agent", "", "")
	if err == nil {
		t.Fatal("expected error for unknown client")
	}
}

// ---------------------------------------------------------------------------
// GeminiClient tests
// ---------------------------------------------------------------------------

func TestGeminiClient_Name(t *testing.T) {
	g := &GeminiClient{}
	if g.Name() != "gemini" {
		t.Fatalf("expected 'gemini', got %q", g.Name())
	}
}

func TestGeminiClient_Run(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "fake_gemini.sh")
	logPath := filepath.Join(tmpDir, "test.log")

	script := `#!/bin/sh
echo '{"type": "init", "session_id": "sid-1"}'
echo '{"type": "message", "content": "Hello"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	g := &GeminiClient{binPath: scriptPath, logPath: logPath}

	var gotSID string
	var chunks []string

	full, err := g.Run("", "test prompt", tmpDir, "", false,
		func(chunk string) { chunks = append(chunks, chunk) },
		func(sid string) { gotSID = sid },
	)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if gotSID != "sid-1" {
		t.Fatalf("expected session_id 'sid-1', got %q", gotSID)
	}
	if len(chunks) != 1 || chunks[0] != "Hello" {
		t.Fatalf("unexpected chunks: %v", chunks)
	}
	if full != "Hello" {
		t.Fatalf("expected full response 'Hello', got %q", full)
	}
}

func TestGeminiClient_Run_WithResume(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Script that echoes its arguments so we can verify --resume is passed
	scriptPath := filepath.Join(tmpDir, "fake_gemini.sh")
	script := `#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = "--resume" ]; then FOUND_RESUME=1; fi
  if [ "$FOUND_RESUME" = "1" ] && [ "$arg" != "--resume" ]; then
    echo "{\"type\": \"message\", \"content\": \"resumed:$arg\"}"
    FOUND_RESUME=0
  fi
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	g := &GeminiClient{binPath: scriptPath, logPath: logPath}

	var chunks []string
	full, err := g.Run("existing-sid", "test", tmpDir, "", false,
		func(chunk string) { chunks = append(chunks, chunk) },
		func(sid string) {},
	)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if full != "resumed:existing-sid" {
		t.Fatalf("expected resume SID in output, got %q", full)
	}
}

func TestGeminiClient_Run_Yolo(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	// Script that checks for -y flag
	scriptPath := filepath.Join(tmpDir, "fake_gemini.sh")
	script := `#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = "-y" ]; then
    echo '{"type": "message", "content": "yolo"}'
    exit 0
  fi
done
echo '{"type": "message", "content": "no-yolo"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	g := &GeminiClient{binPath: scriptPath, logPath: logPath}

	full, err := g.Run("", "test", tmpDir, "", true,
		func(string) {}, func(string) {},
	)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if full != "yolo" {
		t.Fatalf("expected 'yolo', got %q", full)
	}
}

func TestGeminiClient_Run_ApprovalMode(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	scriptPath := filepath.Join(tmpDir, "fake_gemini.sh")
	script := `#!/bin/sh
for arg in "$@"; do
  if [ "$prev" = "--approval-mode" ]; then
    echo "{\"type\": \"message\", \"content\": \"mode:$arg\"}"
    exit 0
  fi
  prev="$arg"
done
echo '{"type": "message", "content": "no-mode"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	g := &GeminiClient{binPath: scriptPath, logPath: logPath}

	full, err := g.Run("", "test", tmpDir, "cautious", false,
		func(string) {}, func(string) {},
	)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if full != "mode:cautious" {
		t.Fatalf("expected 'mode:cautious', got %q", full)
	}
}

// ---------------------------------------------------------------------------
// ClaudeCodeClient tests
// ---------------------------------------------------------------------------

func TestClaudeCodeClient_Name(t *testing.T) {
	c := &ClaudeCodeClient{}
	if c.Name() != "claude-code" {
		t.Fatalf("expected 'claude-code', got %q", c.Name())
	}
}

func TestClaudeCodeClient_Run(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "fake_claude.sh")
	logPath := filepath.Join(tmpDir, "test.log")

	script := `#!/bin/sh
echo '{"type": "init", "session_id": "csid-1"}'
echo '{"type": "assistant", "content": "Hi"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	c := &ClaudeCodeClient{binPath: scriptPath, logPath: logPath}

	var gotSID string
	var chunks []string

	full, err := c.Run("", "test prompt", tmpDir, "", false,
		func(chunk string) { chunks = append(chunks, chunk) },
		func(sid string) { gotSID = sid },
	)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if gotSID != "csid-1" {
		t.Fatalf("expected session_id 'csid-1', got %q", gotSID)
	}
	if len(chunks) != 1 || chunks[0] != "Hi" {
		t.Fatalf("unexpected chunks: %v", chunks)
	}
	if full != "Hi" {
		t.Fatalf("expected full response 'Hi', got %q", full)
	}
}

func TestClaudeCodeClient_Run_WithContinue(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	scriptPath := filepath.Join(tmpDir, "fake_claude.sh")
	script := `#!/bin/sh
for arg in "$@"; do
  if [ "$prev" = "--continue" ]; then
    echo "{\"type\": \"assistant\", \"content\": \"continued:$arg\"}"
    exit 0
  fi
  prev="$arg"
done
echo '{"type": "assistant", "content": "fresh"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	c := &ClaudeCodeClient{binPath: scriptPath, logPath: logPath}

	full, err := c.Run("csid-existing", "test", tmpDir, "", false,
		func(string) {}, func(string) {},
	)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if full != "continued:csid-existing" {
		t.Fatalf("expected continue SID in output, got %q", full)
	}
}

func TestClaudeCodeClient_Run_Yolo(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	scriptPath := filepath.Join(tmpDir, "fake_claude.sh")
	script := `#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = "--dangerously-skip-permissions" ]; then
    echo '{"type": "assistant", "content": "yolo"}'
    exit 0
  fi
done
echo '{"type": "assistant", "content": "no-yolo"}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	c := &ClaudeCodeClient{binPath: scriptPath, logPath: logPath}

	full, err := c.Run("", "test", tmpDir, "", true,
		func(string) {}, func(string) {},
	)
	if err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if full != "yolo" {
		t.Fatalf("expected 'yolo', got %q", full)
	}
}
