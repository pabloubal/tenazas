package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Executor struct {
	BinPath string
	LogPath string
}

func NewExecutor(binPath, storageDir string) *Executor {
	return &Executor{
		BinPath: binPath,
		LogPath: filepath.Join(storageDir, "tenazas.log"),
	}
}

func (e *Executor) Run(geminiSID string, prompt string, cwd string, approvalMode string, yolo bool, onChunk func(string), onSessionID func(string)) (string, error) {
	// We pass the prompt directly to the --prompt flag.
	// This ensures non-interactive mode and is simpler than piping.
	args := []string{"-s", "--output-format", "stream-json", "--prompt", prompt}
	
	if geminiSID != "" {
		args = append(args, "--resume", geminiSID)
	}
	if approvalMode != "" {
		args = append(args, "--approval-mode", approvalMode)
	}
	if yolo {
		args = append(args, "-y")
	}

	cmd := exec.Command(e.BinPath, args...)
	cmd.Dir = cwd
	// Stdin is not needed since we use --prompt
	cmd.Stdin = nil

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}

	// Logging stderr and stdout to tenazas.log
	logFile, _ := os.OpenFile(e.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)

	// Log the exact command being run
	if logFile != nil {
		displayPrompt := prompt
		if len(displayPrompt) > 100 {
			displayPrompt = displayPrompt[:100] + "..."
		}
		// Create a version of args with the truncated prompt for the log
		displayArgs := make([]string, len(args))
		copy(displayArgs, args)
		for i, arg := range displayArgs {
			if arg == prompt {
				displayArgs[i] = displayPrompt
			}
		}
		cmdStr := fmt.Sprintf("\x0a[DEBUG] Executing: %s %s\x0a", e.BinPath, strings.Join(displayArgs, " "))
		logFile.WriteString(cmdStr)
	}

	if logFile != nil {
		go io.Copy(logFile, stderr)
	}

	if err := cmd.Start(); err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(stdout)
	const maxCapacity = 10 * 1024 * 1024 // 10MB
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, maxCapacity)

	var fullResponse bytes.Buffer

	for scanner.Scan() {
		line := scanner.Bytes()

		// Log raw JSONL line for debugging
		if logFile != nil {
			logFile.Write(append(line, '\n'))
		}

		var resp struct {
			Type      string `json:"type"`
			SessionID string `json:"session_id"`
			Content   string `json:"content"`
		}
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}

		if resp.Type == "init" && resp.SessionID != "" {
			onSessionID(resp.SessionID)
		}
		if resp.Type == "message" && resp.Content != "" {
			fullResponse.WriteString(resp.Content)
			onChunk(resp.Content)
		}
	}

	return fullResponse.String(), cmd.Wait()
}
