package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

type GeminiResponse struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	Delta     bool   `json:"delta"`
}

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

func (e *Executor) Run(sess *Session, prompt string, onChunk func(string), onSessionID func(string)) error {
	args := []string{"--output-format", "stream-json", "-p", prompt}
	if sess.GeminiSID != "" {
		args = append([]string{"--resume", sess.GeminiSID}, args...)
	}
	if sess.Yolo {
		args = append(args, "-y")
	}

	cmd := exec.Command(e.BinPath, args...)
	cmd.Dir = sess.CWD

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	// Logging stderr
	logFile, _ := os.OpenFile(e.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if logFile != nil {
		go io.Copy(logFile, stderr)
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var resp GeminiResponse
		line := scanner.Bytes()
		if err := json.Unmarshal(line, &resp); err != nil {
			continue
		}

		if resp.Type == "init" && resp.SessionID != "" {
			onSessionID(resp.SessionID)
		}

		if resp.Type == "message" && resp.Content != "" {
			// Even if it's not the "init" line, some gemini versions send SID in every line.
			if resp.SessionID != "" && sess.GeminiSID == "" {
				onSessionID(resp.SessionID)
			}
			onChunk(resp.Content)
		}
	}

	return cmd.Wait()
}
