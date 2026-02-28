// Package client defines the strategy interface for coding-agent backends
// and provides concrete implementations for each supported client.
package client

import (
	"context"
	"fmt"
)

// Model tier constants used across all clients.
const (
	ModelTierHigh   = "high"
	ModelTierMedium = "medium"
	ModelTierLow    = "low"
)

// RunOptions holds all parameters for a single client invocation.
type RunOptions struct {
	Ctx          context.Context // cancellation context; nil means no cancellation
	NativeSID    string          // client-specific session ID for continuity
	Prompt       string
	CWD          string
	ApprovalMode string  // Tenazas approval mode (PLAN, AUTO_EDIT, YOLO)
	Yolo         bool    // shortcut: bypass all permissions
	ModelTier    string  // "high", "medium", "low" — mapped per client
	MaxBudgetUSD float64 // cost ceiling (0 = unlimited)
	OnThought    func(string) // optional callback for chain-of-thought chunks (used by ACP clients)
	OnToolEvent  func(name, status, detail string) // optional callback for tool execution events (used by ACP clients)
	OnIntent     func(string) // optional callback for current task/intent updates (e.g. report_intent)
	OnPermission func(PermissionRequest) PermissionResponse // optional callback for interactive permission prompts
}

// PermissionOption describes one choice in a permission prompt.
type PermissionOption struct {
	OptionID string // unique id to return in the response
	Name     string // human-readable label
	Kind     string // allow_once, allow_always, reject_once, reject_always
}

// PermissionRequest is sent when the agent wants to run a tool.
type PermissionRequest struct {
	ToolCallID string
	Title      string
	Kind       string // tool kind: read, edit, execute, etc.
	Command    string // for shell executions, the raw command
	Options    []PermissionOption
}

// PermissionResponse is the user's decision on a permission request.
type PermissionResponse struct {
	OptionID string // the selected PermissionOption.OptionID
}

// Client is the strategy interface every coding-agent backend must implement.
type Client interface {
	// Name returns the client identifier (e.g. "gemini", "claude-code").
	Name() string

	// Run executes a prompt and streams results.
	// onChunk is called with each text chunk as it streams.
	// onSessionID is called when the client provides its native session ID.
	Run(opts RunOptions, onChunk func(string), onSessionID func(string)) (fullResponse string, err error)

	// SetModels configures the tier-to-model mapping for this client.
	SetModels(models map[string]string)
}

// registry maps client names to constructor functions.
var registry = map[string]func(binPath, logPath string) Client{}

// Register adds a client constructor to the global registry.
func Register(name string, ctor func(binPath, logPath string) Client) {
	registry[name] = ctor
}

// NewClient creates a Client by name using the global registry.
func NewClient(name, binPath, logPath string) (Client, error) {
	ctor, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown client: %q", name)
	}
	return ctor(binPath, logPath), nil
}

// RegisteredClients returns the names of all registered clients.
func RegisteredClients() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	return names
}
