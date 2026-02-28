package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// mockACPServer simulates a copilot --acp process using a simple script.
// It reads JSON-RPC requests from stdin and writes responses to stdout.
func startMockACP(t *testing.T, handler func(msg jsonRPCMessage, write func(jsonRPCMessage))) (*exec.Cmd, io.WriteCloser, *bufio.Scanner) {
	t.Helper()

	// Use a unix socket pair to avoid needing a real subprocess.
	serverConn, clientConn := net.Pipe()

	// Server-side: read requests, dispatch to handler.
	go func() {
		scanner := bufio.NewScanner(serverConn)
		scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
		writeFn := func(msg jsonRPCMessage) {
			data, _ := json.Marshal(msg)
			serverConn.Write(append(data, '\n'))
		}
		for scanner.Scan() {
			var msg jsonRPCMessage
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}
			handler(msg, writeFn)
		}
	}()

	t.Cleanup(func() {
		serverConn.Close()
		clientConn.Close()
	})

	clientScanner := bufio.NewScanner(clientConn)
	clientScanner.Buffer(make([]byte, 64*1024), 10*1024*1024)

	return nil, clientConn, clientScanner
}

// TestCopilotClient_JSONRPCFormat verifies the JSON-RPC message structure.
func TestCopilotClient_JSONRPCFormat(t *testing.T) {
	var id int64 = 42
	msg := jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  "session/new",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}

	var parsed jsonRPCMessage
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed.JSONRPC != "2.0" {
		t.Errorf("want jsonrpc 2.0, got %s", parsed.JSONRPC)
	}
	if parsed.ID == nil || *parsed.ID != 42 {
		t.Errorf("want id 42, got %v", parsed.ID)
	}
	if parsed.Method != "session/new" {
		t.Errorf("want method session/new, got %s", parsed.Method)
	}
}

// TestCopilotClient_ModeMapping verifies approval mode → ACP mode mapping.
func TestCopilotClient_ModeMapping(t *testing.T) {
	c := &CopilotClient{}

	tests := []struct {
		opts RunOptions
		want string
	}{
		{RunOptions{Yolo: true}, "https://agentclientprotocol.com/protocol/session-modes#autopilot"},
		{RunOptions{ApprovalMode: "PLAN"}, "https://agentclientprotocol.com/protocol/session-modes#plan"},
		{RunOptions{ApprovalMode: "AUTO_EDIT"}, "https://agentclientprotocol.com/protocol/session-modes#agent"},
		{RunOptions{}, ""},
	}

	for _, tc := range tests {
		got := c.mapMode(tc.opts)
		if got != tc.want {
			t.Errorf("mapMode(%+v) = %q, want %q", tc.opts, got, tc.want)
		}
	}
}

// TestCopilotClient_ModelResolve verifies tier → model ID resolution.
func TestCopilotClient_ModelResolve(t *testing.T) {
	c := &CopilotClient{
		models: map[string]string{
			"high":   "claude-opus-4.6",
			"medium": "claude-sonnet-4.6",
			"low":    "claude-haiku-4.5",
		},
	}

	tests := []struct {
		tier string
		want string
	}{
		{"high", "claude-opus-4.6"},
		{"medium", "claude-sonnet-4.6"},
		{"low", "claude-haiku-4.5"},
		{"", ""},
	}

	for _, tc := range tests {
		got := c.ResolveModel(tc.tier)
		if got != tc.want {
			t.Errorf("resolveModel(%q) = %q, want %q", tc.tier, got, tc.want)
		}
	}
}

// TestCopilotClient_NotificationParsing verifies agent_message_chunk handling.
func TestCopilotClient_NotificationParsing(t *testing.T) {
	c := &CopilotClient{}

	var chunks []string
	cbs := &acpCallbacks{
		onChunk: func(text string) {
			chunks = append(chunks, text)
		},
	}
	c.callbacks.Store("test-session", cbs)

	// Simulate an agent_message_chunk notification.
	params, _ := json.Marshal(map[string]any{
		"sessionId": "test-session",
		"update": map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content": map[string]any{
				"type": "text",
				"text": "Hello",
			},
		},
	})

	msg := &jsonRPCMessage{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params:  params,
	}

	c.handleNotification(msg)

	if len(chunks) != 1 || chunks[0] != "Hello" {
		t.Errorf("expected [Hello], got %v", chunks)
	}
}

