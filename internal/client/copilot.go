package client

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

func init() { Register("copilot", newCopilotClient) }

// CopilotClient drives the copilot CLI via the ACP (Agent Client Protocol).
// Unlike Gemini/Claude which spawn one process per prompt, Copilot uses a
// long-lived JSON-RPC 2.0 process over stdio.
type CopilotClient struct {
	binPath string
	logPath string
	models  map[string]string // tier → model ID

	mu      sync.Mutex // protects process lifecycle (ensureProcess)
	writeMu sync.Mutex // protects stdin writes
	proc    *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	logFile *os.File
	nextID  atomic.Int64
	initDone bool

	// callbacks holds per-request notification handlers, keyed by session ID.
	callbacks      sync.Map // sessionID → *acpCallbacks
	responses      sync.Map // id (int64) → chan *jsonRPCMessage
	readerDone     chan struct{}
	stderrBuf      stderrRing
	loadedSessions sync.Map // sessionID → struct{}
}

// acpCallbacks holds the streaming callbacks for an active prompt.
type acpCallbacks struct {
	onChunk      func(string)
	onSessionID  func(string)
	onThought    func(string)
	onToolEvent  func(name, status, detail string)
	onIntent     func(string)
	onPermission func(PermissionRequest) PermissionResponse
}

// jsonRPCMessage is the wire format for JSON-RPC 2.0 messages.
type jsonRPCMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *int64           `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *jsonRPCError    `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func newCopilotClient(binPath, logPath string) Client {
	return &CopilotClient{binPath: binPath, logPath: logPath}
}

func (c *CopilotClient) Name() string { return "copilot" }

func (c *CopilotClient) SetModels(m map[string]string) { c.models = m }

func (c *CopilotClient) Run(opts RunOptions, onChunk func(string), onSessionID func(string)) (string, error) {
	if err := c.ensureProcess(opts); err != nil {
		return "", fmt.Errorf("copilot acp: %w", err)
	}

	// Resolve or create a session.
	sessionID, err := c.resolveSession(opts)
	if err != nil {
		return "", fmt.Errorf("copilot session: %w", err)
	}
	onSessionID(sessionID)

	// Set mode if specified.
	if mode := c.mapMode(opts); mode != "" {
		c.call("session/set_mode", map[string]any{
			"sessionId": sessionID,
			"modeId":    mode,
		})
	}

	// Set model if specified.
	if model := c.ResolveModel(opts.ModelTier); model != "" {
		c.call("session/set_model", map[string]any{
			"sessionId": sessionID,
			"modelId":   model,
		})
	}

	// Register callbacks for streaming.
	var fullResponse strings.Builder
	cbs := &acpCallbacks{
		onChunk: func(text string) {
			fullResponse.WriteString(text)
			onChunk(text)
		},
		onSessionID:  onSessionID,
		onThought:    opts.OnThought,
		onToolEvent:  opts.OnToolEvent,
		onIntent:     opts.OnIntent,
		onPermission: opts.OnPermission,
	}
	c.callbacks.Store(sessionID, cbs)
	defer c.callbacks.Delete(sessionID)

	// Send the prompt.
	result, err := c.call("session/prompt", map[string]any{
		"sessionId": sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": opts.Prompt}},
	})
	if err != nil {
		return fullResponse.String(), err
	}

	c.log("[ACP] prompt complete: %s\n", string(result))
	return fullResponse.String(), nil
}

// ensureProcess starts the copilot --acp subprocess if not already running.
func (c *CopilotClient) ensureProcess(opts RunOptions) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.proc != nil && c.initDone {
		return nil
	}

	cmd := exec.Command(c.binPath, "--acp")
	if opts.CWD != "" {
		cmd.Dir = opts.CWD
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	c.stderrBuf = stderrRing{max: 2048}
	var stderrWriters []io.Writer
	stderrWriters = append(stderrWriters, &c.stderrBuf)
	logFile, _ := os.OpenFile(c.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if logFile != nil {
		stderrWriters = append(stderrWriters, logFile)
	}
	go io.Copy(io.MultiWriter(stderrWriters...), stderr)

	if err := cmd.Start(); err != nil {
		return err
	}

	c.proc = cmd
	c.stdin = stdin
	c.logFile = logFile
	c.scanner = bufio.NewScanner(stdout)
	c.scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)
	c.readerDone = make(chan struct{})

	// Start background reader.
	go c.readLoop()

	// Initialize the ACP connection (mu is held so no concurrent ensureProcess,
	// but sendAndWait only takes writeMu which is free).
	result, err := c.sendAndWait("initialize", map[string]any{
		"protocolVersion": 1,
	})
	if err != nil {
		cmd.Process.Kill()
		c.proc = nil
		c.initDone = false
		return fmt.Errorf("acp initialize: %w", err)
	}
	c.initDone = true
	c.log("[ACP] initialized: %s\n", string(result))
	return nil
}

