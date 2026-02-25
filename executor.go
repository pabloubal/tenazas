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
	args := e.buildArgs(geminiSID, prompt, approvalMode, yolo)
	
	cmd := exec.Command(e.BinPath, args...)
	cmd.Dir = cwd

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}

	e.logExecution(args, prompt)

	logFile, _ := os.OpenFile(e.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if logFile != nil {
		defer logFile.Close()
		go io.Copy(logFile, stderr)
	}

	if err := cmd.Start(); err != nil {
		return "", err
	}

	var fullResponse bytes.Buffer
	scanner := bufio.NewScanner(stdout)
	const maxCapacity = 10 * 1024 * 1024 // 10MB
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, maxCapacity)

	for scanner.Scan() {
		line := scanner.Bytes()
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

		switch resp.Type {
		case "init":
			if resp.SessionID != "" {
				onSessionID(resp.SessionID)
			}
		case "message":
			if resp.Content != "" {
				fullResponse.WriteString(resp.Content)
				onChunk(resp.Content)
			}
		}
	}

	return fullResponse.String(), cmd.Wait()
}

func (e *Executor) buildArgs(geminiSID, prompt, approvalMode string, yolo bool) []string {
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
	return args
}

func (e *Executor) logExecution(args []string, prompt string) {
	logFile, _ := os.OpenFile(e.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if logFile == nil {
		return
	}
	defer logFile.Close()

	displayPrompt := prompt
	if len(displayPrompt) > 100 {
		displayPrompt = displayPrompt[:100] + "..."
	}

	displayArgs := make([]string, len(args))
	copy(displayArgs, args)
	for i, arg := range displayArgs {
		if arg == prompt {
			displayArgs[i] = displayPrompt
		}
	}
	fmt.Fprintf(logFile, "\x0a[DEBUG] Executing: %s %s\x0a", e.BinPath, strings.Join(displayArgs, " "))
}