// TestCopilotClient_ThoughtNotification verifies agent_thought_chunk handling.
func TestCopilotClient_ThoughtNotification(t *testing.T) {
	c := &CopilotClient{}

	var thoughts []string
	cbs := &acpCallbacks{
		onThought: func(text string) {
			thoughts = append(thoughts, text)
		},
	}
	c.callbacks.Store("test-session", cbs)

	params, _ := json.Marshal(map[string]any{
		"sessionId": "test-session",
		"update": map[string]any{
			"sessionUpdate": "agent_thought_chunk",
			"content": map[string]any{
				"type": "text",
				"text": "Thinking...",
			},
		},
	})

	msg := &jsonRPCMessage{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params:  params,
	}

	c.handleNotification(msg)

	if len(thoughts) != 1 || thoughts[0] != "Thinking..." {
		t.Errorf("expected [Thinking...], got %v", thoughts)
	}
}

// TestCopilotClient_IgnoresUnknownNotifications verifies unknown update types are ignored.
func TestCopilotClient_IgnoresUnknownNotifications(t *testing.T) {
	c := &CopilotClient{}

	var chunks []string
	cbs := &acpCallbacks{
		onChunk: func(text string) {
			chunks = append(chunks, text)
		},
	}
	c.callbacks.Store("test-session", cbs)

	params, _ := json.Marshal(map[string]any{
		"sessionId": "test-session",
		"update": map[string]any{
			"sessionUpdate": "tool_call",
			"toolCallId":    "abc",
			"title":         "Running ls",
		},
	})

	msg := &jsonRPCMessage{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params:  params,
	}

	c.handleNotification(msg)

	if len(chunks) != 0 {
		t.Errorf("expected no chunks for tool_call, got %v", chunks)
	}
}

// TestCopilotClient_IgnoresUnknownSession verifies notifications for unknown sessions are ignored.
func TestCopilotClient_IgnoresUnknownSession(t *testing.T) {
	c := &CopilotClient{}
	// No callbacks registered.

	params, _ := json.Marshal(map[string]any{
		"sessionId": "unknown-session",
		"update": map[string]any{
			"sessionUpdate": "agent_message_chunk",
			"content":       map[string]any{"type": "text", "text": "Hello"},
		},
	})

	msg := &jsonRPCMessage{
		JSONRPC: "2.0",
		Method:  "session/update",
		Params:  params,
	}

	// Should not panic.
	c.handleNotification(msg)
}

// TestCopilotClient_Name verifies the client identifier.
func TestCopilotClient_Name(t *testing.T) {
	c := &CopilotClient{}
	if c.Name() != "copilot" {
		t.Errorf("want copilot, got %s", c.Name())
	}
}

// TestCopilotClient_Registration verifies the client is registered in the global registry.
func TestCopilotClient_Registration(t *testing.T) {
	names := RegisteredClients()
	found := false
	for _, n := range names {
		if n == "copilot" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("copilot not found in registered clients: %v", names)
	}
}