// readLoop runs in a goroutine, dispatching JSON-RPC responses and notifications.
func (c *CopilotClient) readLoop() {
	defer close(c.readerDone)
	for c.scanner.Scan() {
		line := c.scanner.Bytes()
		c.log("[ACP] ← %s\n", string(line))

		var msg jsonRPCMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		if msg.ID != nil && msg.Method != "" {
			// Server-initiated request (e.g., session/request_permission).
			c.handleServerRequest(&msg)
		} else if msg.ID != nil {
			// Response to a client request.
			if ch, ok := c.responses.Load(*msg.ID); ok {
				ch.(chan *jsonRPCMessage) <- &msg
			}
		} else if msg.Method != "" {
			// Notification (no id).
			c.handleNotification(&msg)
		}
	}
}

// handleServerRequest responds to server-initiated JSON-RPC requests.
// The primary case is session/request_permission: the ACP agent asks the
// client to approve or deny a tool call.
func (c *CopilotClient) handleServerRequest(msg *jsonRPCMessage) {
	c.log("[ACP] server request: method=%s id=%d\n", msg.Method, *msg.ID)

	switch msg.Method {
	case "session/request_permission":
		var params struct {
			SessionID string `json:"sessionId"`
			ToolCall  struct {
				ToolCallID string `json:"toolCallId"`
				Title      string `json:"title"`
				Kind       string `json:"kind"`
				RawInput   struct {
					Command string `json:"command"`
				} `json:"rawInput"`
			} `json:"toolCall"`
			Options []struct {
				OptionID string `json:"optionId"`
				Name     string `json:"name"`
				Kind     string `json:"kind"`
			} `json:"options"`
		}
		json.Unmarshal(msg.Params, &params)

		// Check if a per-session OnPermission callback is registered.
		var onPerm func(PermissionRequest) PermissionResponse
		if cbsVal, ok := c.callbacks.Load(params.SessionID); ok {
			onPerm = cbsVal.(*acpCallbacks).onPermission
		}

		var optionID string
		if onPerm != nil {
			// Build the request for the interactive callback.
			req := PermissionRequest{
				ToolCallID: params.ToolCall.ToolCallID,
				Title:      params.ToolCall.Title,
				Kind:       params.ToolCall.Kind,
				Command:    params.ToolCall.RawInput.Command,
			}
			for _, o := range params.Options {
				req.Options = append(req.Options, PermissionOption{
					OptionID: o.OptionID,
					Name:     o.Name,
					Kind:     o.Kind,
				})
			}
			resp := onPerm(req)
			optionID = resp.OptionID
		} else {
			// No callback — auto-approve (YOLO mode).
			optionID = autoApproveOption(params.Options)
		}

		if optionID == "" {
			// Fallback: send cancellation outcome if no valid option selected
			c.respondToServer(*msg.ID, map[string]any{
				"outcome": map[string]any{
					"outcome": "cancelled",
				},
			})
		} else {
			c.respondToServer(*msg.ID, map[string]any{
				"outcome": map[string]any{
					"outcome":  "selected",
					"optionId": optionID,
				},
			})
		}
	default:
		c.respondToServer(*msg.ID, map[string]any{})
	}
}

// autoApproveOption picks the best auto-approve option from the list.
func autoApproveOption(options []struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}) string {
	id := ""
	for _, o := range options {
		if o.Kind == "allow_always" {
			return o.OptionID
		}
		if o.Kind == "allow_once" && id == "" {
			id = o.OptionID
		}
	}
	if id == "" && len(options) > 0 {
		id = options[0].OptionID
	}
	return id
}

// respondToServer writes a JSON-RPC response to a server-initiated request.
func (c *CopilotClient) respondToServer(id int64, result any) {
	resultJSON, _ := json.Marshal(result)
	resp := jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      &id,
		Result:  resultJSON,
	}
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	c.log("[ACP] → %s\n", string(data))

	c.writeMu.Lock()
	c.stdin.Write(data)
	c.writeMu.Unlock()
}

