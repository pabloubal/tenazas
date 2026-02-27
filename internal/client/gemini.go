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

func init() { Register("gemini", newGeminiClient) }

// GeminiClient drives the gemini CLI subprocess.
type GeminiClient struct {
	binPath string
	logPath string
	models  map[string]string // tier → model name
}

func newGeminiClient(binPath, logPath string) Client {
	return &GeminiClient{binPath: binPath, logPath: logPath}
}

func (g *GeminiClient) Name() string { return "gemini" }

func (g *GeminiClient) SetModels(m map[string]string) { g.models = m }

func (g *GeminiClient) Run(opts RunOptions, onChunk func(string), onSessionID func(string)) (string, error) {
	args := g.buildArgs(opts)

	cmd := exec.Command(g.binPath, args...)
	cmd.Dir = opts.CWD

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}

	g.logExecution(args, opts.Prompt)

	logFile, _ := os.OpenFile(g.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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
		case "message":
			if resp.Content != "" {
				fullResponse.WriteString(resp.Content)
				onChunk(resp.Content)
			}
		}
	}

	return fullResponse.String(), cmd.Wait()
}

func (g *GeminiClient) buildArgs(opts RunOptions) []string {
	args := []string{"-s", "--output-format", "stream-json", "--prompt", opts.Prompt}
	if opts.NativeSID != "" {
		args = append(args, "--resume", opts.NativeSID)
	}
	if opts.Yolo {
		args = append(args, "-y")
	} else if opts.ApprovalMode != "" {
		args = append(args, "--approval-mode", opts.ApprovalMode)
	}
	if model := g.resolveModel(opts.ModelTier); model != "" {
		args = append(args, "--model", model)
	}
	return args
}

func (g *GeminiClient) resolveModel(tier string) string {
	if tier == "" || len(g.models) == 0 {
		return ""
	}
	return g.models[tier]
}

func (g *GeminiClient) logExecution(args []string, prompt string) {
	logFile, _ := os.OpenFile(g.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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
	fmt.Fprintf(logFile, "\n[DEBUG] Executing: %s %s\n", g.binPath, strings.Join(displayArgs, " "))
}