// TestCopilotClient_EndToEnd_MockProcess tests the full flow with a mock ACP subprocess.
func TestCopilotClient_EndToEnd_MockProcess(t *testing.T) {
	// Create a mock ACP server script.
	tmpDir := t.TempDir()
	scriptPath := tmpDir + "/mock_acp.sh"
	logPath := tmpDir + "/test.log"

	// The script reads JSON-RPC requests line by line and responds.
	script := `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | python3 -c "import json,sys; print(json.load(sys.stdin).get('method',''))" 2>/dev/null)
    id=$(echo "$line" | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))" 2>/dev/null)

    case "$method" in
        initialize)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"protocolVersion\":1,\"agentInfo\":{\"name\":\"MockCopilot\",\"version\":\"0.0.1\"}}}"
            ;;
        session/new)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"sessionId\":\"mock-session-123\",\"models\":{\"currentModelId\":\"test-model\"},\"modes\":{\"currentModeId\":\"agent\"}}}"
            ;;
        session/set_mode)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{}}"
            ;;
        session/set_model)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{}}"
            ;;
        session/prompt)
            # Emit streaming chunks as notifications.
            echo "{\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{\"sessionId\":\"mock-session-123\",\"update\":{\"sessionUpdate\":\"agent_message_chunk\",\"content\":{\"type\":\"text\",\"text\":\"Hello \"}}}}"
            echo "{\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{\"sessionId\":\"mock-session-123\",\"update\":{\"sessionUpdate\":\"agent_message_chunk\",\"content\":{\"type\":\"text\",\"text\":\"World\"}}}}"
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"stopReason\":\"end_turn\"}}"
            ;;
        session/load)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"models\":{\"currentModelId\":\"test-model\"},\"modes\":{\"currentModeId\":\"agent\"}}}"
            ;;
        *)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"error\":{\"code\":-32601,\"message\":\"Method not found\"}}"
            ;;
    esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	// Check python3 is available (needed by mock script).
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found, skipping end-to-end test")
	}

	c := &CopilotClient{
		binPath: scriptPath,
		logPath: logPath,
		models:  map[string]string{"high": "test-opus", "medium": "test-sonnet"},
	}

	var chunks []string
	var sessionID string
	fullResp, err := c.Run(RunOptions{
		Prompt:       "test prompt",
		CWD:          tmpDir,
		ApprovalMode: "PLAN",
		ModelTier:    "high",
	}, func(chunk string) {
		chunks = append(chunks, chunk)
	}, func(sid string) {
		sessionID = sid
	})

	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if sessionID != "mock-session-123" {
		t.Errorf("want session mock-session-123, got %s", sessionID)
	}

	if fullResp != "Hello World" {
		t.Errorf("want full response 'Hello World', got %q", fullResp)
	}

	if len(chunks) != 2 || chunks[0] != "Hello " || chunks[1] != "World" {
		t.Errorf("want chunks [Hello , World], got %v", chunks)
	}
}

// TestCopilotClient_SessionResume tests the session/load flow.
func TestCopilotClient_SessionResume(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := tmpDir + "/mock_acp.sh"
	logPath := tmpDir + "/test.log"

	// Track which methods were called.
	script := `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | python3 -c "import json,sys; print(json.load(sys.stdin).get('method',''))" 2>/dev/null)
    id=$(echo "$line" | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))" 2>/dev/null)
    params=$(echo "$line" | python3 -c "import json,sys; print(json.dumps(json.load(sys.stdin).get('params',{})))" 2>/dev/null)

    case "$method" in
        initialize)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"protocolVersion\":1,\"agentInfo\":{\"name\":\"MockCopilot\"}}}"
            ;;
        session/load)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"models\":{\"currentModelId\":\"test-model\"}}}"
            ;;
        session/prompt)
            echo "{\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{\"sessionId\":\"existing-session\",\"update\":{\"sessionUpdate\":\"agent_message_chunk\",\"content\":{\"type\":\"text\",\"text\":\"Resumed!\"}}}}"
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"stopReason\":\"end_turn\"}}"
            ;;
        session/set_mode|session/set_model)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{}}"
            ;;
        *)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{}}"
            ;;
    esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found")
	}

	c := &CopilotClient{
		binPath: scriptPath,
		logPath: logPath,
	}

	var sid string
	fullResp, err := c.Run(RunOptions{
		Prompt:    "continue",
		CWD:       tmpDir,
		NativeSID: "existing-session",
	}, func(chunk string) {}, func(s string) { sid = s })

	if err != nil {
		t.Fatalf("Run with resume failed: %v", err)
	}

	if sid != "existing-session" {
		t.Errorf("want session existing-session, got %s", sid)
	}

	if fullResp != "Resumed!" {
		t.Errorf("want 'Resumed!', got %q", fullResp)
	}
}