// handleNotification processes ACP notifications (session/update events).
func (c *CopilotClient) handleNotification(msg *jsonRPCMessage) {
	if msg.Method != "session/update" {
		return
	}

	var params struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			ToolCallID string `json:"toolCallId"`
			Title      string `json:"title"`
			Status     string `json:"status"`
		} `json:"update"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return
	}

	cbsVal, ok := c.callbacks.Load(params.SessionID)
	if !ok {
		return
	}
	cbs := cbsVal.(*acpCallbacks)

	switch params.Update.SessionUpdate {
	case "agent_message_chunk":
		if cbs.onChunk != nil && params.Update.Content.Text != "" {
			cbs.onChunk(params.Update.Content.Text)
		}
	case "agent_thought_chunk":
		if cbs.onThought != nil && params.Update.Content.Text != "" {
			cbs.onThought(params.Update.Content.Text)
		}
	case "tool_call":
		if cbs.onToolEvent != nil {
			cbs.onToolEvent(params.Update.Title, params.Update.Status, "")
		}
		if cbs.onIntent != nil && params.Update.Title != "" {
			cbs.onIntent(params.Update.Title)
		}
	case "tool_call_update":
		if cbs.onToolEvent != nil {
			cbs.onToolEvent(params.Update.Title, params.Update.Status, "")
		}
	}
}

// resolveSession creates a new session or loads an existing one.
func (c *CopilotClient) resolveSession(opts RunOptions) (string, error) {
	cwd := opts.CWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	params := map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	}

	if opts.NativeSID != "" {
		// Skip if already loaded in this ACP process.
		if _, loaded := c.loadedSessions.Load(opts.NativeSID); loaded {
			return opts.NativeSID, nil
		}
		// Resume existing session.
		params["sessionId"] = opts.NativeSID
		result, err := c.call("session/load", params)
		if err != nil {
			return "", fmt.Errorf("session/load: %w", err)
		}
		_ = result
		c.loadedSessions.Store(opts.NativeSID, struct{}{})
		return opts.NativeSID, nil
	}

	// Create new session.
	result, err := c.call("session/new", params)
	if err != nil {
		return "", fmt.Errorf("session/new: %w", err)
	}

	var res struct {
		SessionID string `json:"sessionId"`
	}
	json.Unmarshal(result, &res)
	c.loadedSessions.Store(res.SessionID, struct{}{})
	return res.SessionID, nil
}

// mapMode converts Tenazas approval modes to ACP mode URIs.
func (c *CopilotClient) mapMode(opts RunOptions) string {
	if opts.Yolo {
		return "https://agentclientprotocol.com/protocol/session-modes#autopilot"
	}
	switch opts.ApprovalMode {
	case "PLAN":
		return "https://agentclientprotocol.com/protocol/session-modes#plan"
	case "AUTO_EDIT":
		return "https://agentclientprotocol.com/protocol/session-modes#agent"
	default:
		return ""
	}
}

func (c *CopilotClient) ResolveModel(tier string) string {
	if tier == "" || len(c.models) == 0 {
		return ""
	}
	return c.models[tier]
}

// call sends a JSON-RPC request and waits for the response.
func (c *CopilotClient) call(method string, params any) (json.RawMessage, error) {
	return c.sendAndWait(method, params)
}

// sendAndWait marshals a JSON-RPC request, writes it under writeMu, and blocks
// until the readLoop dispatches the matching response.
func (c *CopilotClient) sendAndWait(method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan *jsonRPCMessage, 1)
	c.responses.Store(id, ch)
	defer c.responses.Delete(id)

	paramsJSON, _ := json.Marshal(params)
	msg := jsonRPCMessage{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  paramsJSON,
	}
	data, _ := json.Marshal(msg)
	data = append(data, '\n')

	c.log("[ACP] → %s\n", string(data))
	c.writeMu.Lock()
	_, err := c.stdin.Write(data)
	c.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("write to acp: %w", err)
	}

	// Wait for response (or process exit).
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("acp %s: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	case <-c.readerDone:
		hint := c.stderrBuf.String()
		if len(hint) > 256 {
			hint = hint[:256]
		}
		if hint != "" {
			return nil, fmt.Errorf("acp process exited: %s", strings.TrimSpace(hint))
		}
		return nil, fmt.Errorf("acp process exited")
	}
}

// CancelSession sends a session/cancel RPC to abort the active prompt.
func (c *CopilotClient) CancelSession(sessionID string) error {
	_, err := c.call("session/cancel", map[string]any{
		"sessionId": sessionID,
	})
	return err
}

func (c *CopilotClient) log(format string, args ...any) {
	if c.logFile != nil {
		fmt.Fprintf(c.logFile, format, args...)
	}
}

// stderrRing captures the last N bytes of stderr for error diagnostics.
type stderrRing struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (r *stderrRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if r.max > 0 && len(r.buf) > r.max {
		r.buf = r.buf[len(r.buf)-r.max:]
	}
	return len(p), nil
}

func (r *stderrRing) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}
