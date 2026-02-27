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
	models  map[string]string // tier → model name
}

// approvalModeToPermission maps Tenazas approval modes to Claude permission modes.
var approvalModeToPermission = map[string]string{
	"PLAN":      "plan",
	"AUTO_EDIT": "acceptEdits",
	"YOLO":      "bypassPermissions",
}

func newClaudeCodeClient(binPath, logPath string) Client {
	return &ClaudeCodeClient{binPath: binPath, logPath: logPath}
}

func (c *ClaudeCodeClient) Name() string { return "claude-code" }

func (c *ClaudeCodeClient) SetModels(m map[string]string) { c.models = m }

func (c *ClaudeCodeClient) Run(opts RunOptions, onChunk func(string), onSessionID func(string)) (string, error) {
	args := c.buildArgs(opts)

	cmd := exec.Command(c.binPath, args...)
	cmd.Dir = opts.CWD

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}

	c.logExecution(args, opts.Prompt)

	logFile, _ := os.OpenFile(c.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if logFile != nil {
		defer logFile.Close()
		go io.Copy(logFile, stderr)
	}

	if err := cmd.Start(); err != nil {
		return "", err
	}

	var fullResponse bytes.Buffer
	var sidEmitted bool
	scanner := bufio.NewScanner(stdout)
	const maxCapacity = 10 * 1024 * 1024
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, maxCapacity)

	for scanner.Scan() {
		line := scanner.Bytes()
		if logFile != nil {
			logFile.Write(append(line, '\n'))
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}

		var eventType string
		json.Unmarshal(raw["type"], &eventType)

		var sessionID string
		if sid, ok := raw["session_id"]; ok {
			json.Unmarshal(sid, &sessionID)
		}
		if sessionID != "" && !sidEmitted {
			onSessionID(sessionID)
			sidEmitted = true
		}

		switch eventType {
		case "assistant":
			// Content is nested: message.content[].text
			if msgRaw, ok := raw["message"]; ok {
				var msg struct {
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				}
				if json.Unmarshal(msgRaw, &msg) == nil {
					for _, block := range msg.Content {
						if block.Type == "text" && block.Text != "" {
							fullResponse.WriteString(block.Text)
							onChunk(block.Text)
						}
					}
				}
			}
		case "content_block_delta":
			// Streaming delta: delta.text
			if deltaRaw, ok := raw["delta"]; ok {
				var delta struct {
					Text string `json:"text"`
				}
				if json.Unmarshal(deltaRaw, &delta) == nil && delta.Text != "" {
					fullResponse.WriteString(delta.Text)
					onChunk(delta.Text)
				}
			}
		case "result":
			// Final result text
			if resultRaw, ok := raw["result"]; ok {
				var result string
				if json.Unmarshal(resultRaw, &result) == nil && result != "" && fullResponse.Len() == 0 {
					fullResponse.WriteString(result)
					onChunk(result)
				}
			}
		}
	}

	return fullResponse.String(), cmd.Wait()
}

func (c *ClaudeCodeClient) buildArgs(opts RunOptions) []string {
	args := []string{"--output-format", "stream-json", "--verbose", "-p", opts.Prompt}
	if opts.NativeSID != "" {
		args = append(args, "--continue", opts.NativeSID)
	}
	if opts.Yolo {
		args = append(args, "--dangerously-skip-permissions")
	} else if opts.ApprovalMode != "" {
		if pm, ok := approvalModeToPermission[opts.ApprovalMode]; ok {
			args = append(args, "--permission-mode", pm)
		}
	}
	if model := c.resolveModel(opts.ModelTier); model != "" {
		args = append(args, "--model", model)
	}
	if opts.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", opts.MaxBudgetUSD))
	}
	return args
}

func (c *ClaudeCodeClient) resolveModel(tier string) string {
	if tier == "" || len(c.models) == 0 {
		return ""
	}
	return c.models[tier]
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