// TestCopilotClient_ProcessReuse verifies the ACP process is reused across calls.
func TestCopilotClient_ProcessReuse(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := tmpDir + "/mock_acp.sh"
	logPath := tmpDir + "/test.log"

	// Counter file tracks how many times the script starts.
	counterFile := tmpDir + "/counter"
	os.WriteFile(counterFile, []byte("0"), 0644)

	script := fmt.Sprintf(`#!/bin/bash
# Increment counter
count=$(cat %s)
echo $((count + 1)) > %s

while IFS= read -r line; do
    method=$(echo "$line" | python3 -c "import json,sys; print(json.load(sys.stdin).get('method',''))" 2>/dev/null)
    id=$(echo "$line" | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))" 2>/dev/null)

    case "$method" in
        initialize)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"protocolVersion\":1}}"
            ;;
        session/new)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"sessionId\":\"s1\"}}"
            ;;
        session/prompt)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"stopReason\":\"end_turn\"}}"
            ;;
        *)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{}}"
            ;;
    esac
done
`, counterFile, counterFile)

	os.WriteFile(scriptPath, []byte(script), 0755)
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found")
	}

	c := &CopilotClient{binPath: scriptPath, logPath: logPath}

	// First call.
	_, err := c.Run(RunOptions{Prompt: "first", CWD: tmpDir}, func(string) {}, func(string) {})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Give time for the reader to settle.
	time.Sleep(100 * time.Millisecond)

	// Second call — should reuse the process.
	_, err = c.Run(RunOptions{Prompt: "second", CWD: tmpDir}, func(string) {}, func(string) {})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	data, _ := os.ReadFile(counterFile)
	count := strings.TrimSpace(string(data))
	if count != "1" {
		t.Errorf("ACP process started %s times, want 1 (process should be reused)", count)
	}
}

// TestCopilotClient_PermissionAutoApprove verifies that server-initiated
// session/request_permission requests are automatically approved.
func TestCopilotClient_PermissionAutoApprove(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := tmpDir + "/mock_acp.sh"
	logPath := tmpDir + "/test.log"

	// The mock sends a permission request during prompt, reads the approval,
	// then completes with the tool output.
	script := `#!/bin/bash
while IFS= read -r line; do
    method=$(echo "$line" | python3 -c "import json,sys; print(json.load(sys.stdin).get('method',''))" 2>/dev/null)
    id=$(echo "$line" | python3 -c "import json,sys; print(json.load(sys.stdin).get('id',''))" 2>/dev/null)

    case "$method" in
        initialize)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"protocolVersion\":1}}"
            ;;
        session/new)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"sessionId\":\"perm-session\"}}"
            ;;
        session/prompt)
            # Send a tool call notification.
            echo "{\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{\"sessionId\":\"perm-session\",\"update\":{\"sessionUpdate\":\"tool_call\",\"toolCallId\":\"tc1\",\"title\":\"List files\",\"kind\":\"execute\",\"status\":\"pending\"}}}"
            # Send a permission request (server-initiated, id=0).
            echo "{\"jsonrpc\":\"2.0\",\"id\":0,\"method\":\"session/request_permission\",\"params\":{\"sessionId\":\"perm-session\",\"toolCall\":{\"toolCallId\":\"tc1\",\"title\":\"List files\"},\"options\":[{\"optionId\":\"allow_once\",\"kind\":\"allow_once\"},{\"optionId\":\"allow_always\",\"kind\":\"allow_always\"},{\"optionId\":\"reject_once\",\"kind\":\"reject_once\"}]}}"
            # Read the approval response from client.
            IFS= read -r approval
            option=$(echo "$approval" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('result',{}).get('outcome',{}).get('optionId',''))" 2>/dev/null)
            # Send the result based on approval.
            echo "{\"jsonrpc\":\"2.0\",\"method\":\"session/update\",\"params\":{\"sessionId\":\"perm-session\",\"update\":{\"sessionUpdate\":\"agent_message_chunk\",\"content\":{\"type\":\"text\",\"text\":\"approved:$option\"}}}}"
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"stopReason\":\"end_turn\"}}"
            ;;
        *)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{}}"
            ;;
    esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found")
	}

	c := &CopilotClient{binPath: scriptPath, logPath: logPath}

	var chunks []string
	fullResp, err := c.Run(RunOptions{
		Prompt: "list files",
		CWD:    tmpDir,
	}, func(chunk string) {
		chunks = append(chunks, chunk)
	}, func(string) {})

	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if fullResp != "approved:allow_always" {
		t.Errorf("want 'approved:allow_always', got %q", fullResp)
	}
}
