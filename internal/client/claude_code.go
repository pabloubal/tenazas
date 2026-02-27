package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func init() { Register("claude-code", newClaudeCodeClient) }

// ClaudeCodeClient drives the claude CLI subprocess.
type ClaudeCodeClient struct {
	binPath string
	logPath string
}

func newClaudeCodeClient(binPath, logPath string) Client {
	return &ClaudeCodeClient{binPath: binPath, logPath: logPath}
}

func (c *ClaudeCodeClient) Name() string { return "claude-code" }

func (c *ClaudeCodeClient) Run(nativeSID, prompt, cwd, approvalMode string, yolo bool,
	onChunk func(string), onSessionID func(string)) (string, error) {

	args := c.buildArgs(nativeSID, prompt, yolo)

	cmd := exec.Command(c.binPath, args...)
	cmd.Dir = cwd

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}

	c.logExecution(args, prompt)

	logFile, _ := os.OpenFile(c.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if logFile != nil {
		defer logFile.Close()
		go io.Copy(logFile, stderr)
	}

	if err := cmd.Start(); err != nil {
		return "", err
	}

	var fullResponse bytes.Buffer
	scanner := bufio.NewScanner(stdout)
	const maxCapacity = 10 * 1024 * 1024
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
		case "assistant":
			if resp.Content != "" {
				fullResponse.WriteString(resp.Content)
				onChunk(resp.Content)
			}
		}
	}

	return fullResponse.String(), cmd.Wait()
}

func (c *ClaudeCodeClient) buildArgs(nativeSID, prompt string, yolo bool) []string {
	args := []string{"--output-format", "stream-json", "-p", prompt}
	if nativeSID != "" {
		args = append(args, "--continue", nativeSID)
	}
	if yolo {
		args = append(args, "--dangerously-skip-permissions")
	}
	return args
}

func (c *ClaudeCodeClient) logExecution(args []string, prompt string) {
	logFile, _ := os.OpenFile(c.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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
	fmt.Fprintf(logFile, "\n[DEBUG] Executing: %s %s\n", c.binPath, strings.Join(displayArgs, " "))
}
